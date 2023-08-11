package geminio

import (
	"time"

	"github.com/singchia/geminio/packet"
)

type Request interface {
	// those meta info shouldn't be changed
	ID() uint64
	StreamID() uint64
	ClientID() uint64
	Method() string
	Timeout() time.Duration

	// application data
	Data() []byte
}

type Response interface {
	// those meta info shouldn't be changed
	ID() uint64
	StreamID() uint64
	ClientID() uint64
	Method() string

	// application data
	Data() []byte
	SetData([]byte)
	Error() error
	SetError(error)
}

// rpc functions
type RPC func(Request, Response)

// hijack rpc functions
type HijackRPC func(string, Request, Response)

type Cnss byte

const (
	CnssAtLeastOnce = packet.CnssAtLeastOnce
	CnssAtMostOnce  = packet.CnssAtMostOnce
)

type Message interface {
	Done() error
	Error(err error)
	ID() uint64
	StreamID() uint64
	ClientID() uint64
	// consistency protocol
	Consistency() Cnss

	// application data
	Data() []byte
	Topic() string
}
