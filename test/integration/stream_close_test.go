// Tests that exercise the stream-level dismiss (4-way handshake) path and
// verify that after one side closes a stream, the peer's Read returns io.EOF
// within a short deadline.
//
// Before the fix in multiplexer/dialogue.go#handleInDismissPacket, the
// convergence case (prevState == DISMISS_HALF) queued the final DismissAck
// into writeInCh via a goroutine that then lost the race against the
// subsequent handlePkt FINI / ctx cancel; the peer's closewait timed out at
// 30s before EOF reached the reader.
package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/test/harness"
)

const (
	// streamCloseDeadline is how long we allow between the writer's Close and
	// the reader observing io.EOF. Should be well under 1s when fixed; 30s+
	// when buggy (bounded by the dialogue closewait timeout).
	streamCloseDeadline = 2 * time.Second
)

// readAllWithDeadline reads from r into a sink until EOF or deadline, then
// signals done with the number of bytes read and the final error.
func readAllWithDeadline(t *testing.T, r io.Reader, deadline time.Duration) (int, error) {
	t.Helper()
	type res struct {
		n   int
		err error
	}
	done := make(chan res, 1)
	go func() {
		buf := make([]byte, 8*1024)
		total := 0
		for {
			n, err := r.Read(buf)
			total += n
			if err != nil {
				done <- res{total, err}
				return
			}
		}
	}()
	select {
	case r := <-done:
		return r.n, r.err
	case <-time.After(deadline):
		return 0, context.DeadlineExceeded
	}
}

// acceptStream pulls the next stream off cEnd within d.
func acceptStream(t *testing.T, end geminio.End, d time.Duration) geminio.Stream {
	t.Helper()
	type res struct {
		s   geminio.Stream
		err error
	}
	ch := make(chan res, 1)
	go func() {
		s, err := end.AcceptStream()
		ch <- res{s, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("AcceptStream: %v", r.err)
		}
		return r.s
	case <-time.After(d):
		t.Fatalf("AcceptStream: timeout after %s", d)
		return nil
	}
}

// ─── Case 1: idiomatic demo path — server writes, stream.Close(), client reads to EOF.

func TestStreamClose_SmallPayloadEOF(t *testing.T) {
	t.Parallel()
	harness.LogSilence(t)
	sEnd, cEnd := harness.NewEndPair(t)

	payload := bytes.Repeat([]byte("x"), 8*1024)

	sDone := make(chan error, 1)
	go func() {
		sStream, err := sEnd.OpenStream()
		if err != nil {
			sDone <- err
			return
		}
		if _, err := sStream.Write(payload); err != nil {
			sDone <- err
			return
		}
		sDone <- sStream.Close()
	}()

	cStream := acceptStream(t, cEnd, 2*time.Second)
	n, err := readAllWithDeadline(t, cStream, streamCloseDeadline)
	if err != io.EOF {
		t.Fatalf("expected io.EOF within %s; got n=%d err=%v", streamCloseDeadline, n, err)
	}
	if n != len(payload) {
		t.Fatalf("byte count mismatch: got %d want %d", n, len(payload))
	}
	if err := <-sDone; err != nil {
		t.Fatalf("server side: %v", err)
	}
}

// ─── Case 2: matches the 60-second demo exactly — 1 MB via io.Copy.

func TestStreamClose_DemoFileTransfer(t *testing.T) {
	t.Parallel()
	harness.LogSilence(t)
	sEnd, cEnd := harness.NewEndPair(t)

	payload := make([]byte, 1024*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	sDone := make(chan error, 1)
	go func() {
		sStream, err := sEnd.OpenStream()
		if err != nil {
			sDone <- err
			return
		}
		defer sStream.Close()
		_, err = io.Copy(sStream, bytes.NewReader(payload))
		sDone <- err
	}()

	cStream := acceptStream(t, cEnd, 2*time.Second)

	received := make(chan []byte, 1)
	cErr := make(chan error, 1)
	go func() {
		buf := &bytes.Buffer{}
		_, err := io.Copy(buf, cStream)
		received <- buf.Bytes()
		cErr <- err
	}()

	select {
	case got := <-received:
		if err := <-cErr; err != nil {
			t.Fatalf("client io.Copy err: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), len(payload))
		}
	case <-time.After(streamCloseDeadline):
		t.Fatalf("io.Copy on client did not return within %s (bug: ~30s)", streamCloseDeadline)
	}

	if err := <-sDone; err != nil {
		t.Fatalf("server side: %v", err)
	}
}

// ─── Case 3: multiple concurrent streams, each closed by server.

func TestStreamClose_MultipleConcurrentStreams(t *testing.T) {
	t.Parallel()
	harness.LogSilence(t)
	sEnd, cEnd := harness.NewEndPair(t)

	const nStreams = 10
	const payloadLen = 4 * 1024

	sErr := make(chan error, nStreams)
	for i := 0; i < nStreams; i++ {
		i := i
		go func() {
			s, err := sEnd.OpenStream()
			if err != nil {
				sErr <- err
				return
			}
			payload := bytes.Repeat([]byte{byte('a' + i%26)}, payloadLen)
			if _, err := s.Write(payload); err != nil {
				sErr <- err
				return
			}
			sErr <- s.Close()
		}()
	}

	done := make(chan error, nStreams)
	for i := 0; i < nStreams; i++ {
		go func() {
			cs := acceptStream(t, cEnd, 2*time.Second)
			n, err := readAllWithDeadline(t, cs, streamCloseDeadline)
			if err != io.EOF {
				done <- err
				return
			}
			if n != payloadLen {
				done <- io.ErrUnexpectedEOF
				return
			}
			done <- nil
		}()
	}

	for i := 0; i < nStreams; i++ {
		if err := <-done; err != nil {
			t.Fatalf("client stream %d: %v", i, err)
		}
		if err := <-sErr; err != nil {
			t.Fatalf("server stream: %v", err)
		}
	}
}

// ─── Case 4: both sides call Close() ~simultaneously (simultaneous dismiss).

func TestStreamClose_Simultaneous(t *testing.T) {
	t.Parallel()
	harness.LogSilence(t)
	sEnd, cEnd := harness.NewEndPair(t)

	ready := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		s, err := sEnd.OpenStream()
		if err != nil {
			serverDone <- err
			return
		}
		if _, err := s.Write([]byte("hello")); err != nil {
			serverDone <- err
			return
		}
		<-ready
		serverDone <- s.Close()
	}()

	cs := acceptStream(t, cEnd, 2*time.Second)
	// drain first bytes so we know the stream is established
	buf := make([]byte, 16)
	if _, err := cs.Read(buf); err != nil {
		t.Fatalf("client Read initial: %v", err)
	}
	close(ready)

	// fire both Closes at the same time
	clientDone := make(chan error, 1)
	go func() { clientDone <- cs.Close() }()

	select {
	case err := <-clientDone:
		if err != nil {
			t.Fatalf("client Close err: %v", err)
		}
	case <-time.After(streamCloseDeadline):
		t.Fatal("client Close did not return within deadline")
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server Close err: %v", err)
		}
	case <-time.After(streamCloseDeadline):
		t.Fatal("server Close did not return within deadline")
	}
}

// ─── Case 5: closing one stream must not affect other live streams on the same End.

func TestStreamClose_OtherStreamsUnaffected(t *testing.T) {
	t.Parallel()
	harness.LogSilence(t)
	sEnd, cEnd := harness.NewEndPair(t)

	// open two streams
	sA, err := sEnd.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream A: %v", err)
	}
	sB, err := sEnd.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream B: %v", err)
	}

	// client pulls both
	cA := acceptStream(t, cEnd, 2*time.Second)
	cB := acceptStream(t, cEnd, 2*time.Second)

	// close stream A only
	if _, err := sA.Write([]byte("A done")); err != nil {
		t.Fatalf("sA.Write: %v", err)
	}
	if err := sA.Close(); err != nil {
		t.Fatalf("sA.Close: %v", err)
	}

	n, err := readAllWithDeadline(t, cA, streamCloseDeadline)
	if err != io.EOF {
		t.Fatalf("cA expected EOF, got n=%d err=%v", n, err)
	}

	// stream B should still work
	if _, err := sB.Write([]byte("B still alive")); err != nil {
		t.Fatalf("sB.Write after sA close: %v", err)
	}
	buf := make([]byte, 64)
	n2, err := cB.Read(buf)
	if err != nil {
		t.Fatalf("cB.Read after sA close: %v", err)
	}
	if string(buf[:n2]) != "B still alive" {
		t.Fatalf("cB read mismatch: %q", buf[:n2])
	}

	// close B too
	if err := sB.Close(); err != nil {
		t.Fatalf("sB.Close: %v", err)
	}
	if _, err := readAllWithDeadline(t, cB, streamCloseDeadline); err != io.EOF {
		t.Fatalf("cB final expected EOF, got %v", err)
	}
}

// ─── Case 6: rapid open / write / close loop — no 30-second bleeding across
// iterations, no accumulated timeouts, every stream hits EOF.

func TestStreamClose_RapidCycle(t *testing.T) {
	t.Parallel()
	harness.LogSilence(t)
	sEnd, cEnd := harness.NewEndPair(t)

	const iterations = 30
	const payload = "rapid"

	var wg sync.WaitGroup
	var clientErr atomic.Value

	// one consumer goroutine: accept each stream, read to EOF, discard
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			cs := acceptStream(t, cEnd, 2*time.Second)
			n, err := readAllWithDeadline(t, cs, streamCloseDeadline)
			if err != io.EOF {
				clientErr.Store(err)
				return
			}
			if n != len(payload) {
				clientErr.Store(io.ErrUnexpectedEOF)
				return
			}
		}
	}()

	start := time.Now()
	for i := 0; i < iterations; i++ {
		s, err := sEnd.OpenStream()
		if err != nil {
			t.Fatalf("OpenStream #%d: %v", i, err)
		}
		if _, err := s.Write([]byte(payload)); err != nil {
			t.Fatalf("Write #%d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i, err)
		}
	}
	wg.Wait()
	elapsed := time.Since(start)

	if v := clientErr.Load(); v != nil {
		t.Fatalf("client loop: %v", v)
	}
	// Each iteration must complete well under streamCloseDeadline; total should
	// scale roughly linearly. Allow a generous 2x margin.
	budget := time.Duration(iterations) * streamCloseDeadline
	if elapsed > budget {
		t.Fatalf("rapid cycle took %s (budget %s) — likely hit per-close timeout", elapsed, budget)
	}
}

// ─── Case 7: reader closes first (not writer). Peer's Read should also EOF.

func TestStreamClose_ReaderClosesFirst(t *testing.T) {
	t.Parallel()
	harness.LogSilence(t)
	sEnd, cEnd := harness.NewEndPair(t)

	sDone := make(chan error, 1)
	go func() {
		s, err := sEnd.OpenStream()
		if err != nil {
			sDone <- err
			return
		}
		// keep trying to write until it starts to fail
		deadline := time.Now().Add(streamCloseDeadline)
		for time.Now().Before(deadline) {
			_, err := s.Write([]byte("ping"))
			if err != nil {
				sDone <- nil // expected once reader closed
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		sDone <- io.ErrClosedPipe // never saw an error — unexpected
	}()

	cs := acceptStream(t, cEnd, 2*time.Second)
	buf := make([]byte, 16)
	if _, err := cs.Read(buf); err != nil {
		t.Fatalf("initial Read: %v", err)
	}
	if err := cs.Close(); err != nil {
		t.Fatalf("client Close: %v", err)
	}

	select {
	case err := <-sDone:
		if err != nil {
			t.Fatalf("server never saw close within %s: %v", streamCloseDeadline, err)
		}
	case <-time.After(streamCloseDeadline + time.Second):
		t.Fatal("server writer did not observe stream close within deadline")
	}
}

// ─── Case 8: Close() itself must return promptly (never blocks the caller).
//
// A caller that does `stream.Close()` synchronously must get control back
// within milliseconds, regardless of whether the peer has fini'd. Blocking
// here would ripple into defer chains and break common Go idioms.

func TestStreamClose_CloseReturnsFast(t *testing.T) {
	t.Parallel()
	harness.LogSilence(t)
	sEnd, cEnd := harness.NewEndPair(t)

	s, err := sEnd.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if _, err := s.Write([]byte("x")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Peer does NOT read anything — ensures we are not accidentally waiting
	// on the peer's handshake progress.
	_ = acceptStream(t, cEnd, 2*time.Second)

	done := make(chan time.Duration, 1)
	start := time.Now()
	go func() {
		_ = s.Close()
		done <- time.Since(start)
	}()

	select {
	case d := <-done:
		if d > 100*time.Millisecond {
			t.Fatalf("stream.Close() took %s (expected <100ms)", d)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("stream.Close() did not return within 500ms — blocking caller")
	}
}

// ─── Case 9: idle-stream close (opened but never written to) must not
// block and must not leak the stream goroutines.

func TestStreamClose_IdleStream(t *testing.T) {
	t.Parallel()
	harness.LogSilence(t)
	sEnd, cEnd := harness.NewEndPair(t)

	s, err := sEnd.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	_ = acceptStream(t, cEnd, 2*time.Second)

	done := make(chan struct{})
	go func() {
		_ = s.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("idle stream Close did not return within 500ms")
	}
}

// ─── Case 10: goroutine leak guard. Open/write/close many streams, then
// assert goroutine count has not blown up. Catches forgotten defers in
// the handlePkt / readPkt / writePkt trio and any goroutine spawned by
// the dismiss path that fails to exit.

func TestStreamClose_NoGoroutineLeak(t *testing.T) {
	t.Parallel()
	harness.LogSilence(t)
	sEnd, cEnd := harness.NewEndPair(t)

	// Warm up one round so any one-shot initialisation goroutines are already
	// spawned when we snapshot.
	{
		s, _ := sEnd.OpenStream()
		cs := acceptStream(t, cEnd, 2*time.Second)
		_, _ = s.Write([]byte("warm"))
		_ = s.Close()
		if _, err := readAllWithDeadline(t, cs, streamCloseDeadline); err != io.EOF {
			t.Fatalf("warmup: %v", err)
		}
	}
	// Let any late reapers finish.
	time.Sleep(200 * time.Millisecond)

	before := harness.TakeSnapshot()

	const cycles = 30
	for i := 0; i < cycles; i++ {
		s, err := sEnd.OpenStream()
		if err != nil {
			t.Fatalf("OpenStream %d: %v", i, err)
		}
		if _, err := s.Write([]byte("leak-probe")); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close %d: %v", i, err)
		}
		cs := acceptStream(t, cEnd, 2*time.Second)
		if _, err := readAllWithDeadline(t, cs, streamCloseDeadline); err != io.EOF {
			t.Fatalf("client %d: %v", i, err)
		}
	}

	// Tolerance covers timers / schedulers that may hold transient goroutines.
	harness.AssertNoLeak(t, before, 8)
}

// ─── Case 11: stress — many streams in parallel to provoke panic / race.
// Works as a smoke run for race detector in CI.

func TestStreamClose_ParallelStress(t *testing.T) {
	t.Parallel()
	harness.LogSilence(t)
	sEnd, cEnd := harness.NewEndPair(t)

	const workers = 8
	const perWorker = 20
	payload := []byte("stress")

	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker*2)

	// producers
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				s, err := sEnd.OpenStream()
				if err != nil {
					errs <- err
					return
				}
				if _, err := s.Write(payload); err != nil {
					errs <- err
					_ = s.Close()
					return
				}
				if err := s.Close(); err != nil {
					errs <- err
					return
				}
			}
		}()
	}

	// single accept loop driving many consumer goroutines
	consumerWG := sync.WaitGroup{}
	acceptStop := make(chan struct{})
	consumerWG.Add(1)
	go func() {
		defer consumerWG.Done()
		for {
			// short-deadline accept to detect shutdown
			type r struct {
				s   geminio.Stream
				err error
			}
			ch := make(chan r, 1)
			go func() {
				s, err := cEnd.AcceptStream()
				ch <- r{s, err}
			}()
			select {
			case got := <-ch:
				if got.err != nil {
					return
				}
				consumerWG.Add(1)
				go func(s geminio.Stream) {
					defer consumerWG.Done()
					n, err := readAllWithDeadline(t, s, 2*time.Second)
					if err != io.EOF {
						errs <- err
						return
					}
					if n != len(payload) {
						errs <- io.ErrUnexpectedEOF
					}
				}(got.s)
			case <-acceptStop:
				return
			}
		}
	}()

	wg.Wait()
	// Give the accept loop a beat to drain, then stop it.
	time.Sleep(200 * time.Millisecond)
	close(acceptStop)
	consumerWG.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("stress: %v", err)
	}
}
