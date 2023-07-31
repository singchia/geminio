package application

import (
	"log"
	"sync"

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
}
