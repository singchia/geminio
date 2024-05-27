package client

import (
	"github.com/jumboframes/armorigo/log"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/delegate"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/go-timer/v2"
)

type EndOptions struct {
	Timer                           timer.Timer
	TimerOwner                      interface{}
	PacketFactory                   packet.PacketFactory
	Log                             log.Logger
	Delegate                        delegate.ClientDelegate
	delegate                        delegate.ClientDelegate
	ClientID                        *uint64
	Meta                            []byte
	RemoteMethods                   []string
	RemoteMethodCheck               bool
	LocalMethods                    []*geminio.MethodRPC
	ReadBufferSize, WriteBufferSize int
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

func (eo *EndOptions) SetDelegate(delegate delegate.ClientDelegate) {
	eo.Delegate = delegate
}

func (eo *EndOptions) SetClientID(clientID uint64) {
	eo.ClientID = &clientID
}

func (eo *EndOptions) SetMeta(meta []byte) {
	eo.Meta = meta
}

func (eo *EndOptions) SetWaitRemoteRPCs(methods ...string) {
	eo.RemoteMethods = methods
}

func (eo *EndOptions) SetBufferSize(read, write int) {
	eo.ReadBufferSize = read
	eo.WriteBufferSize = write
}

func (eo *EndOptions) SetRemoteRPCCheck() {
	eo.RemoteMethodCheck = true
}

func (eo *EndOptions) SetRegisterLocalRPCs(methodRPCs ...*geminio.MethodRPC) {
	eo.LocalMethods = methodRPCs
}

func NewEndOptions() *EndOptions {
	return &EndOptions{
		ReadBufferSize:  -1,
		WriteBufferSize: -1,
	}
}

func MergeEndOptions(opts ...*EndOptions) *EndOptions {
	eo := &EndOptions{
		ReadBufferSize:  -1,
		WriteBufferSize: -1,
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		eo.RemoteMethodCheck = opt.RemoteMethodCheck
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
		if opt.RemoteMethods != nil {
			eo.RemoteMethods = opt.RemoteMethods
		}
		if opt.LocalMethods != nil {
			eo.LocalMethods = opt.LocalMethods
		}
		if opt.ReadBufferSize > 0 {
			eo.ReadBufferSize = opt.ReadBufferSize
		}
		if opt.WriteBufferSize > 0 {
			eo.WriteBufferSize = opt.WriteBufferSize
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
