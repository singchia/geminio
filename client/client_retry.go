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
	"github.com/singchia/geminio/application"
	"github.com/singchia/geminio/delegate"
	"github.com/singchia/geminio/options"
)

type RetryClient struct {
	opts *ClientOptions

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

func NewRetryWithDialer(dialer Dialer, opts ...*ClientOptions) (geminio.End, error) {
	// options
	co := MergeClientOptions(opts...)
	initOptions(co)
	ok := int32(1)

	rc := &RetryClient{
		opts:      co,
		dialer:    dialer,
		ok:        &ok,
		onceClose: &sync.Once{},
		rpcs:      make(map[string]geminio.RPC),
	}
	end, err := rc.getEnd()
	if err != nil {
		goto ERR
	}
	rc.end = unsafe.Pointer(end)
ERR:
	atomic.StoreInt32(rc.ok, 0)
	return nil, nil
}

func (rc *RetryClient) getEnd() (*application.End, error) {
	end, err := NewWithDialer(rc.dialer, rc.opts)
	if err != nil {
		return nil, err
	}
	if rc.opts.Delegate != nil {
		rc.opts.Delegate.ConnOnline(end)
	}
	return end.(*application.End), nil
}

func (rc *RetryClient) reinit(old *application.End) error {
	// the reinit take times
	rc.retry.Lock()
	defer rc.retry.Unlock()

	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	if cur != old {
		// already reinited
		return nil
	}
	// release the end
	old.Close()

	time.Sleep(3 * time.Second)
	new, err := rc.getEnd()
	if err != nil {
		// if we still get end error, deliver the error to user, but it
		// doesn't mean to close the end
		return err
	}

	// hijack
	if rc.hijackRPC != nil {
		err = new.Hijack(rc.hijackRPC, rc.hijackRPCOpts)
		if err != nil {
			return err
		}
	}
	// register legacy functions
	rc.rpcMtx.RLock()
	for method, rpc := range rc.rpcs {
		// TODO inconsistency for the context
		err = rc.register(context.TODO(), method, rpc, false)
		if err != nil {
			return err
		}
	}
	rc.rpcMtx.RUnlock()

	// after retry the end succeed, after hijack and register legacy functions,
	// the brand new end online
	if rc.opts.Delegate != nil {
		rc.opts.Delegate.ConnOnline(new)
	}
	return nil
}

// wrappered delegate
func (rc *RetryClient) ConnOnline(conn delegate.ConnDescriber) error {
	delegate := rc.opts.Delegate
	if delegate != nil {
		return delegate.ConnOnline(conn)
	}
	return nil
}

func (rc *RetryClient) ConnOffline(conn delegate.ConnDescriber) error {
	delegate := rc.opts.Delegate
	if delegate != nil {
		// The offline is just a notification, we're still keep on trying reconnct
		err := delegate.ConnOffline(conn)
		if err != nil {
			return err
		}
	}
	fn := func() {
		for atomic.LoadInt32(rc.ok) == 1 {
			end := (*application.End)(atomic.LoadPointer(&rc.end))
			err := rc.reinit(end)
			if err != nil {
				rc.opts.Log.Infof("retry client offline and retry failed: %s", err)
				continue
			}
			rc.opts.Log.Infof("retry client offline and retry succeed")
			return
		}
	}
	// in case of blocking the function
	go fn()
	return nil
}

func (rc *RetryClient) Heartbeat(conn delegate.ConnDescriber) error {
	delegate := rc.opts.Delegate
	if delegate != nil {
		return delegate.Heartbeat(conn)
	}
	return nil
}

func (rc *RetryClient) DialogueOnline(dialogue delegate.DialogueDescriber) error {
	delegate := rc.opts.Delegate
	if delegate != nil {
		return delegate.DialogueOnline(dialogue)
	}
	return nil
}

func (rc *RetryClient) DialogueOffline(dialogue delegate.DialogueDescriber) error {
	delegate := rc.opts.Delegate
	if delegate != nil {
		return delegate.DialogueOffline(dialogue)
	}
	return nil
}

func (rc *RetryClient) RemoteRegistration(method string, clientID uint64, streamID uint64) {}

func (rc *RetryClient) GetClientIDByMeta(meta []byte) (uint64, error) { return 0, nil }

// RPCer
func (rc *RetryClient) NewRequest(data []byte) geminio.Request {
	return rc.NewRequest(data)
}

func (rc *RetryClient) Call(ctx context.Context, method string, req geminio.Request,
	opts ...*options.CallOptions) (geminio.Response, error) {
	if atomic.LoadInt32(rc.ok) != 1 {
		// TODO optimize the error
		return nil, io.EOF
	}
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	rsp, cerr := cur.Call(ctx, method, req, opts...)
	if cerr != nil {
		if cerr == io.EOF && atomic.LoadInt32(rc.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := rc.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should never return io.EOF, if do BUG!!
					rc.opts.Log.Errorf("reinit got io.EOF after Call err: %s", cerr)
					return nil, ierr
				}
				// some other error, maybe ErrInvalidConn, ErrClosed
				return nil, ierr
			}
			// retry succeed, recursive the Call
			return rc.Call(ctx, method, req, opts...)
		}
		return nil, cerr
	}
	return rsp, nil
}

func (rc *RetryClient) CallAsync(ctx context.Context, method string, req geminio.Request, ch chan *geminio.Call,
	opts ...*options.CallOptions) (*geminio.Call, error) {
	if atomic.LoadInt32(rc.ok) != 1 {
		// TODO optimize the error
		return nil, io.EOF
	}
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	call, cerr := cur.CallAsync(ctx, method, req, ch, opts...)
	if cerr != nil {
		if cerr == io.EOF && atomic.LoadInt32(rc.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := rc.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should never return io.EOF, if do BUG!!
					rc.opts.Log.Errorf("reinit got io.EOF after Callasync err: %s", cerr)
					return nil, ierr
				}
				// some other error, maybe ErrInvalidConn, ErrClosed
				return nil, ierr
			}
			// retry succeed, recursive the CallAsync
			return rc.CallAsync(ctx, method, req, ch, opts...)
		}
		return nil, cerr
	}
	return call, nil
}

func (rc *RetryClient) Register(ctx context.Context, method string, rpc geminio.RPC) error {
	return rc.register(ctx, method, rpc, true)
}

func (rc *RetryClient) register(ctx context.Context, method string, rpc geminio.RPC,
	memorize bool) error {
	if atomic.LoadInt32(rc.ok) != 1 {
		// TODO optimize the error
		return io.EOF
	}
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	rerr := cur.Register(ctx, method, rpc)
	if rerr != nil {
		if rerr == io.EOF && atomic.LoadInt32(rc.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := rc.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should never return io.EOF, if do BUG!!
					rc.opts.Log.Errorf("reinit got io.EOF after register err: %s", rerr)
					return ierr
				}
				// some other error, maybe ErrInvalidConn, ErrClosed
				return ierr
			}
			// retry succeed, recursive the register
			return rc.register(ctx, method, rpc, true)
		}
		return rerr
	}
	if memorize {
		// memorize the rpc in case of retry
		rc.rpcMtx.Lock()
		rc.rpcs[method] = rpc
		rc.rpcMtx.Unlock()
	}
	return nil
}

func (rc *RetryClient) Hijack(rpc geminio.HijackRPC, opts ...*options.HijackOptions) error {
	if atomic.LoadInt32(rc.ok) != 1 {
		// TODO optimize the error
		return io.EOF
	}
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	herr := cur.Hijack(rpc, opts...)
	if herr != nil {
		if herr == io.EOF && atomic.LoadInt32(rc.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := rc.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should never return io.EOF, if do BUG!!
					rc.opts.Log.Errorf("reinit got io.EOF after Hijack err: %s", herr)
					return ierr
				}
				return ierr
			}
			// retry succeed, recursive the Hijack
			return rc.Hijack(rpc, opts...)
		}
		return herr
	}
	// memorize the hijack
	rc.hijackRPC = rpc
	rc.hijackRPCOpts = options.MergeHijackOptions(opts...)
	return nil
}

// Messager
func (rc *RetryClient) NewMessage(data []byte) geminio.Message {
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	return cur.NewMessage(data)
}

func (rc *RetryClient) Publish(ctx context.Context, msg geminio.Message,
	opts ...*options.PublishOptions) error {
	if atomic.LoadInt32(rc.ok) != 1 {
		// TODO optimize the error
		return io.EOF
	}
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	perr := cur.Publish(ctx, msg, opts...)
	if perr != nil {
		if perr == io.EOF && atomic.LoadInt32(rc.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := rc.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should never return io.EOF, if do BUG!!
					rc.opts.Log.Errorf("reinit got io.EOF after Publish err: %s", perr)
					return ierr
				}
				return ierr
			}
			// retry succeed, recursive the Publish
			return rc.Publish(ctx, msg, opts...)
		}
		return perr
	}
	return nil
}

func (rc *RetryClient) PublishAsync(ctx context.Context, msg geminio.Message, ch chan *geminio.Publish,
	opts ...*options.PublishOptions) (*geminio.Publish, error) {
	if atomic.LoadInt32(rc.ok) != 1 {
		// TODO optimize the error
		return nil, io.EOF
	}
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	pub, perr := cur.PublishAsync(ctx, msg, ch, opts...)
	if perr != nil {
		if perr == io.EOF && atomic.LoadInt32(rc.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := rc.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should never return io.EOF, if do BUG!!
					rc.opts.Log.Errorf("reinit got io.EOF after PublishAsync err: %s", perr)
					return nil, ierr
				}
				return nil, ierr
			}
			// retry succeed, recursive the PublishAsync
			return rc.PublishAsync(ctx, msg, ch, opts...)
		}
		return nil, perr
	}
	return pub, nil
}

func (rc *RetryClient) Receive(ctx context.Context) (geminio.Message, error) {
	if atomic.LoadInt32(rc.ok) != 1 {
		// TODO optimize the error
		return nil, io.EOF
	}
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	msg, rerr := cur.Receive(ctx)
	if rerr != nil {
		if rerr == io.EOF && atomic.LoadInt32(rc.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := rc.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should never return io.EOF, if do BUG!!
					rc.opts.Log.Errorf("reinit got io.EOF after Receive err: %s", rerr)
					return nil, ierr
				}
				return nil, ierr
			}
			// retry succeed, recursive the Receive
			return rc.Receive(ctx)
		}
		return nil, rerr
	}
	return msg, nil
}

// Raw
func (rc *RetryClient) Read(b []byte) (int, error) {
	if atomic.LoadInt32(rc.ok) != 1 {
		// TODO optimize the error
		return 0, io.EOF
	}
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	n, rerr := cur.Read(b)
	if rerr != nil {
		if rerr == io.EOF && atomic.LoadInt32(rc.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := rc.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should never return io.EOF, if do BUG!!
					rc.opts.Log.Errorf("reinit got io.EOF after Read err: %s", rerr)
					return 0, ierr
				}
				return 0, ierr
			}
			// retry succeed, recursive the Read
			return rc.Read(b)
		}
		return 0, rerr
	}
	return n, nil
}

func (rc *RetryClient) Write(b []byte) (int, error) {
	if atomic.LoadInt32(rc.ok) != 1 {
		// TODO optimize the error
		return 0, io.EOF
	}
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	n, werr := cur.Write(b)
	if werr != nil {
		if werr == io.EOF && atomic.LoadInt32(rc.ok) == 1 {
			// under layer EOF but not closed, we should retry the end,
			// pass the old end for comparition
			ierr := rc.reinit(cur)
			if ierr != nil {
				if ierr == io.EOF {
					// reinit should never return io.EOF, if do BUG!!
					rc.opts.Log.Errorf("reinit got io.EOF after Write err: %s", werr)
					return 0, ierr
				}
				return 0, ierr
			}
			// retry succeed, recursive the Write
			return rc.Write(b)
		}
		return 0, werr
	}
	return n, nil
}

func (rc *RetryClient) Close() error {
	var err error
	rc.onceClose.Do(func() {
		cur := (*application.End)(atomic.LoadPointer(&rc.end))
		// set rc.ok false, no more reconnect
		atomic.StoreInt32(rc.ok, 0)
		err = cur.Close()
	})
	return err
}

func (rc *RetryClient) LocalAddr() net.Addr {
	if rc.end == nil {
		return nil
	}
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	return cur.LocalAddr()
}

func (rc *RetryClient) RemoteAddr() net.Addr {
	if rc.end == nil {
		return nil
	}
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	return cur.RemoteAddr()
}

func (rc *RetryClient) SetDeadline(t time.Time) error {
	if atomic.LoadInt32(rc.ok) != 1 {
		// TODO optimize the error
		return io.EOF
	}
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	return cur.SetDeadline(t)
}

func (rc *RetryClient) SetReadDeadline(t time.Time) error {
	if atomic.LoadInt32(rc.ok) != 1 {
		// TODO optimize the error
		return io.EOF
	}
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	return cur.SetReadDeadline(t)
}

func (rc *RetryClient) SetWriteDeadline(t time.Time) error {
	if atomic.LoadInt32(rc.ok) != 1 {
		// TODO optimize the error
		return io.EOF
	}
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	return cur.SetWriteDeadline(t)
}

func (rc *RetryClient) StreamID() uint64 {
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	return cur.StreamID()
}

func (rc *RetryClient) ClientID() uint64 {
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	return cur.ClientID()
}

func (rc *RetryClient) Meta() []byte {
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	return cur.Meta()
}

func (rc *RetryClient) Side() geminio.Side {
	cur := (*application.End)(atomic.LoadPointer(&rc.end))
	return cur.Side()
}
