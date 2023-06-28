package conn

import (
	"net"

	"github.com/singchia/geminio/packet"
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

type Side int

const (
	ClientSide Side = 0
	ServerSide Side = 1
)

type ConnDescriber interface {
	ClientID() uint64
	Meta() []byte
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Side() Side
}

type Conn interface {
	Reader
	ChannelReader
	Writer
	Closer

	// meta
	ConnDescriber
}
