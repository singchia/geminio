// Package chaos contains resilience tests that inject network failures and
// verify geminio recovers or fails gracefully.
//
// Unlike retry_linux_test.go (which uses iptables), the tests in this file
// use in-process techniques (conn interception, forced close, net.Pipe) that
// run on every OS.
package chaos

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/server"
	"github.com/singchia/geminio/test/harness"
)

// ─────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────

// ─────────────────────────────────────────────
// Abrupt disconnect
// ─────────────────────────────────────────────

// TestAbruptClientDisconnect verifies that the server End detects and recovers
// from a client TCP connection being forcefully terminated (no handshake).
func TestAbruptClientDisconnect(t *testing.T) {
	t.Parallel()
	ln, err := server.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	serverDone := make(chan error, 1)
	go func() {
		end, err := ln.AcceptEnd()
		if err != nil {
			serverDone <- err
			return
		}
		_, err = end.Receive(context.Background())
		serverDone <- err
		end.Close()
	}()

	// Connect then immediately close the raw TCP conn (no graceful disconnect)
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	// Give the server time to start the accept handshake, then rudely close.
	time.Sleep(200 * time.Millisecond)
	conn.Close()

	select {
	case err := <-serverDone:
		// Server should have gotten an error, not hung forever.
		t.Logf("server received (expected) error: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server blocked forever after abrupt client disconnect")
	}
}

// TestAbruptServerDisconnect verifies client End detects server TCP close.
func TestAbruptServerDisconnect(t *testing.T) {
	t.Parallel()
	ln, err := server.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	accepted := make(chan geminio.End, 1)
	go func() {
		end, err := ln.AcceptEnd()
		if err != nil {
			return
		}
		accepted <- end
	}()

	cEnd, err := client.NewEnd("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("client.NewEnd: %v", err)
	}
	t.Cleanup(func() { cEnd.Close() })

	sEnd := <-accepted

	clientDone := make(chan error, 1)
	go func() {
		_, err := cEnd.Receive(context.Background())
		clientDone <- err
	}()

	// Close listener + server end abruptly
	ln.Close()
	sEnd.Close()

	select {
	case err := <-clientDone:
		t.Logf("client received (expected) error: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("client blocked forever after abrupt server disconnect")
	}
}

// ─────────────────────────────────────────────
// Concurrent close race
// ─────────────────────────────────────────────

// TestConcurrentClose verifies that simultaneously closing both ends does not
// panic and both Close() calls return (possibly with errors).
func TestConcurrentClose(t *testing.T) {
	t.Parallel()
	for i := 0; i < 20; i++ {
		sEnd, cEnd := harness.NewEndPair(t)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); cEnd.Close() }()
		go func() { defer wg.Done(); sEnd.Close() }()
		wg.Wait()
	}
}

// TestConcurrentSendAndClose verifies that publishing while the End is being
// closed does not panic.
func TestConcurrentSendAndClose(t *testing.T) {
	t.Parallel()
	for i := 0; i < 10; i++ {
		sEnd, cEnd := harness.NewEndPair(t)

		// Drain server
		go func() {
			for {
				msg, err := sEnd.Receive(context.Background())
				if err != nil {
					return
				}
				msg.Done()
			}
		}()

		var wg sync.WaitGroup
		wg.Add(2)

		// sender
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				cEnd.Publish(context.Background(), cEnd.NewMessage([]byte("x")))
			}
		}()

		// closer
		go func() {
			defer wg.Done()
			time.Sleep(2 * time.Millisecond)
			cEnd.Close()
		}()

		wg.Wait()
		sEnd.Close()
	}
}

// ─────────────────────────────────────────────
// Slow reader (back-pressure)
// ─────────────────────────────────────────────

// TestSlowServerReceiver verifies the client does not hang indefinitely when
// the server is a slow consumer.
func TestSlowServerReceiver(t *testing.T) {
	t.Parallel()
	sEnd, cEnd := harness.NewEndPair(t)

	// Server reads one message then sleeps
	go func() {
		msg, err := sEnd.Receive(context.Background())
		if err != nil {
			return
		}
		msg.Done()
		time.Sleep(500 * time.Millisecond)
	}()

	// Client publishes a burst; some may back-pressure but must not deadlock.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sent := 0
	for sent < 20 {
		if ctx.Err() != nil {
			break
		}
		err := cEnd.Publish(ctx, cEnd.NewMessage(make([]byte, 1024)))
		if err != nil {
			break // back-pressure limit reached — acceptable
		}
		sent++
	}
	t.Logf("sent %d messages before back-pressure", sent)
}

// ─────────────────────────────────────────────
// Malformed / garbage connection
// ─────────────────────────────────────────────

// TestGarbageDataFromClient verifies the server does not crash or hang when a
// raw TCP client sends random garbage (no geminio handshake).
func TestGarbageDataFromClient(t *testing.T) {
	t.Parallel()
	ln, err := server.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	done := make(chan struct{}, 1)
	go func() {
		defer func() { done <- struct{}{} }()
		end, err := ln.AcceptEnd()
		if err != nil {
			return // expected: handshake fails on garbage
		}
		end.Close()
	}()

	// Send garbage
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	garbage := []byte{0xFF, 0xFE, 0x00, 0x01, 0xAB, 0xCD}
	conn.Write(garbage)
	conn.Close()

	select {
	case <-done:
		// Server AcceptEnd returned (either with end or error) — did not hang.
	case <-time.After(5 * time.Second):
		t.Fatal("server hung on garbage data from client")
	}
}

// ─────────────────────────────────────────────
// High-rate reconnect (stress listener)
// ─────────────────────────────────────────────

// TestHighRateReconnect verifies the listener can handle rapid connect/close
// cycles without leaking goroutines or panicking.
func TestHighRateReconnect(t *testing.T) {
	// Do NOT run in parallel: goroutine-leak check needs a stable baseline.
	harness.LogSilence(t)
	ln := harness.NewListener(t)

	var accepted int64
	go func() {
		for {
			end, err := ln.AcceptEnd()
			if err != nil {
				return
			}
			atomic.AddInt64(&accepted, 1)
			go func(e geminio.End) { e.Close() }(end)
		}
	}()

	// Warm up: let the listener goroutine settle before taking the snapshot.
	time.Sleep(50 * time.Millisecond)
	before := harness.TakeSnapshot()

	const rounds = 30
	for i := 0; i < rounds; i++ {
		cEnd, err := client.NewEnd("tcp", ln.Addr().String())
		if err != nil {
			t.Logf("round %d connect error (may be transient): %v", i, err)
			continue
		}
		cEnd.Close()
	}

	// Give goroutines time to exit after all connections are closed.
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 15)
	t.Logf("accepted %d connections", atomic.LoadInt64(&accepted))
}
