package server

import (
	"github.com/jumboframes/armorigo/log"

	"github.com/singchia/geminio"
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
	RemoteMethods []string
	LocalMethods  []*geminio.MethodRPC
	// If set AcceptStreamFunc, the AcceptStream should never be called
	AcceptStreamFunc func(geminio.Stream)
	ClosedStreamFunc func(geminio.Stream)
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

func (eo *EndOptions) SetWaitRemoteRPCs(methods ...string) {
	eo.RemoteMethods = methods
}

func (eo *EndOptions) SetRegisterLocalRPCs(methodRPCs ...*geminio.MethodRPC) {
	eo.LocalMethods = methodRPCs
}

func (eo *EndOptions) SetAcceptStreamFunc(fn func(geminio.Stream)) {
	eo.AcceptStreamFunc = fn
}

func (eo *EndOptions) SetClosedStreamFunc(fn func(geminio.Stream)) {
	eo.ClosedStreamFunc = fn
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
		if opt.PacketFactory != nil {
			eo.PacketFactory = opt.PacketFactory
		}
		if opt.Log != nil {
			eo.Log = opt.Log
		}
		if opt.Delegate != nil {
			eo.Delegate = opt.Delegate
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
		if opt.AcceptStreamFunc != nil {
			eo.AcceptStreamFunc = opt.AcceptStreamFunc
		}
		if opt.ClosedStreamFunc != nil {
			eo.ClosedStreamFunc = opt.ClosedStreamFunc
		}
	}
	return eo
}
