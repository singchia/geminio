package client

import (
	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
)

type RetryEndOptions struct {
	*EndOptions
}

func NewRetryEndOptions() *RetryEndOptions {
	return &RetryEndOptions{
		EndOptions: &EndOptions{},
	}
}

func MergeRetryEndOptions(opts ...*RetryEndOptions) *RetryEndOptions {
	eo := &RetryEndOptions{
		EndOptions: &EndOptions{
			ReadBufferSize:  -1,
			WriteBufferSize: -1,
		},
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
		if opt.ReadBufferSize != -1 {
			eo.ReadBufferSize = opt.ReadBufferSize
		}
		if opt.WriteBufferSize != -1 {
			eo.WriteBufferSize = opt.WriteBufferSize
		}
	}
	return eo
}

func initRetryEndOptions(eo *RetryEndOptions) {
	if eo.Log == nil {
		eo.Log = log.DefaultLog
	}
	if eo.PacketFactory == nil {
		eo.PacketFactory = packet.NewPacketFactory(id.NewIDCounter(id.Odd))
	}
}
