package geminio

import (
	"time"
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
	CnssAtMostOnce  Cnss = 0
	CnssAtLeastOnce Cnss = 1
)

type Message interface {
	// to tell peer received or errored
	Done() error
	Error(err error)
	// those meta info shouldn't be changed
	ID() uint64
	StreamID() uint64
	ClientID() uint64
	Timeout() time.Duration
	// consistency protocol
	Cnss() Cnss

	// application data
	Data() []byte
}

type MessageAttribute struct {
	Timeout time.Duration
	Cnss    Cnss
}

type OptionMessageAttribute func(*MessageAttribute)

func WithMessageTimeout(timeout time.Duration) OptionMessageAttribute {
	return func(opt *MessageAttribute) {
		opt.Timeout = timeout
	}
}

func WithMessagehCnss(cnss Cnss) OptionMessageAttribute {
	return func(opt *MessageAttribute) {
		opt.Cnss = cnss
	}
}
