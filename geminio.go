package geminio

import "time"

type Request interface {
	// those meta info shouldn't be changed
	RequestID() uint64
	StreamID() uint64
	ClientID() uint64
	Method() string
	Timeout() time.Duration

	// application data
	Data() []byte
}

type Response interface {
	// those meta info shouldn't be changed
	RequestID() uint64
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
