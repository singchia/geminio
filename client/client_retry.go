package client

import (
	"context"
	"io"
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
		delegate.ConnOffline(conn)
	}
	fn := func() {
		for atomic.LoadInt32(rc.ok) == 1 {
			end := (*application.End)(atomic.LoadPointer(&rc.end))
			err := rc.reinit(end)
			if err != nil {
				rc.opts.Log.Infof("retry client offline and retry failed: %s", err)
				continue
			}
		}
	}
	// in case of blocking the function
	go fn()
	return nil
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

func (rc *RetryClient) register(ctx context.Context, method string, rpc geminio.RPC,
	memorize bool) error {

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
			// retry succeed, recuse the register
			return rc.register(ctx, method, rpc, true)
		}
	}
	if memorize {
		// memorize the rpc in case of retry
		rc.rpcMtx.Lock()
		rc.rpcs[method] = rpc
		rc.rpcMtx.Unlock()
	}
	return nil
}
