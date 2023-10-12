package delegate

import (
	"net"

	"github.com/singchia/geminio"
)

// connection layer delegation
type ConnDescriber interface {
	ClientID() uint64
	Meta() []byte
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Side() geminio.Side
}

type ClientConnDelegate interface {
	ConnOnline(ConnDescriber) error
	ConnOffline(ConnDescriber) error
}

type ServerConnDelegate interface {
	// notifications
	ClientConnDelegate
	Heartbeat(ConnDescriber) error
	// requirements
	GetClientID(meta []byte) (uint64, error)
}

// dialogue layer delegation
type DialogueDescriber interface {
	NegotiatingID() uint64
	ClientID() uint64
	DialogueID() uint64
	Meta() []byte
	Side() geminio.Side
}

type ClientDialogueDelegate interface {
	DialogueOnline(DialogueDescriber) error
	DialogueOffline(DialogueDescriber) error
}

type ServerDialogueDelegate interface {
	ClientDialogueDelegate
}

type ClientDescriber interface {
	ClientID() uint64
}

// application layer delegation
type ApplicationDelegate interface {
	RemoteRegistration(method string, clientID uint64, streamID uint64)
}

// Delegate
type Delegate interface {
	// connection layer
	ConnOnline(ConnDescriber) error
	ConnOffline(ConnDescriber) error
	Heartbeat(ConnDescriber) error
	GetClientID(meta []byte) (uint64, error)
	// dialogue layer
	DialogueOnline(DialogueDescriber) error
	DialogueOffline(DialogueDescriber) error
	// application layer
	EndOnline(ClientDescriber) error
	EndOffline(ClientDescriber) error
	RemoteRegistration(method string, clientID uint64, streamID uint64)
}
