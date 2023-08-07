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
		return sm.handleMessagePacket(realPkt)
	case *packet.MessageAckPacket:
	case *packet.RequestPacket:
	case *packet.RequestCancelPacket:
		// TODO
	case *packet.ResponsePacket:
	case *packet.RegisterPacket:
	case *packet.RegisterAckPacket:
	case *packet.StreamPacket:
	}
	// unknown packet
	return iodefine.IOErr
}

// input packet
func (sm *stream) handleMessagePacket(pkt *packet.MessagePacket) iodefine.IORet {
	sm.log.Tracef("read message packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
	// TODO add select, we don't want block here.
	sm.messageOutCh <- pkt
	return iodefine.IOSuccess
}

func (sm *stream) handleMessageAckPacket(pkt *packet.MessageAckPacket) iodefine.IORet {
	sm.log.Tracef("read message ack packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
	if pkt.Data.Error != "" {
		err := errors.New(pkt.Data.Error)
		errored := sm.shub.Error(pkt.ID(), err)
		sm.log.Debugf("message ack packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s, errord: %b",
			sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String(), errored)
		return iodefine.IOSuccess
	}
	acked := sm.shub.Ack(pkt.ID(), nil)
	sm.log.Tracef("message ack packet acked: %b, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
		acked, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
	return iodefine.IOSuccess
}

func (sm *stream) handleRequestPacket(pkt *packet.RequestPacket) iodefine.IORet {
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

func (sm *stream) handleRegisterPacket(pkt *packet.RegisterPacket) iodefine.IORet {
	method := pkt.Method()
	sm.log.Tracef("read register packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s, method: %s",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String(), method)

	sm.pf.NewRegisterPacket([]byte(method))

	// to notify the method is registing, in case of we're waiting for the method ready
	sm.shub.Done(method)
	sm.rpcMtx.Lock()
	sm.remoteRPCs[method] = struct{}{}
	sm.rpcMtx.Unlock()
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
