package conn

type Delegate interface {
	ConnOnline(Conn) error
	ConnOffline(Conn) error
}

type ClientConnDelegate interface {
	Delegate
}

type ServerConnDelegate interface {
	Delegate
	Heartbeat(Conn) error
	GetClientIDByMeta(meta []byte) (uint64, error)
}
