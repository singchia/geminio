package server

import (
	"net"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/application"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/multiplexer"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/go-timer/v2"
)

type ServerEnd struct {
	// we need the opts to hold resources to close
	opts *EndOptions
	geminio.End
}

func NewEndWithConn(conn net.Conn, opts ...*EndOptions) (geminio.End, error) {
	return new(conn, opts...)
}

func new(netcn net.Conn, opts ...*EndOptions) (geminio.End, error) {
	// options
	eo := MergeEndOptions(opts...)
	initOptions(eo)
	se := &ServerEnd{
		opts: eo,
	}
	var (
		err error
		// connection
		cn     conn.Conn
		cnOpts []conn.ServerConnOption
		// multiplexer
		mp     multiplexer.Multiplexer
		mpOpts []multiplexer.MultiplexerOption
		// application
		ep     geminio.End
		epOpts []application.EndOption
	)
	// we share packet factory, log, timer and delegate for follow 3 layers.

	// connection layer
	cnOpts = []conn.ServerConnOption{
		conn.OptionServerConnPacketFactory(eo.PacketFactory),
		conn.OptionServerConnDelegate(eo.Delegate),
		conn.OptionServerConnLogger(eo.Log),
		conn.OptionServerConnTimer(eo.Timer),
	}
	if eo.ClientID != nil {
		cnOpts = append(cnOpts, conn.OptionServerConnClientID(*eo.ClientID))
	}
	cn, err = conn.NewServerConn(netcn, cnOpts...)
	if err != nil {
		goto ERR
	}
	// multiplexer
	mpOpts = []multiplexer.MultiplexerOption{
		multiplexer.OptionPacketFactory(eo.PacketFactory),
		multiplexer.OptionDelegate(eo.Delegate),
		multiplexer.OptionLogger(eo.Log),
		multiplexer.OptionTimer(eo.Timer),
		multiplexer.OptionMultiplexerAcceptDialogue(),
		multiplexer.OptionMultiplexerClosedDialogue(),
	}
	mp, err = multiplexer.NewDialogueMgr(cn, mpOpts...)
	if err != nil {
		goto ERR
	}
	// application
	epOpts = []application.EndOption{
		application.OptionPacketFactory(eo.PacketFactory),
		application.OptionDelegate(eo.Delegate),
		application.OptionLogger(eo.Log),
		application.OptionTimer(eo.Timer),
	}
	ep, err = application.NewEnd(cn, mp, epOpts...)
	if err != nil {
		goto ERR
	}
	se.End = ep
	return se, nil
ERR:
	if !eo.TimerOutside {
		eo.Timer.Close()
	}
	return nil, err
}

func initOptions(eo *EndOptions) {
	if eo.Timer == nil {
		eo.Timer = timer.NewTimer()
		eo.TimerOutside = false // needs to be collected after ServerEnd Close
	}
	if eo.Log == nil {
		eo.Log = log.DefaultLog
	}
	if eo.PacketFactory == nil {
		eo.PacketFactory = packet.NewPacketFactory(id.NewIDCounter(id.Even))
	}
}

func (se *ServerEnd) Close() error {
	err := se.End.Close()
	if !se.opts.TimerOutside {
		// TODO in case of timer closed before connection closed
		se.opts.Timer.Close()
	}
	return err
}
