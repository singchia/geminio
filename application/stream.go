package application

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/jumboframes/armorigo/log"

	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/multiplexer"
	"github.com/singchia/geminio/options"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/iodefine"
	gnet "github.com/singchia/geminio/pkg/net"
	gsync "github.com/singchia/geminio/pkg/sync"
	"github.com/singchia/go-timer/v2"
)

var (
	ErrMismatchStreamID      = errors.New("mismatch streamID")
	ErrMismatchClientID      = errors.New("mismatch clientID")
	ErrRemoteRPCUnregistered = errors.New("remote rpc unregistered")
)

const (
	registrationFormat = "%d-%d-registration"
)

type patternRPC struct {
	match   bool
	pattern *regexp.Regexp
	rpc     geminio.HijackRPC
}

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
	*gnet.UnimplementedConn
	// options
	streamOpts

	// sync hubor for negotiations and registrations
	shub *synchub.SyncHub

	// under layer dialogue and connection
	dg multiplexer.Dialogue
	cn conn.Conn

	// registered rpcs
	rpcMtx     sync.RWMutex
	rpcCancels map[uint64]context.CancelFunc // inflight rpc's cancel
	localRPCs  map[string]geminio.RPC        // key: method value: RPC
	remoteRPCs map[string]struct{}           // key: method value: placeholder
	// hijack
	hijackRPC *patternRPC

	// mtx protects follows
	mtx       sync.RWMutex
	streamOK  bool
	closeOnce *gsync.Once

	// app layer messages
	// raw cache
	cache    []byte
	cacheMtx sync.Mutex

	messageCh chan *packet.MessagePacket
	streamCh  chan *packet.StreamPacket
	failedCh  chan packet.Packet

	// deadline mtx protects SetDeadline, SetReadDeadline, SetWriteDeadline and all Read Write
	dlMtx                       sync.Mutex
	dlReadSync, dlWriteSync     synchub.Sync
	dlReadChList, dlWriteChList *list.List
	dlRead, dlWrite             time.Time

	// io
	writeInCh chan packet.Packet // for multiple message types

	// close channel
	closeCh chan struct{}
}

// geminio.RPCer
func (sm *stream) NewRequest(data []byte) geminio.Request {
	id := sm.pf.NewPacketID()
	req := &request{
		//RequestAttribute: &geminio.RequestAttribute{},
		data:     data,
		id:       id,
		clientID: sm.cn.ClientID(),
		streamID: sm.dg.DialogueID(),
	}
	return req
}

func (sm *stream) Register(ctx context.Context, method string, rpc geminio.RPC) error {
	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return io.EOF
	}
	pkt := sm.pf.NewRegisterPacketWithSessionID(sm.dg.DialogueID(), []byte(method))
	sync := sm.shub.New(pkt.ID())
	sm.writeInCh <- pkt
	sm.mtx.RUnlock()
	select {
	case event := <-sync.C():
		if event.Error != nil {
			sm.log.Debugf("register err: %s, clientID: %d, dialogueID: %d, packetID: %d",
				event.Error, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID())
			return event.Error
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	sm.rpcMtx.Lock()
	defer sm.rpcMtx.Unlock()
	sm.localRPCs[method] = rpc
	return nil
}

func (sm *stream) Call(ctx context.Context, method string, req geminio.Request, opts ...*options.CallOptions) (geminio.Response, error) {
	if req.ClientID() != sm.cn.ClientID() {
		return nil, ErrMismatchClientID
	}
	if req.StreamID() != sm.dg.DialogueID() {
		return nil, ErrMismatchStreamID
	}
	co := options.MergeCallOptions(opts...)

	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return nil, io.EOF
	}

	// check remote RPC exists
	sm.rpcMtx.RLock()
	_, ok := sm.remoteRPCs[method]
	if !ok {
		sm.rpcMtx.RUnlock()
		return nil, ErrRemoteRPCUnregistered
	}
	sm.rpcMtx.RUnlock()
	// transfer to underlayer packet
	pkt := sm.pf.NewRequestPacketWithIDAndSessionID(req.ID(), sm.dg.DialogueID(), []byte(method), req.Data())
	if co.Timeout != nil {
		// if timeout exists, we should deliver it
		pkt.Data.Deadline = time.Now().Add(*co.Timeout)
	}
	deadline, ok := ctx.Deadline()
	if ok {
		// if deadline exists, we should deliver it
		pkt.Data.Context.Deadline = deadline
	}
	var sync synchub.Sync
	syncOpts := []synchub.SyncOption{}
	if co.Timeout != nil {
		// the sync may has timeout
		syncOpts = append(syncOpts, synchub.WithTimeout(*co.Timeout))
	}
	sync = sm.shub.New(req.ID(), syncOpts...)
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
	opts := []synchub.SyncOption{synchub.WithContext(ctx),
		synchub.WithCallback(func(event *synchub.Event) {
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

func (sm *stream) Hijack(rpc geminio.HijackRPC, opts ...*options.HijackOptions) error {
	pRPC := &patternRPC{}
	fo := options.MergeHijackOptions(opts...)
	if fo.Pattern != nil {
		reg, err := regexp.Compile(*fo.Pattern)
		if err != nil {
			return err
		}
		pRPC.pattern = reg
	}
	if fo.Match != nil {
		pRPC.match = *fo.Match
	}
	pRPC.rpc = rpc
	sm.hijackRPC = pRPC
	return nil
}

// geminio.Messager
func (sm *stream) NewMessage(data []byte) geminio.Message {
	id := sm.pf.NewPacketID()
	msg := &message{
		data:     data,
		id:       id,
		clientID: sm.cn.ClientID(),
		streamID: sm.dg.DialogueID(),
		sm:       sm,
	}
	return msg
}

func (sm *stream) ackMessage(pktID uint64, err error) error {
	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return io.EOF
	}

	pkt := sm.pf.NewMessageAckPacketWithSessionID(sm.dg.DialogueID(), pktID, err)
	sm.writeInCh <- pkt
	return nil
}

// Publish to peer, a sync function
func (sm *stream) Publish(ctx context.Context, msg geminio.Message, opts ...*options.PublishOptions) error {
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

	if msg.Cnss() == options.CnssAtMostOnce {
		// if consistency is set to be AtMostOnce, we don't care about context or timeout
		sm.writeInCh <- pkt
		sm.mtx.RUnlock()
		return nil
	}
	var sync synchub.Sync
	syncOpts := []synchub.SyncOption{synchub.WithContext(ctx)}
	if msg.Timeout() != 0 {
		// the sync may has timeout
		syncOpts = append(syncOpts, synchub.WithTimeout(msg.Timeout()))
	}
	sync = sm.shub.New(msg.ID(), syncOpts...)
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

	if msg.Cnss() == options.CnssAtMostOnce {
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
func (sm *stream) Receive(ctx context.Context) (geminio.Message, error) {
	select {
	case pkt, ok := <-sm.messageCh:
		if !ok {
			sm.log.Debugf("stream receive EOF, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
				sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
			return nil, io.EOF
		}
		msg := &message{
			timeout:  pkt.Data.Timeout,
			cnss:     options.Cnss(pkt.Cnss),
			data:     pkt.Data.Value,
			id:       pkt.PacketID,
			clientID: sm.cn.ClientID(),
			streamID: sm.dg.DialogueID(),
			sm:       sm,
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// geminio.Raw
func (sm *stream) Read(b []byte) (int, error) {
	sm.cacheMtx.Lock()
	if sm.cache != nil && len(sm.cache) != 0 {
		n := copy(b, sm.cache)
		sm.cache = sm.cache[n:]
		sm.cacheMtx.Unlock()
		return n, nil
	}
	sm.cacheMtx.Unlock()

	sm.dlMtx.Lock()
	defer sm.dlMtx.Unlock()

	if !sm.dlRead.IsZero() && time.Now().After(sm.dlRead) {
		select {
		case pkt, ok := <-sm.streamCh:
			if !ok {
				return 0, io.EOF
			}
			sm.cacheMtx.Lock()
			n := copy(b, pkt.Data)
			sm.cache = pkt.Data[n:]
			sm.cacheMtx.Unlock()
			return n, nil
		default:
			return 0, os.ErrDeadlineExceeded
		}
	} else {
		dlCh := make(chan struct{})
		sm.dlReadChList.PushBack(dlCh)
		select {
		case pkt, ok := <-sm.streamCh:
			if !ok {
				return 0, io.EOF
			}
			sm.cacheMtx.Lock()
			n := copy(b, pkt.Data)
			sm.cache = pkt.Data[n:]
			sm.cacheMtx.Unlock()
			return n, nil
		case <-dlCh:
			return 0, os.ErrDeadlineExceeded
		}
	}
}

func (sm *stream) Write(b []byte) (int, error) {
	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return 0, io.EOF
	}

	newb := make([]byte, len(b))
	copy(newb, b)
	pkt := sm.pf.NewStreamPacketWithSessionID(sm.dg.DialogueID(), newb)

	sm.writeInCh <- pkt
	return len(b), nil
}

func (sm *stream) LocalAddr() net.Addr {
	return sm.cn.LocalAddr()
}

func (sm *stream) RemoteAddr() net.Addr {
	return sm.cn.RemoteAddr()
}

// SetDeadline sets the read and write deadlines associated
// with the connection. It is equivalent to calling both
// SetReadDeadline and SetWriteDeadline.
//
// A deadline is an absolute time after which I/O operations
// fail instead of blocking. The deadline applies to all future
// and pending I/O, not just the immediately following call to
// Read or Write. After a deadline has been exceeded, the
// connection can be refreshed by setting a deadline in the future.
//
// If the deadline is exceeded a call to Read or Write or to other
// I/O methods will return an error that wraps os.ErrDeadlineExceeded.
// This can be tested using errors.Is(err, os.ErrDeadlineExceeded).
// The error's Timeout method will return true, but note that there
// are other possible errors for which the Timeout method will
// return true even if the deadline has not been exceeded.
//
// An idle timeout can be implemented by repeatedly extending
// the deadline after successful Read or Write calls.
//
// A zero value for t means I/O operations will not time out.
func (sm *stream) SetDeadline(t time.Time) error {
	sm.dlMtx.Lock()
	defer sm.dlMtx.Unlock()

	err := sm.setReadDeadline(t)
	if err != nil {
		return err
	}
	err = sm.setWriteDeadline(t)
	if err != nil {
		return err
	}
	return nil
}

// SetReadDeadline sets the deadline for future Read calls
// and any currently-blocked Read call.
// A zero value for t means Read will not time out.
func (sm *stream) SetReadDeadline(t time.Time) error {
	sm.dlMtx.Lock()
	defer sm.dlMtx.Unlock()
	return sm.setReadDeadline(t)
}

func (sm *stream) setReadDeadline(t time.Time) error {
	if sm.dlReadSync != nil {
		sm.dlReadSync.Cancel(false)
		sm.dlReadSync = nil
	}
	duration := time.Now().Sub(t)
	if duration > 0 {
		sm.dlReadSync = sm.shub.Add(&struct{}{},
			synchub.WithTimeout(duration),
			synchub.WithCallback(func(event *synchub.Event) {
				e := sm.dlReadChList.Front()
				for e != nil {
					ch, _ := e.Value.(chan struct{})
					// deadline exceeds and notify all waiting Read/Write
					close(ch)
					olde := e
					e = e.Next()
					sm.dlReadChList.Remove(olde)
				}
			}))
	}
	sm.dlRead = t
	return nil
}

// SetWriteDeadline sets the deadline for future Write calls
// and any currently-blocked Write call.
// Even if write times out, it may return n > 0, indicating that
// some of the data was successfully written.
// A zero value for t means Write will not time out.
func (sm *stream) SetWriteDeadline(t time.Time) error {
	sm.dlMtx.Lock()
	defer sm.dlMtx.Unlock()
	return sm.setWriteDeadline(t)
}

func (sm *stream) setWriteDeadline(t time.Time) error {
	if sm.dlWriteSync != nil {
		sm.dlWriteSync.Cancel(false)
		sm.dlWriteSync = nil
	}
	duration := time.Now().Sub(t)
	if duration > 0 {
		sm.dlWriteSync = sm.shub.Add(&struct{}{},
			synchub.WithTimeout(duration),
			synchub.WithCallback(func(event *synchub.Event) {
				e := sm.dlWriteChList.Front()
				for e != nil {
					ch, _ := e.Value.(chan struct{})
					// deadline exceeds and notify all waiting Read/Write
					close(ch)
					olde := e
					e = e.Next()
					sm.dlWriteChList.Remove(olde)
				}
			}))
	}
	sm.dlWrite = t
	return nil
}

// main handle logic
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
		return sm.handleInRequestCancelPacket(realPkt)
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
	// setup context
	ctx, cancel := context.Background(), context.CancelFunc(nil)
	if !pkt.Data.Deadline.IsZero() || !pkt.Data.Context.Deadline.IsZero() {
		deadline := pkt.Data.Deadline
		if deadline.IsZero() || (!deadline.IsZero() &&
			!pkt.Data.Context.Deadline.IsZero() &&
			pkt.Data.Context.Deadline.Before(deadline)) {
			deadline = pkt.Data.Context.Deadline
		}
		ctx, cancel = context.WithDeadline(ctx, deadline)
		sm.rpcMtx.Lock()
		sm.rpcCancels[pkt.ID()] = cancel
		sm.rpcMtx.Unlock()
	}
	// hijack exist
	if sm.hijackRPC != nil {
		matched := sm.hijackRPC.pattern.Match([]byte(method))
		if (sm.hijackRPC.match && matched) || (!sm.hijackRPC.match && !matched) {
			// do RPC and cancel the context
			sm.doRPC(pkt, methodRPC(sm.hijackRPC.rpc), method, ctx, req, rsp, true)
			return iodefine.IOSuccess
		}
	}
	// registered RPC lookup and call
	sm.rpcMtx.RLock()
	rpc, ok := sm.localRPCs[method]
	sm.rpcMtx.RUnlock()
	if ok {
		wrapperRPC := func(_ string, ctx context.Context, req geminio.Request, rsp geminio.Response) {
			rpc(ctx, req, rsp)
		}
		// do RPC and cancel the context
		sm.doRPC(pkt, wrapperRPC, method, ctx, req, rsp, true)
		return iodefine.IOSuccess
	}

	// release the context
	if cancel != nil {
		sm.rpcMtx.Lock()
		delete(sm.rpcCancels, pkt.ID())
		cancel()
		sm.rpcMtx.Unlock()
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

func (sm *stream) handleInRequestCancelPacket(pkt *packet.RequestCancelPacket) iodefine.IORet {
	sm.rpcMtx.Lock()
	cancel, ok := sm.rpcCancels[pkt.ID()]
	if ok {
		delete(sm.rpcCancels, pkt.ID())
		cancel()
	}
	sm.rpcMtx.Unlock()
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
	select {
	case sm.streamCh <- pkt:
	default:
		// TODO drop the packet, we don't want block here
	}
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
		// once the rpc complete, we should cancel the context
		sm.rpcMtx.Lock()
		cancel, ok := sm.rpcCancels[pkt.ID()]
		if ok {
			delete(sm.rpcCancels, pkt.ID())
			cancel()
		}
		sm.rpcMtx.Unlock()

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

func (sm *stream) Close() error {
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
	return nil
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
