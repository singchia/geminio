package conn

import "net"

type Delegate interface {
	Online(clientID uint64, meta []byte, addr net.Addr) error
	Offline(clientID uint64, meta []byte, addr net.Addr) error
	RemoteRegistration(method string, clientID uint64, sessionID uint64)
}

type SendConnDelegate interface {
	Delegate
}

type RecvConnDelegate interface {
	Delegate
	Heartbeat(clientID uint64, meta []byte, addr net.Addr) error
	GetClientIDByMeta(meta []byte) (uint64, error)
}
