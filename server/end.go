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
	initEndOptions(eo)
	se := &ServerEnd{
		opts: eo,
	}
	if eo.Timer == nil {
		eo.Timer = timer.NewTimer()
		eo.TimerOwner = se
	}
	var (
		err error
		// connection
		cn     conn.Conn
		cnOpts []conn.ServerConnOption
		// multiplexer
		mp       multiplexer.Multiplexer
		mpOpts   []multiplexer.MultiplexerOption
		acceptfn func(multiplexer.Dialogue)
		closedfn func(multiplexer.Dialogue)
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
	// multiplexer and application
	if eo.AcceptStreamFunc != nil {
		acceptfn = func(dg multiplexer.Dialogue) {
			ep.(*application.End).AcceptDialogue(dg)
		}
	}
	if eo.ClosedStreamFunc != nil {
		closedfn = func(dg multiplexer.Dialogue) {
			ep.(*application.End).ClosedDialogue(dg)
		}
	}
	// multiplexer
	mpOpts = []multiplexer.MultiplexerOption{
		multiplexer.OptionPacketFactory(eo.PacketFactory),
		multiplexer.OptionDelegate(eo.Delegate),
		multiplexer.OptionLogger(eo.Log),
		multiplexer.OptionTimer(eo.Timer),
	}
	if eo.AcceptStreamFunc != nil {
		mpOpts = append(mpOpts, multiplexer.OptionMultiplexerAcceptFunc(acceptfn))
	} else {
		mpOpts = append(mpOpts, multiplexer.OptionMultiplexerAcceptDialogue())
	}
	if eo.ClosedStreamFunc != nil {
		mpOpts = append(mpOpts, multiplexer.OptionMultiplexerClosedFunc(closedfn))
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
		application.OptionWaitRemoteRPCs(eo.RemoteMethods...),
		application.OptionRegisterLocalRPCs(eo.LocalMethods...),
	}
	if eo.AcceptStreamFunc != nil {
		epOpts = append(epOpts, application.OptionAcceptStreamFunc(eo.AcceptStreamFunc))
	}
	if eo.ClosedStreamFunc != nil {
		epOpts = append(epOpts, application.OptionClosedStreamFunc(eo.ClosedStreamFunc))
	}
	ep, err = application.NewEnd(cn, mp, epOpts...)
	if err != nil {
		goto ERR
	}
	se.End = ep
	return se, nil
ERR:
	if eo.TimerOwner == se {
		eo.Timer.Close()
	}
	return nil, err
}

func initEndOptions(eo *EndOptions) {
	if eo.Log == nil {
		eo.Log = log.DefaultLog
	}
	if eo.PacketFactory == nil {
		eo.PacketFactory = packet.NewPacketFactory(id.NewIDCounter(id.Even))
	}
}

func (se *ServerEnd) Close() error {
	err := se.End.Close()
	if se.opts.TimerOwner == se {
		// TODO in case of timer closed before connection closed
		se.opts.Timer.Close()
	}
	return err
}
