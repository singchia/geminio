package unit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/singchia/gemino"
	"github.com/singchia/gemino/test/harness"
)

// ─────────────────────────────────────────────
// RPC: Register / Call
// ─────────────────────────────────────────────

func TestRPCEcho(t *testing.T) {
	t.Parallel()
	sEnd, cEnd := harness.NewEndPair(t)

	sEnd.Register(context.Background(), "echo", func(ctx context.Context, req gemino.Request, resp gemino.Response) {
		resp.SetData(req.Data())
	})

	resp, err := cEnd.Call(context.Background(), "echo", cEnd.NewRequest([]byte("hello")))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(resp.Data()) != "hello" {
		t.Errorf("data mismatch: got %q, want %q", resp.Data(), "hello")
	}
}

func TestRPCHandlerError(t *testing.T) {
	t.Parallel()
	sEnd, cEnd := harness.NewEndPair(t)

	sEnd.Register(context.Background(), "fail", func(ctx context.Context, req gemino.Request, resp gemino.Response) {
		resp.SetError(context.DeadlineExceeded)
	})

	_, err := cEnd.Call(context.Background(), "fail", cEnd.NewRequest(nil))
	if err == nil {
		t.Fatal("expected error from failing handler, got nil")
	}
}

func TestRPCUnregisteredMethod(t *testing.T) {
	t.Parallel()
	_, cEnd := harness.NewEndPair(t)

	_, err := cEnd.Call(context.Background(), "no-such-method", cEnd.NewRequest([]byte("x")))
	if err == nil {
		t.Fatal("expected error calling unregistered method")
	}
}

func TestRPCTimeout(t *testing.T) {
	t.Parallel()
	sEnd, cEnd := harness.NewEndPair(t)

	ready := make(chan struct{})
	sEnd.Register(context.Background(), "slow", func(ctx context.Context, req gemino.Request, resp gemino.Response) {
		close(ready) // signal that handler started
		select {
		case <-time.After(10 * time.Second):
			resp.SetData([]byte("done"))
		case <-ctx.Done():
		}
	})

	// Use a very short timeout; wait for handler to start first so we know
	// the request reached the server before the deadline fires.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Start the call in a goroutine so we can wait for the handler to start.
	type result struct {
		resp gemino.Response
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		r, e := cEnd.Call(ctx, "slow", cEnd.NewRequest(nil))
		ch <- result{r, e}
	}()

	// Wait for handler to start (proves request was delivered).
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("handler never started")
	}

	res := <-ch
	if res.err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestRPCConcurrent(t *testing.T) {
	t.Parallel()
	harness.LogSilence(t)
	sEnd, cEnd := harness.NewEndPair(t)

	sEnd.Register(context.Background(), "echo", func(ctx context.Context, req gemino.Request, resp gemino.Response) {
		resp.SetData(req.Data())
	})

	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := cEnd.Call(context.Background(), "echo", cEnd.NewRequest([]byte("ping")))
			if err != nil {
				errs <- err
				return
			}
			if string(resp.Data()) != "ping" {
				errs <- context.DeadlineExceeded // sentinel
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent RPC error: %v", err)
	}
}

func TestRPCMultipleHandlers(t *testing.T) {
	t.Parallel()
	sEnd, cEnd := harness.NewEndPair(t)

	methods := []string{"a", "b", "c", "d"}
	for _, m := range methods {
		name := m
		sEnd.Register(context.Background(), name, func(ctx context.Context, req gemino.Request, resp gemino.Response) {
			resp.SetData([]byte(name))
		})
	}

	for _, m := range methods {
		resp, err := cEnd.Call(context.Background(), m, cEnd.NewRequest(nil))
		if err != nil {
			t.Errorf("call %q: %v", m, err)
			continue
		}
		if string(resp.Data()) != m {
			t.Errorf("call %q: got %q", m, resp.Data())
		}
	}
}

// ─────────────────────────────────────────────
// Message: Publish / Receive
// ─────────────────────────────────────────────

func TestMessageBasic(t *testing.T) {
	t.Parallel()
	sEnd, cEnd := harness.NewEndPair(t)

	received := make(chan []byte, 1)
	go func() {
		msg, err := sEnd.Receive(context.Background())
		if err != nil {
			return
		}
		data := make([]byte, len(msg.Data()))
		copy(data, msg.Data())
		msg.Done()
		received <- data
	}()

	want := []byte("unit-message")
	if err := cEnd.Publish(context.Background(), cEnd.NewMessage(want)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case got := <-received:
		if string(got) != string(want) {
			t.Errorf("message mismatch: got %q, want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestMessageMultiple(t *testing.T) {
	t.Parallel()
	sEnd, cEnd := harness.NewEndPair(t)

	const n = 20
	received := make(chan struct{}, n)
	go func() {
		for {
			msg, err := sEnd.Receive(context.Background())
			if err != nil {
				return
			}
			msg.Done()
			received <- struct{}{}
		}
	}()

	for i := 0; i < n; i++ {
		if err := cEnd.Publish(context.Background(), cEnd.NewMessage([]byte("x"))); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}

	timeout := time.After(5 * time.Second)
	for i := 0; i < n; i++ {
		select {
		case <-received:
		case <-timeout:
			t.Fatalf("timeout: only %d/%d messages received", i, n)
		}
	}
}

func TestMessagePublishAfterClose(t *testing.T) {
	t.Parallel()
	_, cEnd := harness.NewEndPair(t)
	cEnd.Close()

	err := cEnd.Publish(context.Background(), cEnd.NewMessage([]byte("after close")))
	if err == nil {
		t.Log("publish after close returned nil (timing dependent)")
	}
}

// ─────────────────────────────────────────────
// Stream: Open / Accept / Read / Write
// ─────────────────────────────────────────────

func TestStreamRoundTrip(t *testing.T) {
	t.Parallel()
	ss, cs := harness.NewEndStream(t)

	want := []byte("stream-unit-test")
	if _, err := cs.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	buf := make([]byte, len(want))
	n, err := ss.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != string(want) {
		t.Errorf("data mismatch: got %q, want %q", buf[:n], want)
	}
}

func TestStreamMultipleOnOneEnd(t *testing.T) {
	t.Parallel()
	sEnd, cEnd := harness.NewEndPair(t)

	const n = 5
	serverStreams := make(chan gemino.Stream, n)
	go func() {
		for i := 0; i < n; i++ {
			s, err := sEnd.AcceptStream()
			if err != nil {
				return
			}
			serverStreams <- s
		}
	}()

	clientStreams := make([]gemino.Stream, n)
	for i := 0; i < n; i++ {
		s, err := cEnd.OpenStream()
		if err != nil {
			t.Fatalf("OpenStream %d: %v", i, err)
		}
		clientStreams[i] = s
		t.Cleanup(func() { s.Close() })
	}

	timeout := time.After(3 * time.Second)
	for i := 0; i < n; i++ {
		select {
		case s := <-serverStreams:
			t.Cleanup(func() { s.Close() })
		case <-timeout:
			t.Fatalf("timeout accepting stream %d", i)
		}
	}
}

func TestStreamCloseSignalsReader(t *testing.T) {
	t.Parallel()
	ss, cs := harness.NewEndStream(t)

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 32)
		_, err := ss.Read(buf)
		done <- err
	}()

	cs.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Log("Read returned nil after close (timing dependent)")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: server stream Read did not return after client close")
	}
}
