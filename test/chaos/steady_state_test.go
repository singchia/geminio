// Batch G — steady-state soak tests. Each pushes enough work through
// the library to show whether goroutine counts or heap usage creep over
// time. They are gated on -short so CI keeps moving; nightly runs pick
// them up by running go test without -short.
package chaos

import (
	"context"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/singchia/gemino"
	"github.com/singchia/gemino/test/chaos/helpers"
	"github.com/singchia/gemino/test/harness"
)

// gcAndSnapshot forces two collection cycles so any unreferenced
// goroutine and heap memory has a fair chance to release before we
// measure.
func gcAndSnapshot() (goroutines int, heapBytes uint64) {
	runtime.GC()
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return runtime.NumGoroutine(), m.HeapAlloc
}

// G1 — open-write-close 10 000 streams back-to-back, with one consumer
// draining the peer end. At the end, goroutine count must be close to
// the baseline and heap must not have grown dramatically.
func TestGoroutineStableOverManyStreams(t *testing.T) {
	if testing.Short() {
		t.Skip("soak test; gated by -short")
	}
	harness.LogSilence(t)

	sEnd, cEnd, _, _ := helpers.NewChaosEndPair(t)

	// Drain every accepted stream.
	stop := make(chan struct{})
	var drain sync.WaitGroup
	drain.Add(1)
	go func() {
		defer drain.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			type r struct {
				s   gemino.Stream
				err error
			}
			ch := make(chan r, 1)
			go func() {
				s, err := sEnd.AcceptStream()
				ch <- r{s, err}
			}()
			select {
			case res := <-ch:
				if res.err != nil {
					return
				}
				drain.Add(1)
				go func(s gemino.Stream) {
					defer drain.Done()
					io.Copy(io.Discard, s)
				}(res.s)
			case <-stop:
				return
			}
		}
	}()

	// Warm up a few streams first so any one-shot init goroutines are
	// already alive when we snapshot the baseline.
	for i := 0; i < 5; i++ {
		s, _ := cEnd.OpenStream()
		s.Write([]byte("warm"))
		s.Close()
	}
	time.Sleep(200 * time.Millisecond)
	baselineG, baselineH := gcAndSnapshot()

	const streams = 10_000
	for i := 0; i < streams; i++ {
		s, err := cEnd.OpenStream()
		if err != nil {
			t.Fatalf("OpenStream %d: %v", i, err)
		}
		s.Write([]byte("soak"))
		s.Close()
	}

	// Give the consumer and the dismiss handshakes time to drain.
	time.Sleep(1 * time.Second)
	finalG, finalH := gcAndSnapshot()

	if finalG > baselineG+30 {
		t.Errorf("goroutine leak: baseline=%d final=%d over %d streams",
			baselineG, finalG, streams)
	}
	// Heap: allow 25 MiB growth — arbitrary generous bound. Real leaks
	// scale with iterations and blow past this easily.
	if finalH > baselineH+25*1024*1024 {
		t.Errorf("heap grew %d bytes over %d streams (baseline %d, final %d)",
			finalH-baselineH, streams, baselineH, finalH)
	}

	close(stop)
	cEnd.Close()
	sEnd.Close()
	drain.Wait()
}

// G2 — publish 100 000 small messages under at-most-once semantics and
// verify the heap stays flat. Uses the default semantic (no ack
// required), which is the cheapest and most leak-prone code path.
func TestMemoryStableUnderMessageLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("soak test; gated by -short")
	}
	harness.LogSilence(t)

	sEnd, cEnd, _, _ := helpers.NewChaosEndPair(t)

	var received atomic.Int64
	go func() {
		for {
			msg, err := sEnd.Receive(context.Background())
			if err != nil {
				return
			}
			msg.Done()
			received.Add(1)
		}
	}()

	// Warmup.
	for i := 0; i < 100; i++ {
		_ = cEnd.Publish(context.Background(), cEnd.NewMessage([]byte("w")))
	}
	time.Sleep(200 * time.Millisecond)
	baselineG, baselineH := gcAndSnapshot()

	const msgs = 100_000
	for i := 0; i < msgs; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = cEnd.Publish(ctx, cEnd.NewMessage([]byte("soak")))
		cancel()
	}

	// Wait for consumer to drain.
	deadline := time.Now().Add(10 * time.Second)
	for received.Load() < int64(msgs+100) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	finalG, finalH := gcAndSnapshot()

	if finalG > baselineG+30 {
		t.Errorf("goroutine leak: baseline=%d final=%d", baselineG, finalG)
	}
	if finalH > baselineH+50*1024*1024 {
		t.Errorf("heap grew %d bytes over %d messages", finalH-baselineH, msgs)
	}

	cEnd.Close()
	sEnd.Close()
}

// G3 — kill / reconnect the client End 20 times. Each cycle opens a
// fresh End over a fresh TCP conn. Goroutine count at the end must be
// steady.
func TestKillRestartPeerManyTimes(t *testing.T) {
	if testing.Short() {
		t.Skip("soak test; gated by -short")
	}
	harness.LogSilence(t)

	baselineG, _ := gcAndSnapshot()

	const cycles = 20
	for i := 0; i < cycles; i++ {
		sEnd, cEnd, _, cChaos := helpers.NewChaosEndPair(t)
		s, err := cEnd.OpenStream()
		if err != nil {
			t.Fatalf("cycle %d OpenStream: %v", i, err)
		}
		s.Write([]byte("hello"))
		cChaos.Kill()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); cEnd.Close() }()
		go func() { defer wg.Done(); sEnd.Close() }()
		wg.Wait()
	}

	time.Sleep(500 * time.Millisecond)
	finalG, _ := gcAndSnapshot()

	if finalG > baselineG+30 {
		t.Errorf("goroutine leak after %d kill cycles: baseline=%d final=%d",
			cycles, baselineG, finalG)
	}
}
