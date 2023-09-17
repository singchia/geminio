package server

import (
	"github.com/jumboframes/armorigo/log"

	"github.com/singchia/geminio/delegate"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/go-timer/v2"
)

type EndOptions struct {
	Timer         timer.Timer
	TimerOutside  bool
	PacketFactory packet.PacketFactory
	Log           log.Logger
	Delegate      delegate.Delegate
	ClientID      *uint64
}

func (eo *EndOptions) SetTimer(timer timer.Timer) {
	eo.Timer = timer
	eo.TimerOutside = true
}

func (eo *EndOptions) SetPacketFactory(packetFactory packet.PacketFactory) {
	eo.PacketFactory = packetFactory
}

func (eo *EndOptions) SetLog(log log.Logger) {
	eo.Log = log
}

func (eo *EndOptions) SetDelegate(delegate delegate.Delegate) {
	eo.Delegate = delegate
}

func (eo *EndOptions) SetClientID(clientID uint64) {
	eo.ClientID = &clientID
}

func NewEndOptions() *EndOptions {
	return &EndOptions{}
}

func MergeEndOptions(opts ...*EndOptions) *EndOptions {
	eo := &EndOptions{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if opt.Timer != nil {
			eo.Timer = opt.Timer
			eo.TimerOutside = false
		}
	}
	return eo
}