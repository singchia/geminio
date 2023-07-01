package multiplexer

import (
	"errors"

	"github.com/singchia/geminio/packet"
)

var (
	ErrOperationOnClosedMultiplexer = errors.New("operation on closed multiplexer")
	ErrDialogueNotFound             = errors.New("dialogue not found")
	ErrAcceptChNotEnabled           = errors.New("accept channel not enabled")
	ErrClosedChNotEnabled           = errors.New("closed channel not enabled")
)

// dialogue manager
type Multiplexer interface {
	OpenDialogue(meta []byte) (Dialogue, error)
	AcceptDialogue(Dialogue, error)
	ClosedDialogue() (Dialogue, error)
	// list
	ListDialogues() []Dialogue
}

// dialogue
type Reader interface {
	Read() (packet.Packet, error)
}

type Writer interface {
	Write(pkt packet.Packet) error
}

type Closer interface {
	Close()
}

type Side int

const (
	ClientSide Side = 0
	ServerSide Side = 1
)

type DialogueDescriber interface {
	ClientID() uint64
	DialogueID() uint64
	Meta() []byte
	Side() Side
}

type Dialogue interface {
	Reader
	Writer
	Closer

	// meta
	ClientID() uint64
	DialogueID() uint64
	Meta() []byte
	Side() Side
}
