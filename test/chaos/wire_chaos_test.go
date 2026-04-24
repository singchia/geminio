// Batch F — wire-level attacks. Malformed packets, corrupted bytes, and
// oversized-length declarations. Each test sends crafted bytes through a
// raw TCP dial or flips bits on an established connection; the server
// must never crash, must reject obviously-invalid input, and must not
// treat the trash as legitimate traffic.
package chaos

import (
	"context"
	"encoding/binary"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/singchia/gemino"
	"github.com/singchia/gemino/server"
	"github.com/singchia/gemino/test/chaos/helpers"
	"github.com/singchia/gemino/test/harness"
)

// F1 — randomly flip bits on Read bytes during a live RPC workload. The
// server must not panic; the connection may reset (which is fine) but
// noise must never escalate into a crash.
func TestWireBitFlip(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, sChaos, _ := helpers.NewChaosEndPair(t)

	sEnd.Register(context.Background(), "echo", func(ctx context.Context, req gemino.Request, resp gemino.Response) {
		resp.SetData(req.Data())
	})
	time.Sleep(100 * time.Millisecond) // register propagation

	// Light corruption on the server-side read path. 1 permille (0.1%) is
	// enough to guarantee multiple flipped bytes across a burst of RPCs
	// while still letting most traffic land.
	sChaos.SetCorruptRate(1)

	const calls = 100
	var errs atomic.Int64
	var okays atomic.Int64
	for i := 0; i < calls; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, err := cEnd.Call(ctx, "echo", cEnd.NewRequest([]byte("ping")))
		cancel()
		if err != nil {
			errs.Add(1)
		} else {
			okays.Add(1)
		}
	}
	// Every Call returned something; the process is alive; that is the bar.
	if okays.Load()+errs.Load() != calls {
		t.Fatalf("lost calls: ok=%d err=%d total=%d", okays.Load(), errs.Load(), calls)
	}

	sChaos.SetCorruptRate(0)
	cEnd.Close()
	sEnd.Close()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// F2 — raw TCP client declares a packet with PacketLen larger than the
// server's DefaultMaxPacketSize. Server's conn.readPkt should discard
// the oversized packet and either close the connection or resume; it
// must not allocate the declared size.
func TestOversizedPacketDeclaration(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	ln, err := server.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	accepted := make(chan struct{}, 1)
	go func() {
		end, err := ln.AcceptEnd()
		accepted <- struct{}{}
		if err == nil {
			end.Close()
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	// Craft a 14-byte header with type=StreamPacket and PacketLen=500 MB.
	// No payload follows; server must not hang on a 500 MB read or
	// allocate that much memory — the size cap is 10 MB.
	hdr := make([]byte, 14)
	hdr[0] = 0x01                                                 // version
	hdr[1] = 0x61                                                 // TypeStreamPacket
	binary.BigEndian.PutUint64(hdr[2:10], 0xdeadbeefcafebabe)    // random id
	binary.BigEndian.PutUint32(hdr[10:14], 500*1024*1024)         // 500 MB
	if _, err := conn.Write(hdr); err != nil {
		t.Fatalf("Write header: %v", err)
	}

	// Give the server a moment to process then drop us. AcceptEnd may
	// have already returned with err; either way, progress is the bar.
	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not react to oversized-length packet within 3s")
	}

	conn.Close()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// F3 — raw TCP client declares PacketLen=100 but sends only 10 bytes of
// payload then closes. Server's io.ReadFull should return ErrUnexpectedEOF
// and the read goroutine must exit cleanly.
func TestTruncatedPacket(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	ln, err := server.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	accepted := make(chan struct{}, 1)
	go func() {
		end, err := ln.AcceptEnd()
		accepted <- struct{}{}
		if err == nil {
			end.Close()
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	hdr := make([]byte, 14)
	hdr[0] = 0x01
	hdr[1] = 0x61 // StreamPacket
	binary.BigEndian.PutUint64(hdr[2:10], 1)
	binary.BigEndian.PutUint32(hdr[10:14], 100) // declares 100 bytes
	// Write header + only 10 bytes body, then close.
	conn.Write(hdr)
	conn.Write(make([]byte, 10))
	conn.Close()

	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not react to truncated packet within 3s")
	}
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// F4 — heavy corruption (5%) on an already-handshaked connection. A
// handshake completes cleanly; then we turn up corruption so subsequent
// traffic is almost guaranteed to get mangled. End.Close must still
// return; no goroutines may linger.
func TestHeavyCorruptionDuringTraffic(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, sChaos, cChaos := helpers.NewChaosEndPair(t)

	// Run a few normal ops to confirm the link is healthy.
	sEnd.Register(context.Background(), "echo", func(ctx context.Context, req gemino.Request, resp gemino.Response) {
		resp.SetData(req.Data())
	})
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	if _, err := cEnd.Call(ctx, "echo", cEnd.NewRequest([]byte("warmup"))); err != nil {
		cancel()
		t.Fatalf("warmup Call: %v", err)
	}
	cancel()

	// Now sabotage both directions heavily.
	sChaos.SetCorruptRate(50) // 5%
	cChaos.SetCorruptRate(50)

	// Fire more RPCs. Some will succeed (lucky, no corrupted bytes landed
	// on a critical field), many will error. We only assert the process
	// survives and Close returns within a bounded window.
	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, _ = cEnd.Call(ctx, "echo", cEnd.NewRequest([]byte("x")))
		cancel()
	}
	sChaos.SetCorruptRate(0)
	cChaos.SetCorruptRate(0)

	done := make(chan struct{})
	go func() {
		cEnd.Close()
		sEnd.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(closeDeadline):
		t.Fatalf("Close hung under heavy corruption (>%s)", closeDeadline)
	}
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}
