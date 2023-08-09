package application

import (
	"errors"
	"fmt"
	"sync"

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
	cache        []byte
	messageOutCh chan *packet.MessagePacket
	streamCh     chan *packet.StreamPacket

	// io
	writeInCh chan packet.Packet // for multiple message types
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

		case pkt, ok := <-writeInCh:
			if !ok {
				goto FINI
			}
			sm.log.Tracef("stream write in packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
				sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
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
	case *packet.MessageAckPacket:
	case *packet.RequestPacket:
	case *packet.StreamPacket:
	}
	// unknown packet
	return iodefine.IOSuccess
}

// input packet
func (sm *stream) handleInMessagePacket(pkt *packet.MessagePacket) iodefine.IORet {
	sm.log.Tracef("read message packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
	// TODO add select, we don't want block here.
	sm.messageOutCh <- pkt
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
	// hijack exist
	if sm.hijackRPC != nil {
		sm.doRPC(pkt, methodRPC(sm.hijackRPC), method, req, rsp, true)
		return iodefine.IOSuccess
	}
	// registered RPC lookup and call
	sm.rpcMtx.RLock()
	rpc, ok := sm.localRPCs[method]
	sm.rpcMtx.RUnlock()
	if ok {
		wrapperRPC := func(_ string, req geminio.Request, rsp geminio.Response) {
			rpc(req, rsp)
		}
		sm.doRPC(pkt, wrapperRPC, method, req, rsp, true)
		return iodefine.IOSuccess
	}

	// no rpc found, return to call error, note that this error is not set to response error
	err := fmt.Errorf("no such rpc: %s", method)
	rspPkt := sm.pf.NewResponsePacket(pkt.ID(), []byte(method), nil, nil, err)
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

// doRPC provide generic rpc call
func (sm *stream) doRPC(pkt *packet.RequestPacket, rpc methodRPC, method string, req *request, rsp *response, async bool) {
	prog := func() {
		rpc(method, req, rsp)
		rspPkt := sm.pf.NewResponsePacket(pkt.ID(), []byte(req.method), rsp.data, rsp.custom, rsp.err)
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
