package client

import (
	"github.com/jumboframes/armorigo/log"

	"github.com/singchia/geminio/delegate"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/go-timer/v2"
)

type EndOptions struct {
	Timer         timer.Timer
	TimerOwner    interface{}
	PacketFactory packet.PacketFactory
	Log           log.Logger
	Delegate      delegate.Delegate
	delegate      delegate.Delegate
	ClientID      *uint64
	Meta          []byte
	Methods       []string
}

func (eo *EndOptions) SetTimer(timer timer.Timer) {
	eo.Timer = timer
	eo.TimerOwner = nil
}

func (eo *EndOptions) setTimer(timer timer.Timer, owner interface{}) {
	eo.Timer = timer
	eo.TimerOwner = owner
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

func (eo *EndOptions) SetMeta(meta []byte) {
	eo.Meta = meta
}

func (eo *EndOptions) SetWaitRemoteRPCs(methods ...string) {
	eo.Methods = methods
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
			eo.TimerOwner = opt.TimerOwner
		}
		if opt.PacketFactory != nil {
			eo.PacketFactory = opt.PacketFactory
		}
		if opt.Log != nil {
			eo.Log = opt.Log
		}
		if opt.Delegate != nil {
			eo.Delegate = opt.Delegate
		}
		if opt.Meta != nil {
			eo.Meta = opt.Meta
		}
		if opt.ClientID != nil {
			eo.ClientID = opt.ClientID
		}
		if opt.Methods != nil {
			eo.Methods = opt.Methods
		}
	}
	return eo
}

func initEndOptions(eo *EndOptions) {
	if eo.Log == nil {
		eo.Log = log.DefaultLog
	}
	if eo.PacketFactory == nil {
		eo.PacketFactory = packet.NewPacketFactory(id.NewIDCounter(id.Odd))
	}
}
