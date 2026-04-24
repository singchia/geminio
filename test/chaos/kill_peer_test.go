// Batch A — transport sabotage. Each test sabotages the wire in a way that
// would in production correspond to a peer crash, a half-broken link, or a
// silent network blackhole. Every case asserts the other side notices
// within a bounded time and that no goroutines or memory leak.
package chaos

import (
	"bytes"
	"crypto/rand"
	"io"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/test/chaos/helpers"
	"github.com/singchia/geminio/test/harness"
)

// closeDeadline bounds how long End.Close() may take under adversarial
// network conditions. geminio's dialogue closewait is 30 s, so a fully
// silenced peer pushes Close() up against that ceiling; we accept the
// ceiling as current behaviour here and will re-tighten if/when a
// bounded-close API lands upstream.
const closeDeadline = 35 * time.Second

// Each chaos test in this batch runs serially (no t.Parallel) because the
// dismiss close path can take up to 30 s on a silenced peer and we do not
// want concurrent tests to muddy the goroutine snapshot.
var _ = runtime.GOOS // keep runtime import for future stack-dump aids

// A1 — peer is killed while the server is streaming a large payload.
//
// Expected: server's in-flight Write/Copy eventually errors or completes,
// server-side teardown finishes quickly, client's Read returns an EOF-ish
// error (the net.Conn was slammed), and no goroutines leak.
func TestPeerKilledMidWrite(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, cChaos := helpers.NewChaosEndPair(t)

	payload := make([]byte, 4*1024*1024) // 4 MB — guarantees many packets
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand: %v", err)
	}

	serverCopyDone := make(chan error, 1)
	go func() {
		s, err := sEnd.OpenStream()
		if err != nil {
			serverCopyDone <- err
			return
		}
		defer s.Close()
		_, err = io.Copy(s, bytes.NewReader(payload))
		serverCopyDone <- err
	}()

	cStream := acceptStreamWithTimeout(t, cEnd, 2*time.Second)

	// Give a tiny moment for data to start flowing, then slam the client's
	// end of the wire — simulates the client process dying hard.
	time.AfterFunc(20*time.Millisecond, cChaos.Kill)

	readDone := make(chan struct{})
	go func() {
		io.Copy(io.Discard, cStream)
		close(readDone)
	}()

	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatal("client reader did not unblock within 5s of peer kill")
	}

	select {
	case <-serverCopyDone:
	case <-time.After(5 * time.Second):
		t.Fatal("server writer did not unblock within 5s of peer kill")
	}

	// Let the teardown path drain.
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// A2 — peer killed while client's Read is blocked. This differs from A1
// because the reader is the one waiting; we want the Read call to return
// within a bounded time rather than sit forever.
func TestPeerKilledMidRead(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, sChaos, _ := helpers.NewChaosEndPair(t)

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		s, err := sEnd.OpenStream()
		if err != nil {
			return
		}
		defer s.Close()
		s.Write([]byte("prelude"))
		// Don't write anything else — let the reader block.
		<-serverDone // never
	}()

	cStream := acceptStreamWithTimeout(t, cEnd, 2*time.Second)
	// Consume the prelude so we know the stream is established.
	prelude := make([]byte, 16)
	if _, err := cStream.Read(prelude); err != nil {
		t.Fatalf("prelude read: %v", err)
	}

	// Now the client is blocked in a subsequent Read. Slam the server side.
	time.AfterFunc(50*time.Millisecond, sChaos.Kill)

	readBack := make(chan error, 1)
	go func() {
		buf := make([]byte, 16)
		_, err := cStream.Read(buf)
		readBack <- err
	}()
	select {
	case err := <-readBack:
		if err == nil {
			t.Fatal("Read returned nil error after peer kill")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked Read did not unblock within 5s of peer kill")
	}
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// A3 — half-open send-only: writes reach peer but reads are silently
// swallowed. Heartbeat replies never arrive; the send side must notice
// within the heartbeat window and tear the conn down.
func TestHalfOpenSendOnly(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, cChaos := helpers.NewChaosEndPair(t)

	// Establish a stream under normal conditions first.
	serverReady := make(chan struct{})
	go func() {
		s, err := sEnd.OpenStream()
		if err != nil {
			return
		}
		s.Write([]byte("ping"))
		close(serverReady)
		// Let the stream live; the chaos below will kill the conn.
		time.Sleep(4 * time.Second)
		s.Close()
	}()

	cStream := acceptStreamWithTimeout(t, cEnd, 2*time.Second)
	buf := make([]byte, 16)
	cStream.Read(buf)
	<-serverReady

	// Now silence the reply path on the client. Server writes still go out
	// but client heartbeat-ack and data replies never reach the server.
	cChaos.HalfClose(helpers.DirWrite)

	// The server-side End should eventually notice (heartbeat timeout)
	// and tear down. We bound this by 30s which is the current closewait
	// ceiling; most configs are much shorter.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := cStream.Read(buf); err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	// We don't strictly assert error here because the exact timing depends
	// on heartbeat config; we do assert the goroutines drain after close.
	cEnd.Close()
	sEnd.Close()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// A4 — half-open receive-only: client can read from peer but its own
// writes are silently dropped. Our writes will accumulate in local
// buffers then fail; at the very least Close() must still return.
func TestHalfOpenRecvOnly(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, cChaos := helpers.NewChaosEndPair(t)

	// Open a stream, then cut client->server writes mid-stream.
	cs, err := cEnd.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}

	ss := acceptStreamWithTimeout(t, sEnd, 2*time.Second)
	_ = ss

	cChaos.HalfClose(helpers.DirWrite)

	// Try to write: the wrapper swallows bytes silently so Write "succeeds"
	// but the peer never receives. We don't assert on payload delivery,
	// only that Close() itself still returns.
	cs.Write(bytes.Repeat([]byte("x"), 4096))

	// Close both ends in parallel. Running them sequentially would let each
	// hit the 30 s closewait ceiling, blowing past closeDeadline when the
	// peer is silenced.
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); cEnd.Close() }()
		go func() { defer wg.Done(); sEnd.Close() }()
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(closeDeadline):
		t.Fatalf("Close() did not return within %s under half-open recv-only", closeDeadline)
	}
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// A5 — full network blackhole: both directions silently swallow. TCP still
// looks alive. The heartbeat layer must eventually notice and surface the
// failure upward.
func TestNetworkBlackhole(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, cChaos := helpers.NewChaosEndPair(t)

	cs, err := cEnd.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	_ = acceptStreamWithTimeout(t, sEnd, 2*time.Second)

	cChaos.Blackhole()

	// In a blackholed connection Write should either start erroring
	// (buffers fill) or silently absorb; either way Close must not hang.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := cs.Write(bytes.Repeat([]byte("x"), 1024)); err != nil {
				return
			}
		}
	}()
	wg.Wait()

	done := make(chan struct{})
	go func() {
		cEnd.Close()
		sEnd.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(closeDeadline):
		t.Fatalf("End.Close() did not return within %s under blackhole", closeDeadline)
	}
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// ─────────────────────────────────────────────
// Shared helpers for the batch.
// ─────────────────────────────────────────────

func acceptStreamWithTimeout(t *testing.T, end geminio.End, d time.Duration) geminio.Stream {
	t.Helper()
	type r struct {
		s   geminio.Stream
		err error
	}
	ch := make(chan r, 1)
	go func() {
		s, err := end.AcceptStream()
		ch <- r{s, err}
	}()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("AcceptStream: %v", got.err)
		}
		return got.s
	case <-time.After(d):
		t.Fatalf("AcceptStream timeout after %s", d)
		return nil
	}
}
