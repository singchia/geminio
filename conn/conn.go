package conn

import (
	"net"

	"github.com/singchia/gemino"
	"github.com/singchia/gemino/packet"
)

type Reader interface {
	Read() (packet.Packet, error)
}

type ChannelReader interface {
	ChannelRead() <-chan packet.Packet
}

type Writer interface {
	Write(pkt packet.Packet) error
}

type Closer interface {
	Close()
}

type ConnDescriber interface {
	ClientID() uint64
	Meta() []byte
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Side() gemino.Side
}

type Conn interface {
	Reader
	ChannelReader
	Writer
	Closer

	// meta
	ConnDescriber
}
