package id

type IDFactory interface {
	ReserveID(id uint64)
	GetID() uint64
	GetIDByMeta(meta []byte) (uint64, error)
	DelID(uint64)
	Close()
}
