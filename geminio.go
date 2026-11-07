package geminio

import (
	"context"
	"errors"
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
	// custom data
	Custom() []byte

	SetTimeout(timeout time.Duration)
	SetCustom([]byte)
	SetClientID(clientID uint64)
	SetStreamID(streamID uint64)
}

type Response interface {
	// those meta info shouldn't be changed
	ID() uint64
	StreamID() uint64
	ClientID() uint64
	Method() string

	// application data
	Data() []byte
	Error() error
	// custom data
	Custom() []byte

	SetData([]byte)
	SetError(error)
	SetCustom([]byte)
	SetClientID(clientID uint64)
	SetStreamID(streamID uint64)
}

type MethodRPC struct {
	Method string
	RPC    RPC
}

// rpc functions
type RPC func(context.Context, Request, Response)

// hijack rpc functions
type HijackRPC func(context.Context, string, Request, Response)

// for async RPC
type Call struct {
	Method   string
	Request  Request
	Response Response
	Error    error
	Done     chan *Call
}

type RPCer interface {
	NewRequest(data []byte, opts ...*options.NewRequestOptions) Request

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
	Topic() string // empty if not set
	// consistency protocol
	Cnss() options.Cnss
	// application data
	Data() []byte
	// custom data
	Custom() []byte

	// those Set operations must be accomplish before Publish
	SetTimeout(timeout time.Duration)
	SetCustom(data []byte)
	SetTopic(topic string)
	SetClientID(clientID uint64)
	SetStreamID(streamID uint64)
}

// for async Publish
type Publish struct {
	Message Message
	Error   error
	Done    chan *Publish
}

type Messager interface {
	NewMessage(data []byte, opts ...*options.NewMessageOptions) Message

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
	// ForceClose cancels this stream locally without draining or notifying the peer.
	// It is idempotent and does not wait for cleanup to finish.
	ForceClose() error
	// a stream is a geminio
	RawRPCMessager
	// meta info for a stream
	StreamID() uint64
	ClientID() uint64
	Meta() []byte
	Side() Side
	Peer() string
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

	// End is a net.Listener
	// Accept is a wrapper for AcceptStream
	// Addr is a wrapper for LocalAddr
	net.Listener

	Close() error
}

// ContextMultiplexer opens a stream with a deadline for negotiation only.
// Canceling ctx after a successful return does not close the stream.
// This optional interface preserves compatibility with existing End implementations.
type ContextMultiplexer interface {
	OpenStreamContext(context.Context, ...*options.OpenStreamOptions) (Stream, error)
}

var ErrOpenStreamContextUnsupported = errors.New("context-aware stream opening unsupported")

// OpenStreamContext requires cancellation support rather than silently ignoring ctx.
func OpenStreamContext(ctx context.Context, end Multiplexer, opts ...*options.OpenStreamOptions) (Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opener, ok := end.(ContextMultiplexer); ok {
		return opener.OpenStreamContext(ctx, opts...)
	}
	return nil, ErrOpenStreamContextUnsupported
}

// ErrStreamAborted distinguishes abnormal termination from an orderly EOF.
var ErrStreamAborted = errors.New("stream aborted")
var ErrStreamAbortUnsupported = errors.New("peer does not support stream abort")

// AbortStream releases local IO immediately and, when negotiated, asks the peer
// to terminate this stream. It does not abort other streams on the connection.
// Unsupported peers still get local cleanup, but no remote cleanup guarantee.
func AbortStream(s Stream) error {
	if a, ok := s.(interface{ Abort() error }); ok {
		return a.Abort()
	}
	_ = s.ForceClose()
	return ErrStreamAbortUnsupported
}
