package client

import (
	"github.com/jumboframes/armorigo/log"

	"github.com/singchia/geminio/delegate"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/go-timer/v2"
)

type ClientOptions struct {
	Timer         timer.Timer
	TimerOutside  bool
	PacketFactory packet.PacketFactory
	Log           log.Logger
	Delegate      delegate.Delegate
	ClientID      *uint64
	Meta          []byte
}

func (co *ClientOptions) SetTimer(timer timer.Timer) {
	co.Timer = timer
	co.TimerOutside = true
}

func (co *ClientOptions) SetPacketFactory(packetFactctory packet.PacketFactory) {
	co.PacketFactory = packetFactctory
}

func (co *ClientOptions) SetLog(log log.Logger) {
	co.Log = log
}

func (co *ClientOptions) SetDelegate(delegate delegate.Delegate) {
	co.Delegate = delegate
}

func (co *ClientOptions) SetClientID(clientID uint64) {
	co.ClientID = &clientID
}

func (co *ClientOptions) SetMeta(meta []byte) {
	co.Meta = meta
}

func NewClientOptions() *ClientOptions {
	return &ClientOptions{}
}

func MergeClientOptions(opts ...*ClientOptions) *ClientOptions {
	co := &ClientOptions{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if opt.Timer != nil {
			co.Timer = opt.Timer
		}
	}
	return co
}
