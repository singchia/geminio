package conn

import "net"

type Delegate interface {
	Online(clientId uint64, meta []byte, addr net.Addr) error
	Offline(clientId uint64, meta []byte, addr net.Addr) error
	RemoteRegistration(method string, clientId uint64, sessionId uint64)
}

type SendConnDelegate interface {
	Delegate
}

type RecvConnDelegate interface {
	Delegate
	Heartbeat(clientId uint64, meta []byte, addr net.Addr) error
	GetClientIDByMeta(meta []byte) (uint64, error)
}
