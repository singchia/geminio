package client

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

type Dialer func() (net.Conn, error)

type ClientEnd struct {
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
	initOptions(eo)
	client := &ClientEnd{
		opts: eo,
	}

	var (
		err    error
		cn     conn.Conn
		cnOpts []conn.ClientConnOption
		mp     multiplexer.Multiplexer
		mpOpts []multiplexer.MultiplexerOption
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
		application.OptionEndPacketFactory(eo.PacketFactory),
		application.OptionEndDelegate(eo.Delegate),
		application.OptionEndLogger(eo.Log),
		application.OptionEndTimer(eo.Timer),
	}
	ep, err = application.NewEnd(cn, mp, epOpts...)
	if err != nil {
		goto ERR
	}
	// client
	client.End = ep
	return client, nil
ERR:
	if !eo.TimerOutside {
		eo.Timer.Close()
	}
	return nil, err
}

func initOptions(eo *EndOptions) {
	if eo.Timer == nil {
		eo.Timer = timer.NewTimer()
		eo.TimerOutside = false // needs to be collected after Client Close
	}
	if eo.Log == nil {
		eo.Log = log.DefaultLog
	}
	if eo.PacketFactory == nil {
		eo.PacketFactory = packet.NewPacketFactory(id.NewIDCounter(id.Odd))
	}
}

func (client *ClientEnd) Close() error {
	err := client.End.Close()
	if !client.opts.TimerOutside {
		// TODO in case of timer closed before connection closed
		client.opts.Timer.Close()
	}
	return err
}
