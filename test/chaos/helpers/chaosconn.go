// Package helpers provides primitives for chaos testing gemino: a net.Conn
// wrapper that can drop, delay, corrupt, or kill traffic at runtime, plus
// glue to spin up an End pair over chaosConn pipes.
package helpers

import (
	"io"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/singchia/gemino"
	"github.com/singchia/gemino/client"
	"github.com/singchia/gemino/server"
)

// ChaosDir identifies a direction of traffic.
type ChaosDir int

const (
	DirRead  ChaosDir = 1 << 0
	DirWrite ChaosDir = 1 << 1
	DirBoth  ChaosDir = DirRead | DirWrite
)

// ChaosConn wraps a net.Conn so traffic can be sabotaged at runtime. Every
// knob is atomic so tests can flip chaos on/off in flight without locking.
type ChaosConn struct {
	net.Conn

	// dropRate / corruptRate / stopped are stored as fixed-point (×1000) so
	// atomic.LoadInt32 is safe and lock-free.
	dropRate     atomic.Int32 // 0–1000, packets dropped per 1000
	corruptRate  atomic.Int32 // 0–1000, bytes bit-flipped per 1000
	writeDelayNs atomic.Int64
	readDelayNs  atomic.Int64

	killed atomic.Bool // Kill() slams the underlying conn
	halved atomic.Int32

	rng    *rand.Rand
	rngMtx sync.Mutex
}

// WrapConn wraps c with chaos controls. Use a non-zero seed so two wrappers
// can be paired deterministically.
func WrapConn(c net.Conn, seed int64) *ChaosConn {
	return &ChaosConn{
		Conn: c,
		rng:  rand.New(rand.NewSource(seed)),
	}
}

// SetDropRate in 0..1000 (permille).
func (c *ChaosConn) SetDropRate(permille int32) { c.dropRate.Store(permille) }

// SetCorruptRate in 0..1000 (permille, per read byte).
func (c *ChaosConn) SetCorruptRate(permille int32) { c.corruptRate.Store(permille) }

// SetWriteDelay sets an artificial delay per Write call.
func (c *ChaosConn) SetWriteDelay(d time.Duration) { c.writeDelayNs.Store(int64(d)) }

// SetReadDelay sets an artificial delay per Read call.
func (c *ChaosConn) SetReadDelay(d time.Duration) { c.readDelayNs.Store(int64(d)) }

// HalfClose disables traffic in a direction, simulating a half-open link.
func (c *ChaosConn) HalfClose(dir ChaosDir) { c.halved.Store(int32(dir)) }

// Blackhole drops everything both ways while leaving the socket "alive" —
// the far side's heartbeat should eventually notice.
func (c *ChaosConn) Blackhole() {
	c.halved.Store(int32(DirBoth))
}

// Kill slams the underlying connection. Any pending Read/Write returns an
// error on the next call. Emulates peer kill -9 from the far side.
func (c *ChaosConn) Kill() {
	if c.killed.CompareAndSwap(false, true) {
		_ = c.Conn.Close()
	}
}

func (c *ChaosConn) maybeRand() *rand.Rand {
	c.rngMtx.Lock()
	r := c.rng
	c.rngMtx.Unlock()
	return r
}

func (c *ChaosConn) rollPermille(permille int32) bool {
	if permille <= 0 {
		return false
	}
	c.rngMtx.Lock()
	defer c.rngMtx.Unlock()
	return c.rng.Int31n(1000) < permille
}

func (c *ChaosConn) Read(b []byte) (int, error) {
	if c.killed.Load() {
		return 0, io.EOF
	}
	if int32(DirRead)&c.halved.Load() != 0 {
		// Read direction silenced: block for a long time OR return EOF? EOF
		// lets the caller unblock and observe the half-close. We pick a
		// long blocking read that ends when the underlying conn is closed
		// (tests always close the wrapped conn on teardown).
		time.Sleep(50 * time.Millisecond)
		return 0, io.EOF
	}
	if d := time.Duration(c.readDelayNs.Load()); d > 0 {
		time.Sleep(d)
	}
	n, err := c.Conn.Read(b)
	if err != nil {
		return n, err
	}
	if c.rollPermille(c.dropRate.Load()) {
		// Simulate data-on-the-wire being lost by returning zero bytes.
		// gemino's decoder uses io.ReadFull internally, so a short read
		// just leads to another Read; we instead zero them so higher
		// layers see junk and the connection resets. Tests decide which
		// behaviour they want by how they compose this.
		for i := range b[:n] {
			b[i] = 0
		}
	}
	if rate := c.corruptRate.Load(); rate > 0 {
		r := c.maybeRand()
		c.rngMtx.Lock()
		for i := 0; i < n; i++ {
			if r.Int31n(1000) < rate {
				b[i] ^= 1 << uint(r.Intn(8))
			}
		}
		c.rngMtx.Unlock()
	}
	return n, nil
}

func (c *ChaosConn) Write(b []byte) (int, error) {
	if c.killed.Load() {
		return 0, io.ErrClosedPipe
	}
	if int32(DirWrite)&c.halved.Load() != 0 {
		// Writes silently succeed but do not reach the peer.
		return len(b), nil
	}
	if d := time.Duration(c.writeDelayNs.Load()); d > 0 {
		time.Sleep(d)
	}
	if c.rollPermille(c.dropRate.Load()) {
		return len(b), nil // fake success, peer never sees it
	}
	return c.Conn.Write(b)
}

func (c *ChaosConn) Close() error { return c.Conn.Close() }

// ─────────────────────────────────────────────
// End-pair constructors over chaos-wrapped conns
// ─────────────────────────────────────────────

// NewChaosEndPair returns a server/client End pair connected through
// ChaosConns the caller can sabotage at runtime. Both ends are closed by
// t.Cleanup in the normal order (client first, then server).
func NewChaosEndPair(t testing.TB) (sEnd, cEnd gemino.End, sChaos, cChaos *ChaosConn) {
	t.Helper()
	sConn, cConn := rawTCPPair(t)

	sChaos = WrapConn(sConn, 1)
	cChaos = WrapConn(cConn, 2)

	type result struct {
		end gemino.End
		err error
	}
	ch := make(chan result, 1)
	go func() {
		e, err := server.NewEndWithConn(sChaos)
		ch <- result{e, err}
	}()

	dialer := func() (net.Conn, error) { return cChaos, nil }
	var err error
	cEnd, err = client.NewEndWithDialer(dialer)
	if err != nil {
		t.Fatalf("chaos: client.NewEndWithDialer: %v", err)
	}
	res := <-ch
	if res.err != nil {
		cEnd.Close()
		t.Fatalf("chaos: server.NewEndWithConn: %v", res.err)
	}
	sEnd = res.end

	t.Cleanup(func() {
		cEnd.Close()
		sEnd.Close()
	})
	return sEnd, cEnd, sChaos, cChaos
}

func rawTCPPair(t testing.TB) (net.Conn, net.Conn) {
	t.Helper()
	lst, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("chaos: net.Listen: %v", err)
	}
	defer lst.Close()

	ch := make(chan net.Conn, 1)
	go func() {
		c, err := lst.Accept()
		if err != nil {
			return
		}
		ch <- c
	}()
	cConn, err := net.Dial("tcp", lst.Addr().String())
	if err != nil {
		t.Fatalf("chaos: net.Dial: %v", err)
	}
	sConn := <-ch
	return sConn, cConn
}
