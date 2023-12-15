package application

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/delegate"
	"github.com/singchia/geminio/multiplexer"
	"github.com/singchia/geminio/options"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/go-timer/v2"
)

type opts struct {
	// packet factory
	pf packet.PacketFactory
	// logger
	log log.Logger
	// timer
	tmr      timer.Timer
	tmrOwner interface{}
	// delegate
	dlgt delegate.Delegate
	// methods
	remoteMethods []string
	localMethods  []*geminio.MethodRPC
}

type EndOption func(*End)

// OptionPacketFactory sets the packet factory for End and Streams from the End
func OptionPacketFactory(pf packet.PacketFactory) EndOption {
	return func(end *End) {
		end.pf = pf
	}
}

// OptionLogger sets logger for End and Streams from the End
func OptionLogger(log log.Logger) EndOption {
	return func(end *End) {
		end.log = log
	}
}

// OptionTimer sets timer for End and Streams from the End
func OptionTimer(tmr timer.Timer) EndOption {
	return func(end *End) {
		end.tmr = tmr
		end.tmrOwner = nil
	}
}

// OptionDelegate sets delegate for End and Streams from the End
func OptionDelegate(dlgt delegate.Delegate) EndOption {
	return func(end *End) {
		end.dlgt = dlgt
	}
}

func OptionWaitRemoteRPCs(methods ...string) EndOption {
	return func(end *End) {
		end.remoteMethods = methods
	}
}

func OptionRegisterLocalRPCs(methodRPCs ...*geminio.MethodRPC) EndOption {
	return func(end *End) {
		end.localMethods = methodRPCs
	}
}

type End struct {
	// options for packet factory, log and timer
	*opts

	cn          conn.Conn
	multiplexer multiplexer.Multiplexer
	streams     sync.Map
	// End holds the default stream
	*stream
	onceClose *sync.Once
}

func NewEnd(cn conn.Conn, multiplexer multiplexer.Multiplexer, options ...EndOption) (
	*End, error) {

	end := &End{
		opts:        &opts{},
		cn:          cn,
		multiplexer: multiplexer,
		onceClose:   new(sync.Once),
	}
	for _, opt := range options {
		opt(end)
	}
	// if packet factory was't set, then new a packet factory
	if end.pf == nil {
		end.pf = packet.NewPacketFactory(id.NewIDCounter(id.Odd))
	}
	// if timer was't set, then new a timer
	if end.tmr == nil {
		end.tmr = timer.NewTimer()
		end.tmrOwner = end
	}
	// if log was't set, then use the global default log
	if end.log == nil {
		end.log = log.DefaultLog
	}

	// set default stream whose streamID is 1 for the End
	// newStream start to roll the stream
	dg, err := end.multiplexer.GetDialogue(cn.ClientID(), 1)
	if err != nil {
		goto ERR
	}
	end.stream = newStream(end, cn, dg, end.opts)
	end.streams.Store(dg.DialogueID(), end.stream)

	// RPCs
	// pre register local RPCs
	if end.opts.localMethods != nil && len(end.opts.localMethods) != 0 {
		for _, elem := range end.opts.localMethods {
			err = end.Register(context.TODO(), elem.Method, elem.RPC)
			if err != nil {
				goto ERR
			}
		}
	}
	// wait for all remote RPCs registration
	if end.opts.remoteMethods != nil && len(end.opts.remoteMethods) != 0 {
		// check whether remote RPCs have been registered
		tocheckMethods := []string{}
		for _, method := range end.opts.remoteMethods {
			if !end.stream.hasRemoteRPC(method) {
				tocheckMethods = append(tocheckMethods, method)
			}
		}
		if len(tocheckMethods) != 0 {
			ifs := strings2interfaces(tocheckMethods...)
			syncID := fmt.Sprintf(registrationFormat, cn.ClientID(), dg.DialogueID())
			sync := end.stream.shub.Add(syncID, synchub.WithSub(ifs...))
			event := <-sync.C()
			if event.Error != nil {
				err = event.Error
				goto ERR
			}
		}
	}
	return end, nil
ERR:
	if end.tmrOwner == end {
		end.tmr.Close()
	}
	return nil, err
}

func (end *End) OpenStream(opts ...*options.OpenStreamOptions) (
	geminio.Stream, error) {

	oo := options.MergeOpenStreamOptions(opts...)
	dg, err := end.multiplexer.OpenDialogue(oo.Meta)
	if err != nil {
		return nil, err
	}
	sm := newStream(end, end.cn, dg, end.opts)
	end.streams.Store(sm.StreamID(), sm)
	return sm, nil
}

func (end *End) delStream(streamID uint64) {
	end.streams.Delete(streamID)
}

func (end *End) AcceptStream() (geminio.Stream, error) {
	dg, err := end.multiplexer.AcceptDialogue()
	if err != nil {
		return nil, err
	}
	sm := newStream(end, end.cn, dg, end.opts)
	end.streams.Store(sm.StreamID(), sm)
	return sm, nil
}

func (end *End) Accept() (net.Conn, error) {
	return end.AcceptStream()
}

func (end *End) ListStreams() []geminio.Stream {
	streams := []geminio.Stream{}
	end.streams.Range(func(_, value interface{}) bool {
		streams = append(streams, value.(*stream))
		return true
	})
	return streams
}

func (end *End) Addr() net.Addr {
	return end.LocalAddr()
}

func (end *End) Close() error {
	end.onceClose.Do(func() {
		end.multiplexer.Close()
		end.cn.Close()
		if end.tmrOwner == end {
			end.tmr.Close()
		}
	})
	return nil
}

func (end *End) fini() {
	end.log.Debugf("end finishing, clientID: %d", end.cn.ClientID())
	if end.tmrOwner == end {
		end.tmr.Close()
	}
	end.tmr = nil
	end.log.Debugf("end finished, clientID: %d", end.cn.ClientID())
}
