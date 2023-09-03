package geminio

import (
	"context"
	"net"
	"time"

	"github.com/singchia/geminio/options"
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
	NewRequest(data []byte) Request

	Call(ctx context.Context, method string, req Request, opts ...*options.CallOptions) (Response, error)
	CallAsync(ctx context.Context, method string, req Request, ch chan *Call, opts ...*options.CallOptions) (*Call, error)
	Register(ctx context.Context, method string, rpc RPC) error
	// Hijack rpc from remote
	Hijack(rpc HijackRPC, opts ...*options.HijackOptions) error
}

type Message interface {
	// to tell peer received or errored
	Done() error
	Error(err error) error
	// those meta info shouldn't be changed
	ID() uint64
	StreamID() uint64
	ClientID() uint64
	Timeout() time.Duration
	// consistency protocol
	Cnss() options.Cnss
	// application data
	Data() []byte
}

// for async Publish
type Publish struct {
	Message Message
	Error   error
	Done    chan *Publish
}

type Messager interface {
	NewMessage(data []byte) Message

	Publish(ctx context.Context, msg Message, opts ...*options.PublishOptions) error
	PublishAsync(ctx context.Context, msg Message, ch chan *Publish, opts ...*options.PublishOptions) (*Publish, error)
	Receive(ctx context.Context) (Message, error)
}

type Raw net.Conn

type RawRPCMessager interface {
	// raw
	Raw
	// rpc
	RPCer
	// message
	Messager
}

type Side int

const (
	InitiatorSide Side = 0
	RecipientSide Side = 1
)

type Stream interface {
	// a stream is a geminio
	RawRPCMessager
	// meta info for a stream
	StreamID() uint64
	ClientID() uint64
	Meta() []byte
	Side() Side
}

// Stream multiplexer
type Multiplexer interface {
	OpenStream(opts ...*options.OpenStreamOptions) (Stream, error)
	AcceptStream() (Stream, error)
	ListStreams() []Stream
}

type End interface {
	// End is a default stream with streamID 1
	// Close on default stream will close all from the End
	Stream
	// End is a stream multiplexer
	Multiplexer
}
