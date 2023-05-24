package delegate

import "net"

// event delegates
type Delegate interface {
	Online(clientId uint64, meta []byte, addr net.Addr) error
	Offline(clientId uint64, meta []byte, addr net.Addr) error
	Heartbeat(clientId uint64, meta []byte, addr net.Addr) error
	RemoteRegistration(method string, clientId uint64, sessionId uint64)
	GetClientIDByMeta(meta []byte) (uint64, error)
}
