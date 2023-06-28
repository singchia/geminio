package multiplexer

type Delegate interface {
	DialogueOnline(DialogueDescriber) error
	DialogueOffline(DialogueDescriber) error
}
