package client

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/delegate"
	"github.com/singchia/geminio/options"
	"github.com/singchia/go-timer/v2"
)

type RetryEnd struct {
	opts *RetryEndOptions
	*delegate.UnimplementedDelegate

	end unsafe.Pointer
	ok  *int32

	retry     sync.Mutex
	onceClose *sync.Once

	// dialer to use while retry
	dialer Dialer

	// rpcs for re-register
	rpcs   map[string]geminio.RPC
	rpcMtx sync.RWMutex
	// hijack
	hijackRPCOpts *options.HijackOptions
	hijackRPC     geminio.HijackRPC
}

func NewRetryEndWithDialer(dialer Dialer, opts ...*RetryEndOptions) (geminio.End, error) {
	// options
	eo := MergeRetryEndOptions(opts...)
	initRetryEndOptions(eo)
	ok := int32(1)
	re := &RetryEnd{
		opts:                  eo,
		UnimplementedDelegate: &delegate.UnimplementedDelegate{},
		dialer:                dialer,
		ok:                    &ok,
		onceClose:             &sync.Once{},
		rpcs:                  make(map[string]geminio.RPC),
	}
	if eo.Timer == nil {
		eo.Timer = timer.NewTimer()
		eo.TimerOwner = re
	}

	// replace outside delegate to ours
	re.opts.delegate = re.opts.Delegate
	re.opts.Delegate = re
	end, err := re.getEnd()
	if err != nil {
		goto ERR
	}
	re.end = unsafe.Pointer(end)
	return re, nil
ERR:
	atomic.StoreInt32(re.ok, 0)
	if eo.TimerOwner == re {
		eo.Timer.Close()
	}
	return nil, err
}

func (re *RetryEnd) getEnd() (*clientEnd, error) {
	end, err := NewEndWithDialer(re.dialer, re.opts.EndOptions)
	if err != nil {
		return nil, err
	}
	if re.opts.delegate != nil {
		re.opts.delegate.ConnOnline(end)
	}
	return end.(*clientEnd), nil
}

func (re *RetryEnd) reinit(old *clientEnd) error {
	// the reinit take times
	re.retry.Lock()
	defer re.retry.Unlock()

	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	if cur != old {
		// already reinited
		return nil
	}
	// release the end
	if old != nil {
		old.Close()
	}

	time.Sleep(3 * time.Second)
	if atomic.LoadInt32(re.ok) == 0 {
		return io.EOF
	}
	new, err := re.getEnd()
	if err != nil {
		// if we still get end error, deliver the error to user, but it
		// doesn't mean to close the end
		return err
	}
	atomic.StorePointer(&re.end, unsafe.Pointer(new))
	// TODO optimize rests
	time.Sleep(1 * time.Second)

	// hijack
	if re.hijackRPC != nil {
		err = new.Hijack(re.hijackRPC, re.hijackRPCOpts)
		if err != nil {
			return err
		}
	}
	// register legacy functions
	re.rpcMtx.RLock()
	for method, rpc := range re.rpcs {
		// TODO inconsistency for the context
		err = re.register(context.TODO(), method, rpc, false)
		if err != nil {
			re.rpcMtx.RUnlock()
			return err
		}
	}
	re.rpcMtx.RUnlock()

	// after retry the end succeed, after hijack and register legacy functions,
	// the brand new end online
	if re.opts.delegate != nil {
		re.opts.delegate.EndReOnline(new)
	}
	return nil
}

// wrappered delegate
func (re *RetryEnd) ConnOnline(conn delegate.ConnDescriber) error {
	delegate := re.opts.delegate
	if delegate != nil {
		return delegate.ConnOnline(conn)
	}
	return nil
}

func (re *RetryEnd) ConnOffline(conn delegate.ConnDescriber) error {
	delegate := re.opts.delegate
	if delegate != nil {
		// The offline is just a notification, we're still keep on trying reconnct
		err := delegate.ConnOffline(conn)
		if err != nil {
			return err
		}
	}
	fn := func() {
		for atomic.LoadInt32(re.ok) == 1 {
			end := (*clientEnd)(atomic.LoadPointer(&re.end))
			err := re.reinit(end)
			if err != nil {
				re.opts.Log.Infof("retry client offline and retry failed: %s", err)
				continue
			}
			re.opts.Log.Infof("retry client offline and retry succeed")
			return
		}
	}
	// in case of blocking the function
	go fn()
	return nil
}

func (re *RetryEnd) DialogueOnline(dialogue delegate.DialogueDescriber) error {
	delegate := re.opts.delegate
	if delegate != nil {
		return delegate.DialogueOnline(dialogue)
	}
	return nil
}

func (re *RetryEnd) DialogueOffline(dialogue delegate.DialogueDescriber) error {
	delegate := re.opts.delegate
	if delegate != nil {
		return delegate.DialogueOffline(dialogue)
	}
	return nil
}

func (re *RetryEnd) RemoteRegistration(method string, clientID uint64, streamID uint64) {
	delegate := re.opts.delegate
	if delegate != nil {
		delegate.RemoteRegistration(method, clientID, streamID)
	}
}

// only called at server side
func (re *RetryEnd) GetClientID(meta []byte) (uint64, error) { return 0, nil }

// Multiplexer
func (re *RetryEnd) OpenStream(opts ...*options.OpenStreamOptions) (geminio.Stream, error) {
	if atomic.LoadInt32(re.ok) != 1 {
		// TODO optimize the error
		return nil, io.EOF
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	sm, oerr := cur.OpenStream(opts...)
	if oerr != nil {
		if oerr == io.EOF && atomic.LoadInt32(re.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := re.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should only return io.EOF aflter RetryEnd Close
					re.opts.Log.Infof("reinit got io.EOF after Call err: %s", oerr)
					return nil, ierr
				}
				// some other error, maybe ErrInvalidConn, ErrClosed
				return nil, ierr
			}
			// retry succeed, recursive the Call
			return re.OpenStream(opts...)
		}
		return nil, oerr
	}
	return sm, nil
}

func (re *RetryEnd) AcceptStream() (geminio.Stream, error) {
	if atomic.LoadInt32(re.ok) != 1 {
		// TODO optimize the error
		return nil, io.EOF
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	sm, aerr := cur.AcceptStream()
	if aerr != nil {
		if aerr == io.EOF && atomic.LoadInt32(re.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := re.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should only return io.EOF aflter RetryEnd Close
					re.opts.Log.Infof("reinit got io.EOF after AcceptStream err: %s", aerr)
					return nil, ierr
				}
				// some other error, maybe ErrInvalidConn, ErrClosed
				return nil, ierr
			}
			// retry succeed, recursive the AcceptStream
			return re.AcceptStream()
		}
		return nil, aerr
	}
	return sm, nil
}

func (re *RetryEnd) Accept() (net.Conn, error) {
	return re.AcceptStream()
}

func (re *RetryEnd) ListStreams() []geminio.Stream {
	if atomic.LoadInt32(re.ok) != 1 {
		// TODO optimize the error
		return nil
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	return cur.ListStreams()
}

// RPCer
func (re *RetryEnd) NewRequest(data []byte, opts ...*options.NewRequestOptions) geminio.Request {
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	return cur.NewRequest(data, opts...)
}

func (re *RetryEnd) Call(ctx context.Context, method string, req geminio.Request,
	opts ...*options.CallOptions) (geminio.Response, error) {
	if atomic.LoadInt32(re.ok) != 1 {
		// TODO optimize the error
		return nil, io.EOF
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	rsp, cerr := cur.Call(ctx, method, req, opts...)
	if cerr != nil {
		if cerr == io.EOF && atomic.LoadInt32(re.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := re.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should only return io.EOF aflter RetryEnd Close
					re.opts.Log.Infof("reinit got io.EOF after Call err: %s", cerr)
					return nil, ierr
				}
				// some other error, maybe ErrInvalidConn, ErrClosed
				return nil, ierr
			}
			// retry succeed, recursive the Call
			return re.Call(ctx, method, req, opts...)
		}
		return nil, cerr
	}
	return rsp, nil
}

func (re *RetryEnd) CallAsync(ctx context.Context, method string, req geminio.Request, ch chan *geminio.Call,
	opts ...*options.CallOptions) (*geminio.Call, error) {
	if atomic.LoadInt32(re.ok) != 1 {
		// TODO optimize the error
		return nil, io.EOF
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	call, cerr := cur.CallAsync(ctx, method, req, ch, opts...)
	if cerr != nil {
		if cerr == io.EOF && atomic.LoadInt32(re.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := re.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should only return io.EOF aflter RetryEnd Close
					re.opts.Log.Infof("reinit got io.EOF after Callasync err: %s", cerr)
					return nil, ierr
				}
				// some other error, maybe ErrInvalidConn, ErrClosed
				return nil, ierr
			}
			// retry succeed, recursive the CallAsync
			return re.CallAsync(ctx, method, req, ch, opts...)
		}
		return nil, cerr
	}
	return call, nil
}

func (re *RetryEnd) Register(ctx context.Context, method string, rpc geminio.RPC) error {
	return re.register(ctx, method, rpc, true)
}

func (re *RetryEnd) register(ctx context.Context, method string, rpc geminio.RPC,
	memorize bool) error {
	if atomic.LoadInt32(re.ok) != 1 {
		// TODO optimize the error
		return io.EOF
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	rerr := cur.Register(ctx, method, rpc)
	if rerr != nil {
		if rerr == io.EOF && atomic.LoadInt32(re.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := re.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should only return io.EOF aflter RetryEnd Close
					re.opts.Log.Infof("reinit got io.EOF after register err: %s", rerr)
					return ierr
				}
				// some other error, maybe ErrInvalidConn, ErrClosed
				return ierr
			}
			// retry succeed, recursive the register
			return re.register(ctx, method, rpc, true)
		}
		return rerr
	}
	if memorize {
		// memorize the rpc in case of retry
		re.rpcMtx.Lock()
		re.rpcs[method] = rpc
		re.rpcMtx.Unlock()
	}
	return nil
}

func (re *RetryEnd) Hijack(rpc geminio.HijackRPC, opts ...*options.HijackOptions) error {
	if atomic.LoadInt32(re.ok) != 1 {
		// TODO optimize the error
		return io.EOF
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	herr := cur.Hijack(rpc, opts...)
	if herr != nil {
		if herr == io.EOF && atomic.LoadInt32(re.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := re.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should only return io.EOF aflter RetryEnd Close
					re.opts.Log.Infof("reinit got io.EOF after Hijack err: %s", herr)
					return ierr
				}
				return ierr
			}
			// retry succeed, recursive the Hijack
			return re.Hijack(rpc, opts...)
		}
		return herr
	}
	// memorize the hijack
	re.hijackRPC = rpc
	re.hijackRPCOpts = options.MergeHijackOptions(opts...)
	return nil
}

// Messager
func (re *RetryEnd) NewMessage(data []byte, opts ...*options.NewMessageOptions) geminio.Message {
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	return cur.NewMessage(data, opts...)
}

func (re *RetryEnd) Publish(ctx context.Context, msg geminio.Message,
	opts ...*options.PublishOptions) error {
	if atomic.LoadInt32(re.ok) != 1 {
		// TODO optimize the error
		return io.EOF
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	perr := cur.Publish(ctx, msg, opts...)
	if perr != nil {
		if perr == io.EOF && atomic.LoadInt32(re.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := re.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should only return io.EOF aflter RetryEnd Close
					re.opts.Log.Infof("reinit got io.EOF after Publish err: %s", perr)
					return ierr
				}
				return ierr
			}
			// retry succeed, recursive the Publish
			return re.Publish(ctx, msg, opts...)
		}
		return perr
	}
	return nil
}

func (re *RetryEnd) PublishAsync(ctx context.Context, msg geminio.Message, ch chan *geminio.Publish,
	opts ...*options.PublishOptions) (*geminio.Publish, error) {
	if atomic.LoadInt32(re.ok) != 1 {
		// TODO optimize the error
		return nil, io.EOF
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	pub, perr := cur.PublishAsync(ctx, msg, ch, opts...)
	if perr != nil {
		if perr == io.EOF && atomic.LoadInt32(re.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := re.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should only return io.EOF aflter RetryEnd Close
					re.opts.Log.Infof("reinit got io.EOF after PublishAsync err: %s", perr)
					return nil, ierr
				}
				return nil, ierr
			}
			// retry succeed, recursive the PublishAsync
			return re.PublishAsync(ctx, msg, ch, opts...)
		}
		return nil, perr
	}
	return pub, nil
}

func (re *RetryEnd) Receive(ctx context.Context) (geminio.Message, error) {
	if atomic.LoadInt32(re.ok) != 1 {
		// TODO optimize the error
		return nil, io.EOF
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	msg, rerr := cur.Receive(ctx)
	if rerr != nil {
		if rerr == io.EOF && atomic.LoadInt32(re.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := re.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should only return io.EOF aflter RetryEnd Close
					re.opts.Log.Infof("reinit got io.EOF after Receive err: %s", rerr)
					return nil, ierr
				}
				return nil, ierr
			}
			// retry succeed, recursive the Receive
			return re.Receive(ctx)
		}
		return nil, rerr
	}
	return msg, nil
}

// Raw
func (re *RetryEnd) Read(b []byte) (int, error) {
	if atomic.LoadInt32(re.ok) != 1 {
		// TODO optimize the error
		return 0, io.EOF
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	n, rerr := cur.Read(b)
	if rerr != nil {
		if rerr == io.EOF && atomic.LoadInt32(re.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := re.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should only return io.EOF aflter RetryEnd Close
					re.opts.Log.Infof("reinit got io.EOF after Read err: %s", rerr)
					return 0, ierr
				}
				return 0, ierr
			}
			// retry succeed, recursive the Read
			return re.Read(b)
		}
		return 0, rerr
	}
	return n, nil
}

func (re *RetryEnd) Write(b []byte) (int, error) {
	if atomic.LoadInt32(re.ok) != 1 {
		// TODO optimize the error
		return 0, io.EOF
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	n, werr := cur.Write(b)
	if werr != nil {
		if werr == io.EOF && atomic.LoadInt32(re.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := re.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should only return io.EOF aflter RetryEnd Close
					re.opts.Log.Infof("reinit got io.EOF after Write err: %s", werr)
					return 0, ierr
				}
				return 0, ierr
			}
			// retry succeed, recursive the Write
			return re.Write(b)
		}
		return 0, werr
	}
	return n, nil
}

func (re *RetryEnd) Close() error {
	var err error
	re.onceClose.Do(func() {
		cur := (*clientEnd)(atomic.LoadPointer(&re.end))
		// set re.ok false, no more reconnect
		atomic.StoreInt32(re.ok, 0)
		err = cur.Close()
		if re.opts.TimerOwner == re {
			re.opts.Timer.Close()
		}
	})
	return err
}

func (re *RetryEnd) Addr() net.Addr {
	return re.LocalAddr()
}

func (re *RetryEnd) LocalAddr() net.Addr {
	if re.end == nil {
		return nil
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	return cur.LocalAddr()
}

func (re *RetryEnd) RemoteAddr() net.Addr {
	if re.end == nil {
		return nil
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	return cur.RemoteAddr()
}

func (re *RetryEnd) SetDeadline(t time.Time) error {
	if atomic.LoadInt32(re.ok) != 1 {
		// TODO optimize the error
		return io.EOF
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	return cur.SetDeadline(t)
}

func (re *RetryEnd) SetReadDeadline(t time.Time) error {
	if atomic.LoadInt32(re.ok) != 1 {
		// TODO optimize the error
		return io.EOF
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	return cur.SetReadDeadline(t)
}

func (re *RetryEnd) SetWriteDeadline(t time.Time) error {
	if atomic.LoadInt32(re.ok) != 1 {
		// TODO optimize the error
		return io.EOF
	}
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	return cur.SetWriteDeadline(t)
}

func (re *RetryEnd) StreamID() uint64 {
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	return cur.StreamID()
}

func (re *RetryEnd) ClientID() uint64 {
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	return cur.ClientID()
}

func (re *RetryEnd) Meta() []byte {
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	return cur.Meta()
}

func (re *RetryEnd) Side() geminio.Side {
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	return cur.Side()
}

func (re *RetryEnd) Peer() string {
	cur := (*clientEnd)(atomic.LoadPointer(&re.end))
	return cur.Peer()
}
