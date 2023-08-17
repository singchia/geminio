package geminio

import (
	"context"
	"net"
	"time"
)

// RPC releated
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

type RequestAttribute struct {
	Timeout time.Duration
}

type OptionRequestAttribute func(*RequestAttribute)

func WithRequestTimeout(timeout time.Duration) OptionRequestAttribute {
	return func(opt *RequestAttribute) {
		opt.Timeout = timeout
	}
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
type RPC func(context.Context, Request, Response)

// hijack rpc functions
type HijackRPC func(string, context.Context, Request, Response)

// for async RPC
type Call struct {
	Method   string
	Request  Request
	Response Response
	Error    error
	Done     chan *Call
}

type RPCer interface {
	NewRequest(data []byte, opts ...OptionRequestAttribute) Request
	Call(ctx context.Context, method string, req Request) (Response, error)
	CallAsync(ctx context.Context, method string, req Request, ch chan *Call) (*Call, error)
	Register(ctx context.Context, method string) error
}

// message related defination
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

// for async Publish
type Publish struct {
	Message Message
	Error   error
	Done    chan *Publish
}

type Messager interface {
	NewMessage(data []byte, opts ...MessageAttribute) Message
	Publish(ctx context.Context, msg Message) error
	PublishAsync(ctx context.Context, msg Message, publish chan *Publish) (*Publish, error)
	Receive() (Message, error)
}

type Raw net.Conn

// Application
type Application interface {
	// rpc
	RPCer
	// message
	Messager
	// raw
	Raw
}

type Stream interface {
	// a stream is a geminio
	Application
	// meta info for a stream
	StreamID() uint64
	ClientID() uint64
	Meta() []byte
}

// Stream multiplexer
type Multiplexer interface {
	OpenStream(meta []byte) (Stream, error)
	AcceptStream() (Stream, error)
	ListStreams() []Stream
}

type Geminio interface {
	// End is a default stream with streamID 1
	Stream
	// End is a stream multiplexer
	Multiplexer
}
