package multiplexer

import "github.com/singchia/geminio/delegate"

type Delegate interface {
	DialogueOnline(delegate.DialogueDescriber) error
	DialogueOffline(delegate.DialogueDescriber) error
}
