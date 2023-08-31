package conn

import "github.com/singchia/geminio/delegate"

type Delegate interface {
	ConnOnline(delegate.ConnDescriber) error
	ConnOffline(delegate.ConnDescriber) error
}

type ClientConnDelegate interface {
	Delegate
}

type ServerConnDelegate interface {
	Delegate
	Heartbeat(delegate.ConnDescriber) error
	GetClientIDByMeta(meta []byte) (uint64, error)
}
