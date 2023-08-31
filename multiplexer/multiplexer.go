package multiplexer

import (
	"errors"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/packet"
)

var (
	ErrOperationOnClosedMultiplexer = errors.New("operation on closed multiplexer")
	ErrConnNotFound                 = errors.New("conn not found")
	ErrDialogueNotFound             = errors.New("dialogue not found")
	ErrAcceptChNotEnabled           = errors.New("accept channel not enabled")
	ErrClosedChNotEnabled           = errors.New("closed channel not enabled")
)

// dialogue manager
type Multiplexer interface {
	OpenDialogue(meta []byte) (Dialogue, error)
	AcceptDialogue() (Dialogue, error)
	ClosedDialogue() (Dialogue, error)
	// list
	ListDialogues() []Dialogue
	GetDialogue(clientID uint64, dialogueID uint64) (Dialogue, error)
	Close()
}

// dialogue
type Reader interface {
	Read() (packet.Packet, error)
	ReadC() <-chan packet.Packet
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
	NegotiatingID() uint64
	ClientID() uint64
	DialogueID() uint64
	Meta() []byte
	Side() geminio.Side
}

type Dialogue interface {
	Reader
	Writer
	Closer

	// meta
	NegotiatingID() uint64
	ClientID() uint64
	DialogueID() uint64
	Meta() []byte
	Side() geminio.Side
}
