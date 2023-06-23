package conn

import "net"

type Delegate interface {
	ConnOnline(clientID uint64, meta []byte, addr net.Addr) error
	ConnOffline(clientID uint64, meta []byte, addr net.Addr) error
}

type ClientConnDelegate interface {
	Delegate
}

type ServerConnDelegate interface {
	Delegate
	Heartbeat(clientID uint64, meta []byte, addr net.Addr) error
	GetClientIDByMeta(meta []byte) (uint64, error)
}
