package delegate

import (
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/multiplexer"
)

// Delegate
type Delegate interface {
	ConnOnline(conn.ConnDescriber) error
	ConnOffline(conn.ConnDescriber) error
	DialogueOnline(multiplexer.DialogueDescriber) error
	DialogueOffline(multiplexer.DialogueDescriber) error
	RemoteRegistration(method string, clientId uint64, streamId uint64)
	GetClientIdByMeta(meta []byte) (uint64, error)
}
