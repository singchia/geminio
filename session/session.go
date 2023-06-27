package session

import (
	"errors"

	"github.com/singchia/geminio/packet"
)

var (
	ErrOperationOnClosedSessionMgr = errors.New("operation on closed session manager")
	ErrSessionNotFound             = errors.New("session not found")
)

// session manager
type Sessionor interface {
	OpenSession(meta []byte) (Session, error)
	AcceptSession(Session, error)
}

// session
type Reader interface {
	Read() (packet.Packet, error)
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

type SessionDescriber interface {
	SessionID() uint64
	Meta() []byte
	Side() Side
}

type Session interface {
	Reader
	Writer
	Closer

	// meta
	SessionID() uint64
	Meta() []byte
	Side() Side
}
