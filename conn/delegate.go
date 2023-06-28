package conn

type Delegate interface {
	ConnOnline(ConnDescriber) error
	ConnOffline(ConnDescriber) error
}

type ClientConnDelegate interface {
	Delegate
}

type ServerConnDelegate interface {
	Delegate
	Heartbeat(ConnDescriber) error
	GetClientIDByMeta(meta []byte) (uint64, error)
}
