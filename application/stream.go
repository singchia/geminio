package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/jumboframes/armorigo/log"

	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/multiplexer"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/iodefine"
	gsync "github.com/singchia/geminio/pkg/sync"
	"github.com/singchia/go-timer/v2"
)

var (
	ErrMismatchStreamID = errors.New("mismatch streamID")
	ErrMismatchClientID = errors.New("mismatch clientID")
)

const (
	registrationFormat = "%d-%d-registration"
)

type methodRPC geminio.HijackRPC

type streamOpts struct {
	// packet factory
	pf *packet.PacketFactory
	// logger
	log log.Logger
	// timer
	tmr        timer.Timer
	tmrOutside bool
	// meta
	meta []byte
}

type stream struct {
	// options
	streamOpts

	// sync hubor for negotiations and registrations
	shub *synchub.SyncHub

	// under layer dialogue and connection
	dg multiplexer.Dialogue
	cn conn.Conn

	// registered rpcs
	rpcMtx     sync.RWMutex
	localRPCs  map[string]geminio.RPC // key: method value: RPC
	remoteRPCs map[string]struct{}    // key: method value: placeholder

	// hijacks
	hijackRPC geminio.HijackRPC

	// mtx protects follows
	mtx       sync.RWMutex
	streamOK  bool
	closeOnce *gsync.Once

	// app layer messages
	// raw cache
	cache     []byte
	messageCh chan *packet.MessagePacket
	streamCh  chan *packet.StreamPacket
	failedCh  chan packet.Packet

	// io
	writeInCh chan packet.Packet // for multiple message types

	// close channel
	closeCh chan struct{}
}

// geminio.RPCer
func (sm *stream) NewRequest(data []byte, opts ...geminio.OptionRequestAttribute) geminio.Request {
	id := sm.pf.NewPacketID()
	req := &request{
		RequestAttribute: &geminio.RequestAttribute{},
		data:             data,
		id:               id,
		clientID:         sm.cn.ClientID(),
		streamID:         sm.dg.DialogueID(),
	}
	for _, opt := range opts {
		opt(req.RequestAttribute)
	}
	return req
}

func (sm *stream) Register(ctx context.Context, method string) error {
	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return io.EOF
	}
	pkt := sm.pf.NewRegisterPacketWithSessionID(sm.dg.DialogueID(), []byte(method))
	sync := sm.shub.New(pkt.ID(), synchub.WithContext(ctx))
	sm.writeInCh <- pkt
	sm.mtx.RUnlock()

}

func (sm *stream) Call(ctx context.Context, method string, req geminio.Request) (geminio.Response, error) {
	if req.ClientID() != sm.cn.ClientID() {
		return nil, ErrMismatchClientID
	}
	if req.StreamID() != sm.dg.DialogueID() {
		return nil, ErrMismatchStreamID
	}
	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return nil, io.EOF
	}
	pkt := sm.pf.NewRequestPacketWithIDAndSessionID(req.ID(), sm.dg.DialogueID(), []byte(method), req.Data())
	if req.Timeout() != 0 {
		// if timeout exists, we should deliver it
		pkt.Data.Deadline = time.Now().Add(req.Timeout())
	}
	deadline, ok := ctx.Deadline()
	if ok {
		// if deadline exists, we should deliver it
		pkt.Data.Context.Deadline = deadline
	}
	var sync synchub.Sync
	opts := []synchub.SyncOption{}
	if req.Timeout() != 0 {
		// the sync may has timeout
		opts = append(opts, synchub.WithTimeout(req.Timeout()))
	}
	sync = sm.shub.New(req.ID(), opts...)
	sm.writeInCh <- pkt
	sm.mtx.RUnlock()

	// we don't set ctx to sync, because select perform better
	select {
	case <-ctx.Done():
		sync.Cancel(false)

		if ctx.Err() == context.DeadlineExceeded {
			// we don't deliver deadline exceeded since the pkt already has it
			return nil, ctx.Err()
		}
		sm.mtx.RLock()
		if !sm.streamOK {
			sm.mtx.RUnlock()
			return nil, io.EOF
		}
		// notify peer if context Canceled
		cancelType := packet.RequestCancelTypeCanceled
		cancelPkt := sm.pf.NewRequestCancelPacketWithIDAndSessionID(pkt.ID(), sm.dg.DialogueID(), cancelType)
		sm.writeInCh <- cancelPkt
		sm.mtx.RUnlock()
		return nil, ctx.Err()

	case event := <-sync.C():
		if event.Error != nil {
			sm.log.Debugf("request return err: %s, clientID: %d, dialogueID: %d, reqID: %d",
				event.Error, sm.cn.ClientID(), sm.dg.DialogueID(), req.ID())
			return nil, event.Error
		}
		rsp := event.Ack.(*response)
		return rsp, nil
	}
}

func (sm *stream) CallAsync(ctx context.Context, method string, req geminio.Request, ch chan *geminio.Call) (*geminio.Call, error) {
	if req.ClientID() != sm.cn.ClientID() {
		return nil, ErrMismatchClientID
	}
	if req.StreamID() != sm.dg.DialogueID() {
		return nil, ErrMismatchStreamID
	}
	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return nil, io.EOF
	}
	pkt := sm.pf.NewRequestPacketWithIDAndSessionID(req.ID(), sm.dg.DialogueID(), []byte(method), req.Data())
	// deadline and timeout for peer
	if req.Timeout() != 0 {
		pkt.Data.Deadline = time.Now().Add(req.Timeout())
	}
	deadline, ok := ctx.Deadline()
	if ok {
		pkt.Data.Context.Deadline = deadline
	}
	if ch == nil {
		ch = make(chan *geminio.Call, 1)
	}
	call := &geminio.Call{
		Method:  method,
		Request: req,
		Done:    ch,
	}
	// deadline and timeout for local
	opts := []synchub.SyncOption{synchub.WithContext(ctx), synchub.WithCallback(func(event *synchub.Event) {
		if event.Error != nil {
			sm.log.Debugf("request packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
				event.Error, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
			call.Error = event.Error
			ch <- call
			return
		}
		sm.log.Tracef("response return succeed, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
		ch <- call
		return
	})}
	if req.Timeout() != 0 {
		opts = append(opts, synchub.WithTimeout(req.Timeout()))
	}
	// Add a new sync for the async call
	sm.shub.New(pkt.ID(), opts...)
	sm.writeInCh <- pkt
	sm.mtx.RUnlock()
	return call, nil
}

// geminio.Messager
func (sm *stream) NewMessage(data []byte, opts ...geminio.OptionMessageAttribute) geminio.Message {
	id := sm.pf.NewPacketID()
	msg := &message{
		MessageAttribute: &geminio.MessageAttribute{},
		data:             data,
		id:               id,
		clientID:         sm.cn.ClientID(),
		streamID:         sm.dg.DialogueID(),
		sm:               sm,
	}
	for _, opt := range opts {
		opt(msg.MessageAttribute)
	}
	return msg
}

// Publish to peer, a sync function
func (sm *stream) Publish(ctx context.Context, msg geminio.Message) error {
	if msg.ClientID() != sm.cn.ClientID() {
		return ErrMismatchClientID
	}
	if msg.StreamID() != sm.dg.DialogueID() {
		return ErrMismatchStreamID
	}
	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return io.EOF
	}

	pkt := sm.pf.NewMessagePacketWithIDAndSessionID(msg.ID(), sm.dg.DialogueID(), nil, msg.Data())
	if msg.Timeout() != 0 {
		pkt.Data.Deadline = time.Now().Add(msg.Timeout())
	}
	deadline, ok := ctx.Deadline()
	if ok {
		pkt.Data.Context.Deadline = deadline
	}

	if msg.Cnss() == geminio.CnssAtMostOnce {
		// if consistency is set to be AtMostOnce, we don't care about context or timeout
		sm.writeInCh <- pkt
		sm.mtx.RUnlock()
		return nil
	}
	var sync synchub.Sync
	opts := []synchub.SyncOption{synchub.WithContext(ctx)}
	if msg.Timeout() != 0 {
		// the sync may has timeout
		opts = append(opts, synchub.WithTimeout(msg.Timeout()))
	}
	sync = sm.shub.New(msg.ID(), opts...)
	sm.writeInCh <- pkt
	sm.mtx.RUnlock()

	event := <-sync.C()
	if event.Error != nil {
		sm.log.Debugf("message return err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			event.Error, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
		// TODO we did't separate err from lib and user
		return event.Error
	}
	sm.log.Tracef("message return succeed, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
	return nil
}

func (sm *stream) PublishAsync(ctx context.Context, msg geminio.Message, ch chan *geminio.Publish) (*geminio.Publish, error) {
	if msg.ClientID() != sm.cn.ClientID() {
		return nil, ErrMismatchClientID
	}
	if msg.StreamID() != sm.dg.DialogueID() {
		return nil, ErrMismatchStreamID
	}
	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return nil, io.EOF
	}
	now := time.Now()
	pkt := sm.pf.NewMessagePacketWithIDAndSessionID(msg.ID(), sm.dg.DialogueID(), nil, msg.Data())
	if msg.Timeout() != 0 {
		pkt.Data.Deadline = now.Add(msg.Timeout())
	}
	deadline, ok := ctx.Deadline()
	if ok {
		pkt.Data.Context.Deadline = deadline
	}

	if msg.Cnss() == geminio.CnssAtMostOnce {
		// if consistency is set to be AtMostOnce, we don't care about context or timeout or async
		sm.writeInCh <- pkt
		sm.mtx.RUnlock()
		return nil, nil
	}
	if ch == nil {
		// we don't want block here
		ch = make(chan *geminio.Publish, 1)
	}
	publish := &geminio.Publish{
		Message: msg,
		Done:    ch,
	}
	// deadline and timeout for local
	opts := []synchub.SyncOption{synchub.WithContext(ctx), synchub.WithCallback(func(event *synchub.Event) {
		if event.Error != nil {
			sm.log.Debugf("message packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
				event.Error, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
			publish.Error = event.Error
			ch <- publish
			return
		}
		sm.log.Tracef("message return succeed, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
		ch <- publish
		return
	})}
	if msg.Timeout() != 0 {
		// the sync may has timeout
		opts = append(opts, synchub.WithTimeout(msg.Timeout()))
	}
	// Add a new sync for the async publish
	sm.shub.New(pkt.ID(), opts...)
	sm.writeInCh <- pkt
	sm.mtx.RUnlock()
	return publish, nil
}

// return EOF means the stream is closed
func (sm *stream) Receive() (geminio.Message, error) {
	pkt, ok := <-sm.messageCh
	if !ok {
		sm.log.Debugf("stream receive EOF, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
		return nil, io.EOF
	}
	msg := &message{
		MessageAttribute: &geminio.MessageAttribute{
			Timeout: pkt.Data.Timeout,
			Cnss:    geminio.Cnss(pkt.Cnss),
		},
		data:     pkt.Data.Value,
		id:       pkt.PacketID,
		clientID: sm.cn.ClientID(),
		streamID: sm.dg.DialogueID(),
		sm:       sm,
	}
	return msg, nil
}

func (sm *stream) handlePkt() {
	readInCh := sm.dg.ReadC()
	writeInCh := sm.writeInCh

	for {
		select {
		case pkt, ok := <-readInCh:
			if !ok {
				goto FINI
			}
			sm.log.Tracef("stream read in packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
				sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
			ret := sm.handleIn(pkt)
			switch ret {
			case iodefine.IOSuccess:
				continue
			case iodefine.IOErr:
				goto FINI
			}

		case pkt, ok := <-writeInCh:
			if !ok {
				// BUG! shoud never be here.
				goto FINI
			}
			sm.log.Tracef("stream write in packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
				sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
			ret := sm.handleOut(pkt)
			switch ret {
			case iodefine.IOSuccess:
				continue
			case iodefine.IOErr:
				goto FINI
			}
		}
	}
FINI:
}

func (sm *stream) handleIn(pkt packet.Packet) iodefine.IORet {
	switch realPkt := pkt.(type) {
	case *packet.MessagePacket:
		return sm.handleInMessagePacket(realPkt)
	case *packet.MessageAckPacket:
		return sm.handleInMessageAckPacket(realPkt)
	case *packet.RequestPacket:
		return sm.handleInRequestPacket(realPkt)
	case *packet.RequestCancelPacket:
		// TODO
	case *packet.ResponsePacket:
		return sm.handleInResponsePacket(realPkt)
	case *packet.RegisterPacket:
		return sm.handleInRegisterPacket(realPkt)
	case *packet.RegisterAckPacket:
		return sm.handleInRegisterAckPacket(realPkt)
	case *packet.StreamPacket:
		return sm.handleInStreamPacket(realPkt)
	}
	// unknown packet
	return iodefine.IOErr
}

func (sm *stream) handleOut(pkt packet.Packet) iodefine.IORet {
	switch realPkt := pkt.(type) {
	case *packet.MessagePacket:
		return sm.handleOutMessagePacket(realPkt)
	case *packet.MessageAckPacket:
		return sm.handleOutMessageAckPacket(realPkt)
	case *packet.RequestPacket:
		return sm.handleOutRequestPacket(realPkt)
	case *packet.StreamPacket:
		return sm.handleOutStreamPacket(realPkt)
	}
	// unknown packet
	return iodefine.IOSuccess
}

// input packet
func (sm *stream) handleInMessagePacket(pkt *packet.MessagePacket) iodefine.IORet {
	sm.log.Tracef("read message packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
	// TODO add select, we don't want block here.
	sm.messageCh <- pkt
	return iodefine.IOSuccess
}

func (sm *stream) handleInMessageAckPacket(pkt *packet.MessageAckPacket) iodefine.IORet {
	if pkt.Data.Error != "" {
		err := errors.New(pkt.Data.Error)
		errored := sm.shub.Error(pkt.ID(), err)
		sm.log.Tracef("read message ack packet with err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s, errord: %b",
			err, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String(), errored)
		return iodefine.IOSuccess
	}
	sm.log.Tracef("read message ack packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
	acked := sm.shub.Ack(pkt.ID(), nil)
	sm.log.Tracef("message ack packet acked: %b, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
		acked, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
	return iodefine.IOSuccess
}

func (sm *stream) handleInRequestPacket(pkt *packet.RequestPacket) iodefine.IORet {
	method := string(pkt.Data.Key)
	sm.log.Tracef("read request packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s, method: %s",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String(), method)
	// we use Data.Key as method
	req, rsp :=
		&request{
			// we use Data.Value as data
			data:     pkt.Data.Value,
			id:       pkt.PacketID,
			method:   method,
			clientID: sm.cn.ClientID(),
			streamID: sm.dg.DialogueID(),
		},
		&response{
			method:    method,
			requestID: pkt.ID(),
			clientID:  sm.cn.ClientID(),
			streamID:  sm.dg.DialogueID(),
		}
	// TODO context
	// hijack exist
	if sm.hijackRPC != nil {
		sm.doRPC(pkt, methodRPC(sm.hijackRPC), method, context.TODO(), req, rsp, true)
		return iodefine.IOSuccess
	}
	// registered RPC lookup and call
	sm.rpcMtx.RLock()
	rpc, ok := sm.localRPCs[method]
	sm.rpcMtx.RUnlock()
	if ok {
		wrapperRPC := func(_ string, ctx context.Context, req geminio.Request, rsp geminio.Response) {
			rpc(ctx, req, rsp)
		}
		sm.doRPC(pkt, wrapperRPC, method, context.TODO(), req, rsp, true)
		return iodefine.IOSuccess
	}

	// no rpc found, return to call error, note that this error is not set to response error
	err := fmt.Errorf("no such rpc: %s", method)
	rspPkt := sm.pf.NewResponsePacket(pkt.ID(), []byte(method), nil, err)
	err = sm.dg.Write(rspPkt)
	if err != nil {
		sm.log.Debugf("write no such rpc response packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s, method: %s",
			err, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String(), method)
		return iodefine.IOErr
	}
	return iodefine.IOSuccess
}

func (sm *stream) handleInResponsePacket(pkt *packet.ResponsePacket) iodefine.IORet {
	if pkt.Data.Error != "" {
		err := errors.New(pkt.Data.Error)
		errored := sm.shub.Error(pkt.ID(), err)
		sm.log.Tracef("read response packet with err: %d, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s, errored: %b,",
			sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String(), errored)
		return iodefine.IOSuccess
	}
	sm.log.Tracef("read response packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s, method: %s,",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
	rsp := &response{
		data:      pkt.Data.Value,
		requestID: pkt.ID(),
		clientID:  sm.cn.ClientID(),
		streamID:  sm.dg.DialogueID(),
	}
	// the return is ignored for now
	// TODO consistency optimize
	_ = sm.shub.Ack(pkt.ID(), rsp)
	return iodefine.IOSuccess
}

func (sm *stream) handleInRegisterPacket(pkt *packet.RegisterPacket) iodefine.IORet {
	method := pkt.Method()
	sm.log.Tracef("read register packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s, method: %s",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String(), method)

	retPkt := sm.pf.NewRegisterAckPacket(pkt.ID(), nil)
	err := sm.dg.Write(retPkt)
	if err != nil {
		sm.log.Debugf("write register ack packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s, method: %s",
			err, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String(), method)
		return iodefine.IOErr
	}

	// TODO delegate for remote registration

	sm.rpcMtx.Lock()
	sm.remoteRPCs[method] = struct{}{}
	sm.rpcMtx.Unlock()

	// to notify the method is registing, in case of we're waiting for the method ready
	syncID := fmt.Sprintf(registrationFormat, sm.cn.ClientID(), sm.dg.DialogueID())
	sm.shub.DoneSub(syncID, method)

	return iodefine.IOSuccess
}

func (sm *stream) handleInRegisterAckPacket(pkt *packet.RegisterAckPacket) iodefine.IORet {
	sm.log.Tracef("read register ack packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
	sm.shub.Done(pkt.ID())
	return iodefine.IOSuccess
}

func (sm *stream) handleInStreamPacket(pkt *packet.StreamPacket) iodefine.IORet {
	sm.log.Tracef("read stream packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
	sm.streamCh <- pkt
	return iodefine.IOSuccess
}

// output packet
func (sm *stream) handleOutMessagePacket(pkt *packet.MessagePacket) iodefine.IORet {
	err := sm.dg.Write(pkt)
	if err != nil {
		sm.log.Debugf("write message packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			err, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
		// notify the publish side the err
		sm.shub.Error(pkt.ID(), err)
		return iodefine.IOErr
	}
	return iodefine.IOSuccess
}

func (sm *stream) handleOutMessageAckPacket(pkt *packet.MessageAckPacket) iodefine.IORet {
	err := sm.dg.Write(pkt)
	if err != nil {
		sm.log.Debugf("write message ack packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			err, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
		return iodefine.IOErr
	}
	return iodefine.IOSuccess
}

func (sm *stream) handleOutRequestPacket(pkt *packet.RequestPacket) iodefine.IORet {
	err := sm.dg.Write(pkt)
	if err != nil {
		sm.log.Debugf("write request packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			err, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
		sm.shub.Error(pkt.ID(), err)
		return iodefine.IOErr
	}
	return iodefine.IOSuccess
}

func (sm *stream) handleOutStreamPacket(pkt *packet.StreamPacket) iodefine.IORet {
	err := sm.dg.Write(pkt)
	if err != nil {
		sm.log.Debugf("write stream packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			err, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
		return iodefine.IOErr
	}
	return iodefine.IOSuccess
}

// doRPC provide generic rpc call
func (sm *stream) doRPC(pkt *packet.RequestPacket, rpc methodRPC, method string, ctx context.Context, req *request, rsp *response, async bool) {
	prog := func() {
		rpc(method, ctx, req, rsp)
		rspPkt := sm.pf.NewResponsePacket(pkt.ID(), []byte(req.method), rsp.data, rsp.err)
		err := sm.dg.Write(rspPkt)
		if err != nil {
			// Write error, the response cannot be delivered, so should be debuged
			sm.log.Debug("write response packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s, method: %s",
				err, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String(), method)
			// TOD do we need finish the stream while write err
			// sm.fini()
			return
		}
		sm.log.Tracef("write response succeed, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s, method: %s",
			sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String(), method)
	}
	if async {
		go prog()
	} else {
		prog()
	}
}

func (sm *stream) Close() {
	sm.closeOnce.Do(func() {
		sm.mtx.RLock()
		defer sm.mtx.RUnlock()
		if !sm.streamOK {
			return
		}

		sm.log.Debugf("stream async close, clientID: %d, dialogueID: %d",
			sm.cn.ClientID(), sm.dg.DialogueID())
		// diglogue Close will leads to fini and then close the readInCh
		sm.dg.Close()
	})
}

// finish and reclaim resources
func (sm *stream) fini() {
	sm.log.Debugf("stream finishing, clientID: %d, dialogueID: %d",
		sm.cn.ClientID(), sm.dg.DialogueID())

	sm.mtx.Lock()
	// collect shub, and all syncs will be close notified
	sm.shub.Close()
	sm.shub = nil

	sm.streamOK = false
	close(sm.writeInCh)
	sm.mtx.Unlock()

	for range sm.writeInCh {
		// TODO we show care about msg in writeInCh buffer, it may contains message, request...
	}
	// collect channels
	sm.writeInCh = nil

	// the outside should care about message and stream channel status
	close(sm.messageCh)
	close(sm.streamCh)

	// collect timer
	if !sm.tmrOutside {
		sm.tmr.Close()
	}
	sm.tmr = nil

	sm.log.Debugf("stream finished, clientID: %d, dialogueID: %d",
		sm.cn.ClientID(), sm.dg.DialogueID())
}
