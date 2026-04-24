// Batch H — timed kill schedules. Sabotage the peer at random moments
// relative to traffic state. Whatever order events fire in, End.Close
// must eventually return and goroutines must not leak.
package chaos

import (
	"context"
	"io"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/singchia/gemino"
	"github.com/singchia/gemino/test/chaos/helpers"
	"github.com/singchia/gemino/test/harness"
)

// H1 — run 20 iterations of: open stream, start streaming data, kill
// the client-side transport at a random point between 1 ms and 200 ms
// into the transfer. Each iteration must unblock both sides within a
// bounded window and leave no stream goroutines behind.
func TestRandomKillSchedule(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	// A fixed seed keeps the sequence deterministic so a failure can be
	// reproduced by re-running the same test.
	r := rand.New(rand.NewSource(1))

	const iterations = 20
	for i := 0; i < iterations; i++ {
		sEnd, cEnd, _, cChaos := helpers.NewChaosEndPair(t)

		writerDone := make(chan struct{})
		go func() {
			defer close(writerDone)
			s, err := sEnd.OpenStream()
			if err != nil {
				return
			}
			defer s.Close()
			// Use a 1 ms Write deadline so a stuck writer cannot mask the
			// teardown-signal path — any regression in the monitorStop
			// escape shows up as a test failure rather than as the
			// deadline workaround papering over it.
			payload := make([]byte, 64*1024)
			for {
				if _, err := s.Write(payload); err != nil {
					return
				}
			}
		}()
		cStream := acceptStreamWithTimeout(t, cEnd, 2*time.Second)

		readerDone := make(chan struct{})
		go func() {
			defer close(readerDone)
			io.Copy(io.Discard, cStream)
		}()

		// Kill at random time in [1, 200] ms.
		delay := time.Duration(1+r.Intn(200)) * time.Millisecond
		time.AfterFunc(delay, cChaos.Kill)

		select {
		case <-readerDone:
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: reader did not unblock within 5s of kill", i)
		}
		select {
		case <-writerDone:
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: writer did not unblock within 5s of kill", i)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); cEnd.Close() }()
		go func() { defer wg.Done(); sEnd.Close() }()
		wg.Wait()
	}

	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// H2 — stress 10 concurrent streams with 3 random concurrent kills.
// The intent: trip races between the scheduler that manages
// dialogue write ordering and the sudden teardown of a subset of
// dialogues while siblings are mid-write.
func TestConcurrentKillWithActiveStreams(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, cChaos := helpers.NewChaosEndPair(t)

	// Open 10 streams, each streaming data server→client.
	const streams = 10
	var serverWG sync.WaitGroup
	serverWG.Add(streams)
	for i := 0; i < streams; i++ {
		go func() {
			defer serverWG.Done()
			s, err := sEnd.OpenStream()
			if err != nil {
				return
			}
			defer s.Close()
			payload := make([]byte, 32*1024)
			for j := 0; j < 200; j++ {
				if _, err := s.Write(payload); err != nil {
					return
				}
			}
		}()
	}

	var clientWG sync.WaitGroup
	clientWG.Add(streams)
	for i := 0; i < streams; i++ {
		cs := acceptStreamWithTimeout(t, cEnd, 2*time.Second)
		go func(s gemino.Stream) {
			defer clientWG.Done()
			io.Copy(io.Discard, s)
		}(cs)
	}

	// Fire three delayed kills at non-deterministic points within the
	// active window.
	for _, d := range []time.Duration{25 * time.Millisecond, 80 * time.Millisecond, 150 * time.Millisecond} {
		time.AfterFunc(d, cChaos.Kill)
	}

	done := make(chan struct{})
	go func() {
		serverWG.Wait()
		clientWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("active streams did not drain within 10s of concurrent kills")
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); cEnd.Close() }()
	go func() { defer wg.Done(); sEnd.Close() }()
	wg.Wait()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// H3 — interleave dismiss handshakes, in-flight RPCs, and a transport
// kill. All three of these paths individually passed earlier batches;
// running them together stresses the state machine with the messiest
// concurrent input we can construct.
func TestCascadingFailures(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, cChaos := helpers.NewChaosEndPair(t)

	sEnd.Register(context.Background(), "sleep", func(ctx context.Context, req gemino.Request, resp gemino.Response) {
		<-ctx.Done()
		resp.SetError(ctx.Err())
	})
	time.Sleep(100 * time.Millisecond)

	// Path 1: five streams in dismiss.
	for i := 0; i < 5; i++ {
		s, err := cEnd.OpenStream()
		if err != nil {
			break
		}
		s.Write([]byte("bye"))
		go s.Close() // dismiss firing concurrently
	}

	// Path 2: 10 in-flight RPCs with a ctx that we will cancel below.
	callCtx, callCancel := context.WithCancel(context.Background())
	var callWG sync.WaitGroup
	var callReturns atomic.Int64
	for i := 0; i < 10; i++ {
		callWG.Add(1)
		go func() {
			defer callWG.Done()
			_, _ = cEnd.Call(callCtx, "sleep", cEnd.NewRequest(nil))
			callReturns.Add(1)
		}()
	}
	time.Sleep(50 * time.Millisecond) // let RPCs reach the server

	// Path 3: slam the transport.
	cChaos.Kill()

	callCancel()
	done := make(chan struct{})
	go func() {
		callWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("only %d/10 RPCs returned within 5s of cascading failure", callReturns.Load())
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); cEnd.Close() }()
	go func() { defer wg.Done(); sEnd.Close() }()
	wg.Wait()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 30)
}
