package conn

import (
	"context"
	"net"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/packet"
)

type Reader interface {
	Read() (packet.Packet, error)
}

type ChannelReader interface {
	ChannelRead() <-chan packet.Packet
}

type Writer interface {
	WriteContext(context.Context, packet.Packet) error
	Write(pkt packet.Packet) error
}

type Closer interface {
	ForceClose()
	Close()
}

type ConnDescriber interface {
	ClientID() uint64
	Meta() []byte
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Side() geminio.Side
}

type Conn interface {
	Reader
	ChannelReader
	Writer
	Closer

	// meta
	ConnDescriber
}
