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

// Client Delegate
type ClientDelegate interface {
	ClientConnDelegate
	ClientDialogueDelegate
	ApplicationDelegate
	EndReOnline(ClientDescriber)
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
	EndReOnline(ClientDescriber)
	RemoteRegistration(method string, clientID uint64, streamID uint64)
}

type UnimplementedDelegate struct{}

func (dlgt *UnimplementedDelegate) ConnOnline(ConnDescriber) error { return nil }

func (dlgt *UnimplementedDelegate) ConnOffline(ConnDescriber) error { return nil }

func (dlgt *UnimplementedDelegate) Heartbeat(ConnDescriber) error { return nil }

func (dlgt *UnimplementedDelegate) GetClientID(meta []byte) (uint64, error) { return 0, nil }

func (dlgt *UnimplementedDelegate) DialogueOnline(DialogueDescriber) error { return nil }

func (dlgt *UnimplementedDelegate) DialogueOffline(DialogueDescriber) error { return nil }

func (dlgt *UnimplementedDelegate) EndReOnline(ClientDescriber) { return }

func (dlgt *UnimplementedDelegate) RemoteRegistration(method string, clientID uint64, streamID uint64) {
}
