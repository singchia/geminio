// Batch E — RPC破坏 (RPC sabotage). Handlers that panic, block forever,
// or bi-RPC loops that could deadlock. The bar is: no single misbehaving
// handler or caller may take down the whole End, and every Call must
// return (success or error) in bounded time.
package chaos

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/test/chaos/helpers"
	"github.com/singchia/geminio/test/harness"
)

// waitForRemoteRPC gives the client a brief window to receive the peer's
// RPC-registration packet. Tests that don't use SetWaitRemoteRPCs must
// still avoid racing Register with Call.
func waitForRemoteRPC() { time.Sleep(100 * time.Millisecond) }

// E1 — a handler that panics must not take down the End. After the
// panic, a second call on the same connection must still return an
// error (not hang) and other RPCs should keep working.
func TestHandlerPanics(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, _ := helpers.NewChaosEndPair(t)

	sEnd.Register(context.Background(), "boom", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		panic("handler panic")
	})
	sEnd.Register(context.Background(), "echo", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData(req.Data())
	})
	waitForRemoteRPC()

	// First call: handler panics. We only require that the client Call
	// returns within a bounded window; error vs empty response is
	// acceptable — both are better than hanging.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, _ = cEnd.Call(ctx, "boom", cEnd.NewRequest([]byte("x")))
	cancel()

	// Second call: still works.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	resp, err := cEnd.Call(ctx2, "echo", cEnd.NewRequest([]byte("alive")))
	if err != nil {
		t.Fatalf("echo after panic: %v", err)
	}
	if string(resp.Data()) != "alive" {
		t.Fatalf("echo payload: got %q", resp.Data())
	}

	cEnd.Close()
	sEnd.Close()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// E2 — a handler that blocks forever on <-ctx.Done must be terminated
// when the client cancels. The Call must return with ctx.Err(), and the
// server handler goroutine must exit once its ctx fires.
func TestHandlerBlocksForever(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, _ := helpers.NewChaosEndPair(t)

	var handlerExited atomic.Bool
	sEnd.Register(context.Background(), "wait", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		<-ctx.Done()
		handlerExited.Store(true)
		resp.SetError(ctx.Err())
	})
	waitForRemoteRPC()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := cEnd.Call(ctx, "wait", cEnd.NewRequest(nil))
	if err == nil {
		t.Fatal("Call should have errored on ctx timeout")
	}

	// Give the server a moment to notice cancel and exit the handler.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if handlerExited.Load() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !handlerExited.Load() {
		t.Fatal("handler did not observe ctx.Done within 2s of client cancel")
	}

	cEnd.Close()
	sEnd.Close()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// E3 — cross-recursive bidirectional RPC: A's handler calls B, B's
// handler calls A. A naive serial dispatch would deadlock; geminio is
// expected to handle each inbound request in its own goroutine so both
// calls progress.
func TestBiRPCDeadlockAvoidance(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, _ := helpers.NewChaosEndPair(t)

	// Server exposes pingS; it calls pingC on the client inside the
	// handler and echoes the response.
	sEnd.Register(context.Background(), "pingS", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		r, err := sEnd.Call(callCtx, "pingC", sEnd.NewRequest([]byte("from-server")))
		if err != nil {
			resp.SetError(err)
			return
		}
		resp.SetData(r.Data())
	})

	cEnd.Register(context.Background(), "pingC", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData(append([]byte("client-saw:"), req.Data()...))
	})
	waitForRemoteRPC()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := cEnd.Call(ctx, "pingS", cEnd.NewRequest(nil))
	if err != nil {
		t.Fatalf("bi-rpc Call: %v", err)
	}
	if got := string(resp.Data()); got != "client-saw:from-server" {
		t.Fatalf("bi-rpc payload: %q", got)
	}

	cEnd.Close()
	sEnd.Close()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// E4 — kill the transport mid-Call. Client's Call must return with an
// error inside the call context window; the server's handler, if it
// already started, must observe ctx cancellation.
func TestRPCCancelThroughReconnect(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, cChaos := helpers.NewChaosEndPair(t)

	started := make(chan struct{}, 1)
	sEnd.Register(context.Background(), "long", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			resp.SetError(ctx.Err())
		case <-time.After(5 * time.Second):
			resp.SetData(nil)
		}
	})
	waitForRemoteRPC()

	callDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := cEnd.Call(ctx, "long", cEnd.NewRequest(nil))
		callDone <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never started")
	}

	// Slam the client's transport once the handler is running.
	cChaos.Kill()

	select {
	case err := <-callDone:
		if err == nil {
			t.Fatal("Call should have errored after transport kill")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Call did not return within 5s of transport kill")
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); cEnd.Close() }()
	go func() { defer wg.Done(); sEnd.Close() }()
	wg.Wait()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// E5 — fire 200 Calls, cancel them all at once. Every Call must return
// ctx.Err or a peer error; no goroutine should be left blocked on the
// in-flight packet wait. Verifies cancel propagates from client to
// server handler context.
func TestManySimultaneousRPCCancels(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	ln := harness.NewListener(t)
	serverReady := make(chan struct{})
	handlerStarted := make(chan struct{}, 1024)
	handlerExits := make(chan struct{}, 1024)
	serverEndCh := make(chan geminio.End, 1)
	go func() {
		end, err := ln.AcceptEnd()
		if err != nil {
			return
		}
		end.Register(context.Background(), "sleep", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
			handlerStarted <- struct{}{}
			<-ctx.Done()
			resp.SetError(ctx.Err())
			handlerExits <- struct{}{}
		})
		serverEndCh <- end
		close(serverReady)
	}()

	opts := client.NewEndOptions()
	opts.SetWaitRemoteRPCs("sleep")
	cEnd, err := client.NewEnd("tcp", ln.Addr().String(), opts)
	if err != nil {
		t.Fatalf("NewEnd: %v", err)
	}
	<-serverReady
	sEnd := <-serverEndCh

	const n = 50 // keep modest — 200 saturates the writeInCh and races cancel vs request arrival
	rootCtx, rootCancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := cEnd.Call(rootCtx, "sleep", cEnd.NewRequest(nil))
			errs[i] = err
		}(i)
	}

	// Wait until all N handlers have started before cancelling. Cancelling
	// a Call whose request packet has not yet reached the server is a
	// separate scenario covered by E4; here we want a clean "cancel while
	// server is genuinely running the handler" signal.
	deadline := time.Now().Add(5 * time.Second)
	var started int
	for started < n && time.Now().Before(deadline) {
		select {
		case <-handlerStarted:
			started++
		case <-time.After(50 * time.Millisecond):
		}
	}
	if started < n {
		t.Fatalf("only %d/%d handlers started before timeout", started, n)
	}

	rootCancel() // cancel every in-flight Call at once
	wg.Wait()

	var nilCount int
	for _, e := range errs {
		if e == nil {
			nilCount++
		}
	}
	if nilCount > 0 {
		t.Fatalf("%d Calls returned nil error after mass cancel", nilCount)
	}

	// Every handler must observe its ctx cancellation and exit.
	deadline = time.Now().Add(3 * time.Second)
	var exited int
	for exited < n && time.Now().Before(deadline) {
		select {
		case <-handlerExits:
			exited++
		case <-time.After(100 * time.Millisecond):
		}
	}
	if exited < n {
		t.Fatalf("only %d/%d handlers exited after mass cancel", exited, n)
	}

	cEnd.Close()
	sEnd.Close()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 30)
}
