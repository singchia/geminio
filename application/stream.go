package application

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"
	"time"

	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/multiplexer"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/iodefine"
	gnet "github.com/singchia/geminio/pkg/net"
	gsync "github.com/singchia/geminio/pkg/sync"
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

type stream struct {
	*gnet.UnimplementedConn
	// options for End and stream, remember stream dones't own opts
	*opts
	end *End

	// sync hubor for negotiations and registrations
	shub *synchub.SyncHub

	// under layer connection and dialogue
	cn conn.Conn
	dg multiplexer.Dialogue

	// registered rpcs
	rpcMtx sync.RWMutex
	// inflight rpc's cancel
	rpcCancels map[uint64]context.CancelFunc
	// key: method value: RPC
	localRPCs map[string]geminio.RPC
	// key: method value: placeholder
	remoteRPCs map[string]struct{}
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
	dlMtx                       sync.RWMutex
	dlReadSync, dlWriteSync     synchub.Sync
	dlReadChList, dlWriteChList *list.List
	dlRead, dlWrite             time.Time

	// io
	writeInCh chan packet.Packet // for multiple message types

	// close channel
	closeCh chan struct{}
}

func newStream(end *End, cn conn.Conn, dg multiplexer.Dialogue, opts *opts) *stream {
	shub := synchub.NewSyncHub(synchub.OptionTimer(opts.tmr))
	sm := &stream{
		UnimplementedConn: &gnet.UnimplementedConn{},
		opts:              opts,
		end:               end,
		shub:              shub,
		cn:                cn,
		dg:                dg,
		rpcCancels:        make(map[uint64]context.CancelFunc),
		localRPCs:         make(map[string]geminio.RPC),
		remoteRPCs:        make(map[string]struct{}),
		streamOK:          true,
		closeOnce:         new(gsync.Once),
		messageCh:         make(chan *packet.MessagePacket, 32),
		streamCh:          make(chan *packet.StreamPacket, 32),
		failedCh:          make(chan packet.Packet),
		dlReadChList:      list.New(),
		dlWriteChList:     list.New(),
		writeInCh:         make(chan packet.Packet),
		closeCh:           make(chan struct{}),
	}
	go sm.handlePkt()
	return sm
}

func (sm *stream) hasRemoteRPC(method string) bool {
	sm.rpcMtx.RLock()
	defer sm.rpcMtx.RUnlock()

	_, ok := sm.remoteRPCs[method]
	return ok
}

func (sm *stream) StreamID() uint64 {
	return sm.dg.DialogueID()
}

func (sm *stream) ClientID() uint64 {
	return sm.cn.ClientID()
}

func (sm *stream) Meta() []byte {
	return sm.dg.Meta()
}

func (sm *stream) Peer() string {
	return sm.dg.Peer()
}

func (sm *stream) Side() geminio.Side {
	return geminio.Side(sm.dg.Side())
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
			case iodefine.IODiscard:
				sm.log.Infof("stream read in packet but buffer full and discard, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
					sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
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
			case iodefine.IODiscard:
				sm.shub.Error(pkt.ID(), io.ErrShortBuffer)
			case iodefine.IOErr:
				goto FINI
			}
		}
	}
FINI:
	sm.fini()
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
	case *packet.RegisterPacket:
		return sm.handleOutRegisterPacket(realPkt)
	}
	// unknown packet
	return iodefine.IOSuccess
}

// input packet
func (sm *stream) handleInMessagePacket(pkt *packet.MessagePacket) iodefine.IORet {
	sm.log.Tracef("read message packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
	// we don't want block here.
	select {
	case sm.messageCh <- pkt:
	default:
		// TODO ack error back
		return iodefine.IODiscard
	}
	return iodefine.IOSuccess
}

func (sm *stream) handleInMessageAckPacket(pkt *packet.MessageAckPacket) iodefine.IORet {
	if pkt.Data.Error != "" {
		err := errors.New(pkt.Data.Error)
		errored := sm.shub.Error(pkt.ID(), err)
		sm.log.Tracef("read message ack packet with err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s, errord: %t",
			err, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String(), errored)
		return iodefine.IOSuccess
	}
	sm.log.Tracef("read message ack packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
	acked := sm.shub.Ack(pkt.ID(), nil)
	sm.log.Tracef("message ack packet acked: %t, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
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
			custom:   pkt.Data.Custom,
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
		if sm.hijackRPC.pattern == nil {
			sm.doRPC(pkt, methodRPC(sm.hijackRPC.rpc), method, ctx, req, rsp, true)
			return iodefine.IOSuccess
		}
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
		wrapperRPC := func(ctx context.Context, _ string, req geminio.Request, rsp geminio.Response) {
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
		sm.log.Tracef("read response packet with err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s, errored: %t",
			err, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String(), errored)
		return iodefine.IOSuccess
	}
	sm.log.Tracef("read response packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
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

	if sm.opts.dlgt != nil {
		sm.opts.dlgt.RemoteRegistration(method, sm.cn.ClientID(), sm.dg.DialogueID())
	}

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
		return iodefine.IODiscard
	}
	return iodefine.IOSuccess
}

// output packet
func (sm *stream) handleOutMessagePacket(pkt *packet.MessagePacket) iodefine.IORet {
	err := sm.dg.Write(pkt)
	if err != nil {
		if err == io.ErrShortBuffer {
			return iodefine.IODiscard
		}
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
		if err == io.ErrShortBuffer {
			return iodefine.IODiscard
		}
		sm.log.Debugf("write message ack packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			err, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
		return iodefine.IOErr
	}
	return iodefine.IOSuccess
}

func (sm *stream) handleOutRequestPacket(pkt *packet.RequestPacket) iodefine.IORet {
	err := sm.dg.Write(pkt)
	if err != nil {
		if err == io.ErrShortBuffer {
			return iodefine.IODiscard
		}
		sm.log.Debugf("write request packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			err, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
		sm.shub.Error(pkt.ID(), err)
		return iodefine.IOErr
	}
	return iodefine.IOSuccess
}

func (sm *stream) handleOutRegisterPacket(pkt *packet.RegisterPacket) iodefine.IORet {
	err := sm.dg.Write(pkt)
	if err != nil {
		if err == io.ErrShortBuffer {
			return iodefine.IODiscard
		}
		sm.log.Debugf("write register packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			err, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
		return iodefine.IOErr
	}
	return iodefine.IOSuccess
}

func (sm *stream) handleOutStreamPacket(pkt *packet.StreamPacket) iodefine.IORet {
	sm.log.Tracef("write stream packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
	err := sm.dg.Write(pkt)
	if err != nil {
		if err == io.ErrShortBuffer {
			return iodefine.IODiscard
		}
		sm.log.Debugf("write stream packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			err, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
		return iodefine.IOErr
	}
	return iodefine.IOSuccess
}

// doRPC provide generic rpc call
func (sm *stream) doRPC(pkt *packet.RequestPacket, rpc methodRPC, method string, ctx context.Context, req *request, rsp *response, async bool) {
	prog := func() {
		rpc(ctx, method, req, rsp)
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
			sm.log.Debugf("write response packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s, method: %s",
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
		// TODO we should care about msg in writeInCh buffer, it may contains message, request...
	}
	// collect channels
	sm.writeInCh = nil

	// the outside should care about message and stream channel status
	close(sm.messageCh)
	close(sm.streamCh)

	// collect timer
	if sm.tmrOwner == sm {
		sm.tmr.Close()
	}
	sm.tmr = nil

	// collect close
	close(sm.closeCh)

	if sm.dg.DialogueID() == 1 {
		// the master stream
		sm.end.fini()
	}
	// reclaim stream from end
	sm.end.delStream(sm.dg.DialogueID())

	sm.log.Debugf("stream finished, clientID: %d, dialogueID: %d",
		sm.cn.ClientID(), sm.dg.DialogueID())
}
