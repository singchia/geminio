// Package unit contains fine-grained protocol-level tests for gemino.
// Each test focuses on a single behaviour of the conn / application layer.
package unit

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/singchia/gemino/client"
	"github.com/singchia/gemino/server"
	"github.com/singchia/gemino/test/harness"
)

// ─────────────────────────────────────────────
// Handshake / Accept
// ─────────────────────────────────────────────

// TestHandshakeBasic verifies that a client and server can complete the
// gemino handshake over a raw TCP connection.
func TestHandshakeBasic(t *testing.T) {
	t.Parallel()
	_, _ = harness.NewEndPair(t)
	// If NewEndPair returns without fatal, handshake succeeded.
}

// TestHandshakeWithMeta verifies that client-supplied metadata is visible on
// the server side after handshake.
func TestHandshakeWithMeta(t *testing.T) {
	t.Parallel()
	ln := harness.NewListener(t)

	meta := []byte("unit-test-meta-42")
	accepted := make(chan []byte, 1)

	go func() {
		end, err := ln.AcceptEnd()
		if err != nil {
			return
		}
		accepted <- end.Meta()
		end.Close()
	}()

	opt := client.NewEndOptions()
	opt.SetMeta(meta)
	cEnd, err := client.NewEnd("tcp", ln.Addr().String(), opt)
	if err != nil {
		t.Fatalf("NewEnd: %v", err)
	}
	defer cEnd.Close()

	select {
	case got := <-accepted:
		if string(got) != string(meta) {
			t.Errorf("meta mismatch: got %q, want %q", got, meta)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for server to accept")
	}
}

// TestHandshakeMultipleClients verifies that a single listener can accept
// several clients sequentially without state contamination.
func TestHandshakeMultipleClients(t *testing.T) {
	t.Parallel()
	ln := harness.NewListener(t)

	const n = 5
	done := make(chan struct{}, n)

	go func() {
		for i := 0; i < n; i++ {
			end, err := ln.AcceptEnd()
			if err != nil {
				return
			}
			go func(e interface{ Close() error }) {
				e.Close()
				done <- struct{}{}
			}(end)
		}
	}()

	for i := 0; i < n; i++ {
		cEnd, err := client.NewEnd("tcp", ln.Addr().String())
		if err != nil {
			t.Fatalf("client %d: NewEnd: %v", i, err)
		}
		t.Cleanup(func() { cEnd.Close() })
	}

	// 5 sequential handshakes shouldn't need more than a second, but CI
	// runners under race + coverage can stall well past that. Use a generous
	// bound so this doesn't masquerade as a genuine failure.
	timeout := time.After(30 * time.Second)
	for i := 0; i < n; i++ {
		select {
		case <-done:
		case <-timeout:
			t.Fatalf("timeout: only %d/%d accepted", i, n)
		}
	}
}

// ─────────────────────────────────────────────
// Close semantics
// ─────────────────────────────────────────────

// TestCloseClient verifies that closing the client end propagates to the server
// (server Receive returns an error).
func TestCloseClient(t *testing.T) {
	t.Parallel()
	sEnd, cEnd := harness.NewEndPair(t)

	done := make(chan error, 1)
	go func() {
		_, err := sEnd.Receive(context.Background())
		done <- err
	}()

	cEnd.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error on server Receive after client close, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: server did not detect client close")
	}
}

// TestCloseServer verifies that closing the server end propagates to the client.
func TestCloseServer(t *testing.T) {
	t.Parallel()
	sEnd, cEnd := harness.NewEndPair(t)

	done := make(chan error, 1)
	go func() {
		_, err := cEnd.Receive(context.Background())
		done <- err
	}()

	sEnd.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error on client Receive after server close, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: client did not detect server close")
	}
}

// TestDoubleClose ensures calling Close twice does not panic.
func TestDoubleClose(t *testing.T) {
	t.Parallel()
	sEnd, cEnd := harness.NewEndPair(t)
	cEnd.Close()
	cEnd.Close() // must not panic
	sEnd.Close()
	sEnd.Close() // must not panic
}

// ─────────────────────────────────────────────
// Listener
// ─────────────────────────────────────────────

// TestListenerAcceptAfterClose verifies AcceptEnd returns an error after the
// listener is closed.
func TestListenerAcceptAfterClose(t *testing.T) {
	t.Parallel()
	ln, err := server.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := ln.AcceptEnd()
		done <- err
	}()

	ln.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error from AcceptEnd after Close, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: AcceptEnd did not return after listener Close")
	}
}

// TestListenerRandomPort verifies that Listen(":0") gives a non-zero port.
func TestListenerRandomPort(t *testing.T) {
	t.Parallel()
	ln, err := server.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	if addr.Port == 0 {
		t.Error("expected non-zero port from Listen(:0)")
	}
}
