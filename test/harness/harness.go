// Package harness provides reusable test helpers for gemino tests.
//
// All constructors accept *testing.T (or *testing.B) and register cleanup
// via t.Cleanup, so callers never need manual defer/Close boilerplate.
//
// Usage:
//
//	func TestFoo(t *testing.T) {
//	    sEnd, cEnd := harness.NewEndPair(t)
//	    // use sEnd, cEnd; they are closed automatically when t finishes
//	}
package harness

import (
	"net"
	"testing"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/gemino"
	"github.com/singchia/gemino/client"
	"github.com/singchia/gemino/server"
)

// TB is the common interface satisfied by *testing.T and *testing.B.
type TB interface {
	testing.TB
}

// ─────────────────────────────────────────────
// Network-layer helpers
// ─────────────────────────────────────────────

// NewListener starts a TCP listener on a random port and registers
// t.Cleanup(ln.Close). Callers use ln.Addr().String() for the address.
func NewListener(t TB) server.Listener {
	t.Helper()
	ln, err := server.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("harness: server.Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

// NewConnPair returns a pair of connected net.Conn values (server-side first)
// over a random local TCP port. Both connections are closed via t.Cleanup.
func NewConnPair(t TB) (sConn net.Conn, cConn net.Conn) {
	t.Helper()
	sConn, cConn = newConnPairRaw(t)
	t.Cleanup(func() { sConn.Close() })
	t.Cleanup(func() { cConn.Close() })
	return sConn, cConn
}

// newConnPairRaw returns a connected pair without registering any cleanup.
// Used internally by NewEndPair so that End.Close() fully owns the lifecycle.
func newConnPairRaw(t TB) (sConn net.Conn, cConn net.Conn) {
	t.Helper()
	lst, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("harness: net.Listen: %v", err)
	}
	defer lst.Close()

	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		c, e := lst.Accept()
		ch <- result{c, e}
	}()

	cConn, err = net.Dial("tcp", lst.Addr().String())
	if err != nil {
		t.Fatalf("harness: net.Dial: %v", err)
	}

	res := <-ch
	if res.err != nil {
		cConn.Close()
		t.Fatalf("harness: Accept: %v", res.err)
	}
	return res.conn, cConn
}

// ─────────────────────────────────────────────
// End-layer helpers
// ─────────────────────────────────────────────

// NewEndPair creates a matched server/client End pair over a random TCP port.
// Both ends are closed automatically when t finishes. The client is closed
// first, then the server, to follow the expected disconnect handshake order.
func NewEndPair(t TB) (sEnd gemino.End, cEnd gemino.End) {
	t.Helper()
	sConn, cConn := newConnPairRaw(t)

	type result struct {
		end gemino.End
		err error
	}
	ch := make(chan result, 1)
	go func() {
		e, err := server.NewEndWithConn(sConn)
		ch <- result{e, err}
	}()

	dialer := func() (net.Conn, error) { return cConn, nil }
	cEnd, err := client.NewEndWithDialer(dialer)
	if err != nil {
		t.Fatalf("harness: client.NewEndWithDialer: %v", err)
	}

	res := <-ch
	if res.err != nil {
		cEnd.Close()
		t.Fatalf("harness: server.NewEndWithConn: %v", res.err)
	}

	// Register a single cleanup that closes client first, then server.
	// This order matches the normal disconnect handshake and avoids the
	// concurrent-close nil-pointer race in the conn layer.
	t.Cleanup(func() {
		cEnd.Close()
		res.end.Close()
	})
	return res.end, cEnd
}

// NewEndStream creates a matched stream pair (server-side, client-side) by
// building an End pair first and negotiating one stream. All resources are
// closed via t.Cleanup.
func NewEndStream(t TB) (sStream gemino.Stream, cStream gemino.Stream) {
	t.Helper()
	sEnd, cEnd := NewEndPair(t)

	type result struct {
		s   gemino.Stream
		err error
	}
	ch := make(chan result, 1)
	go func() {
		s, err := sEnd.AcceptStream()
		ch <- result{s, err}
	}()

	cs, err := cEnd.OpenStream()
	if err != nil {
		t.Fatalf("harness: cEnd.OpenStream: %v", err)
	}

	res := <-ch
	if res.err != nil {
		cs.Close()
		t.Fatalf("harness: sEnd.AcceptStream: %v", res.err)
	}

	t.Cleanup(func() {
		cs.Close()
		res.s.Close()
	})
	return res.s, cs
}

// ─────────────────────────────────────────────
// Observability helpers
// ─────────────────────────────────────────────

// LogSilence suppresses gemino log output for the duration of the test and
// restores it afterwards. Call at the start of noisy tests.
func LogSilence(t TB) {
	t.Helper()
	log.SetLevel(log.LevelError)
	t.Cleanup(func() { log.SetLevel(log.LevelInfo) })
}
