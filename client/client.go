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

type Client struct {
	opts *ClientOptions
	geminio.End
}

func New(network, address string, opts ...*ClientOptions) (geminio.End, error) {
	// connection
	netcn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}
	return new(netcn, opts...)
}

func NewWithDialer(dialer Dialer, opts ...*ClientOptions) (geminio.End, error) {
	netcn, err := dialer()
	if err != nil {
		return nil, err
	}
	return new(netcn, opts...)
}

func NewWithConn(conn net.Conn, opts ...*ClientOptions) (geminio.End, error) {
	return new(conn, opts...)
}

func new(netcn net.Conn, opts ...*ClientOptions) (geminio.End, error) {
	// options
	co := MergeClientOptions(opts...)
	initOptions(co)
	client := &Client{
		opts: co,
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
		conn.OptionClientConnPacketFactory(co.PacketFactory),
		conn.OptionClientConnDelegate(co.Delegate),
		conn.OptionClientConnLogger(co.Log),
		conn.OptionClientConnTimer(co.Timer),
		conn.OptionClientConnMeta(co.Meta),
	}
	if co.ClientID != nil {
		cnOpts = append(cnOpts, conn.OptionClientConnClientID(*co.ClientID))
	}
	cn, err = conn.NewClientConn(netcn, cnOpts...)
	if err != nil {
		goto ERR
	}
	// multiplexer
	mpOpts = []multiplexer.MultiplexerOption{
		multiplexer.OptionPacketFactory(co.PacketFactory),
		multiplexer.OptionDelegate(co.Delegate),
		multiplexer.OptionLogger(co.Log),
		multiplexer.OptionTimer(co.Timer),
		multiplexer.OptionMultiplexerAcceptDialogue(),
	}
	mp, err = multiplexer.NewDialogueMgr(cn, mpOpts...)
	if err != nil {
		goto ERR
	}
	// application
	epOpts = []application.EndOption{
		application.OptionEndPacketFactory(co.PacketFactory),
		application.OptionEndDelegate(co.Delegate),
		application.OptionEndLogger(co.Log),
		application.OptionEndTimer(co.Timer),
	}
	ep, err = application.NewEnd(cn, mp, epOpts...)
	if err != nil {
		goto ERR
	}
	// client
	client.End = ep
	return client, nil
ERR:
	if !co.TimerOutside {
		co.Timer.Close()
	}
	return nil, err
}

func initOptions(co *ClientOptions) {
	if co.Timer == nil {
		co.Timer = timer.NewTimer()
		co.TimerOutside = false // needs to be collected after Client Close
	}
	if co.Log == nil {
		co.Log = log.DefaultLog
	}
	if co.PacketFactory == nil {
		co.PacketFactory = packet.NewPacketFactory(id.NewIDCounter(id.Odd))
	}
}

func (client *Client) Close() error {
	if client.opts.TimerOutside {
		client.opts.Timer.Close()
	}
	return client.End.Close()
}
