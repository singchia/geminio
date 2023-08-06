package application

import (
	"sync"

	"github.com/jumboframes/armorigo/log"

	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/multiplexer"
	"github.com/singchia/geminio/packet"
	gsync "github.com/singchia/geminio/pkg/sync"
	"github.com/singchia/go-timer/v2"
)

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

	// misc
	shub *synchub.SyncHub

	// under layer dialogue and connection
	dg multiplexer.Dialogue
	cn conn.Conn

	// registered rpcs
	localRPCs map[string]geminio.RPC // key: method value: RPC

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

func (stream *stream) handlePkt() {
	readInCh := stream.dg.ReadC()
	writeInCh := stream.writeInCh

	for {
		select {
		case pkt, ok := <-readInCh:
			if !ok {
				goto FINI
			}
			stream.log.Tracef("stream read in packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
				stream.cn.ClientID(), pkt.ID(), pkt.Type().String())

		case pkt, ok := <-writeInCh:
			if !ok {
				goto FINI
			}
			stream.log.Tracef("stream write in packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
				stream.cn.ClientID(), pkt.ID(), pkt.Type().String())
		}
	}
FINI:
}
