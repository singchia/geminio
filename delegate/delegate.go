package delegate

import (
	"net"

	"github.com/singchia/geminio"
)

type ConnDescriber interface {
	ClientID() uint64
	Meta() []byte
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Side() geminio.Side
}

type DialogueDescriber interface {
	NegotiatingID() uint64
	ClientID() uint64
	DialogueID() uint64
	Meta() []byte
	Side() geminio.Side
}

// Delegate
type Delegate interface {
	ConnOnline(ConnDescriber) error
	ConnOffline(ConnDescriber) error
	Heartbeat(ConnDescriber) error
	DialogueOnline(DialogueDescriber) error
	DialogueOffline(DialogueDescriber) error
	RemoteRegistration(method string, clientID uint64, streamID uint64)
	GetClientIDByMeta(meta []byte) (uint64, error)
}
