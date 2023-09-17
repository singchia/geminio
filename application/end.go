package application

import (
	"sync"

	"github.com/jumboframes/armorigo/log"
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
	tmr        timer.Timer
	tmrOutside bool
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
		end.tmrOutside = true
	}
}

// OptionDelegate sets delegate for End and Streams from the End
func OptionDelegate(dlgt delegate.Delegate) EndOption {
	return func(end *End) {
		end.dlgt = dlgt
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

	dlgt delegate.Delegate
}

func NewEnd(cn conn.Conn, multiplexer multiplexer.Multiplexer, options ...EndOption) (
	*End, error) {

	end := &End{
		opts:        &opts{},
		cn:          cn,
		multiplexer: multiplexer,
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
		end.tmrOutside = false
	}
	// if log was't set, then use the global default log
	if end.log == nil {
		end.log = log.DefaultLog
	}

	// set default stream whose streamID is 1 for the End
	// newStream start to roll the stream
	dg, err := end.multiplexer.GetDialogue(cn.ClientID(), 1)
	if err != nil {
		return nil, err
	}
	end.stream = newStream(cn, dg, end.opts)
	end.streams.Store(dg.DialogueID(), end.stream)
	return end, nil
}

func (end *End) OpenStream(opts ...*options.OpenStreamOptions) (
	geminio.Stream, error) {

	oo := options.MergeOpenStreamOptions(opts...)
	dg, err := end.multiplexer.OpenDialogue(oo.Meta)
	if err != nil {
		return nil, err
	}
	sm := newStream(end.cn, dg, end.opts)
	end.streams.Store(sm.StreamID(), sm)
	return sm, nil
}

func (end *End) AcceptStream() (geminio.Stream, error) {
	dg, err := end.multiplexer.AcceptDialogue()
	if err != nil {
		return nil, err
	}
	sm := newStream(end.cn, dg, end.opts)
	end.streams.Store(sm.StreamID(), sm)
	return sm, nil
}

func (end *End) ListStreams() []geminio.Stream {
	streams := []geminio.Stream{}
	end.streams.Range(func(_, value interface{}) bool {
		streams = append(streams, value.(*stream))
		return true
	})
	return streams
}

func (end *End) Close() error {
	end.multiplexer.Close()
	end.cn.Close()
	if !end.tmrOutside {
		end.tmr.Close()
	}
	return nil
}
