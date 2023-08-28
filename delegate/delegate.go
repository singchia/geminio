package delegate

import "net"

// Delegate
type Delegate interface {
	Online(clientId uint64, meta []byte, addr net.Addr) error
	Offline(clientId uint64, meta []byte, addr net.Addr) error
	Heartbeat(clientId uint64, meta []byte, addr net.Addr) error
	RemoteRegistration(method string, clientId uint64, streamId uint64)
	GetClientIdByMeta(meta []byte) (uint64, error)
}
