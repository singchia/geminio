package server

import (
	"github.com/jumboframes/armorigo/log"

	"github.com/singchia/geminio/delegate"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/go-timer/v2"
)

type ServerOptions struct {
	Timer         timer.Timer
	TimerOutside  bool
	PacketFactory packet.PacketFactory
	Log           log.Logger
	Delegate      delegate.Delegate
	ClientID      *uint64
}

func (so *ServerOptions) SetTimer(timer timer.Timer) {
	so.Timer = timer
	so.TimerOutside = true
}

func (so *ServerOptions) SetPacketFactory(packetFactory packet.PacketFactory) {
	so.PacketFactory = packetFactory
}
