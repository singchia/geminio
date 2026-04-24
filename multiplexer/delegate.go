package multiplexer

import "github.com/singchia/gemino/delegate"

type Delegate interface {
	DialogueOnline(delegate.DialogueDescriber) error
	DialogueOffline(delegate.DialogueDescriber) error
}
