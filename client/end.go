package client

import (
	"net"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/application"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/multiplexer"
	"github.com/singchia/go-timer/v2"
)

type Dialer func() (net.Conn, error)

type clientEnd struct {
	// we need the opts to hold resources to close
	opts *EndOptions
	geminio.End
}

func NewEnd(network, address string, opts ...*EndOptions) (geminio.End, error) {
	// connection
	netcn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	return new(netcn, opts...)
}

func NewEndWithDialer(dialer Dialer, opts ...*EndOptions) (geminio.End, error) {
	netcn, err := dialer()
	if err != nil {
		return nil, err
	}
	return new(netcn, opts...)
}

func NewEndWithConn(conn net.Conn, opts ...*EndOptions) (geminio.End, error) {
	return new(conn, opts...)
}

func new(netcn net.Conn, opts ...*EndOptions) (geminio.End, error) {
	// options
	eo := MergeEndOptions(opts...)
	initEndOptions(eo)
	ce := &clientEnd{
		opts: eo,
	}
	if eo.Timer == nil {
		eo.Timer = timer.NewTimer()
		eo.TimerOwner = ce
	}

	var (
		err error
		// connection
		cn     conn.Conn
		cnOpts []conn.ClientConnOption
		// multiplexer
		mp     multiplexer.Multiplexer
		mpOpts []multiplexer.MultiplexerOption
		// application
		ep     geminio.End
		epOpts []application.EndOption
	)
	// we share packet factory, log, timer and delegate for follow 3 layers.

	// connection layer
	cnOpts = []conn.ClientConnOption{
		conn.OptionClientConnPacketFactory(eo.PacketFactory),
		conn.OptionClientConnDelegate(eo.Delegate),
		conn.OptionClientConnLogger(eo.Log),
		conn.OptionClientConnTimer(eo.Timer),
		conn.OptionClientConnMeta(eo.Meta),
	}
	if eo.ClientID != nil {
		cnOpts = append(cnOpts, conn.OptionClientConnClientID(*eo.ClientID))
	}
	cn, err = conn.NewClientConn(netcn, cnOpts...)
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
	if eo.RemoteMethodCheck {
		epOpts = append(epOpts, application.OptionWithRemoteRPCCheck())
	}
	ep, err = application.NewEnd(cn, mp, epOpts...)
	if err != nil {
		goto ERR
	}
	// client
	ce.End = ep
	return ce, nil
ERR:
	if eo.TimerOwner == ce {
		eo.Timer.Close()
	}
	return nil, err
}

func (ce *clientEnd) Close() error {
	err := ce.End.Close()
	if ce.opts.TimerOwner == ce {
		// TODO in case of timer closed before connection closed
		ce.opts.Timer.Close()
	}
	return err
}
