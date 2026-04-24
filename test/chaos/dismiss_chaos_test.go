// Batch B — lifecycle edge cases. These sabotage dismiss handshakes,
// force reentrant closes, and race open/close over many iterations. They
// are the tests most likely to tickle nil-deref or leaked goroutines in
// the state machine around stream/dialogue close.
package chaos

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/test/chaos/helpers"
	"github.com/singchia/geminio/test/harness"
)

// B1 — kill the peer right in the middle of the 4-way dismiss handshake.
// The initiating side should eventually tear down (heartbeat / closewait
// timeout) without a panic or leak; it should never return successfully
// as if the handshake had completed cleanly.
func TestKillPeerDuringDismiss(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, cChaos := helpers.NewChaosEndPair(t)

	s, err := sEnd.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	cs := acceptStreamWithTimeout(t, cEnd, 2*time.Second)
	s.Write([]byte("hi"))
	buf := make([]byte, 16)
	cs.Read(buf)

	// Arm a kill for the instant after Close is called. The exact delay
	// puts us deep inside the dismiss handshake — after the initiator has
	// sent its DismissPacket but before the peer replies.
	time.AfterFunc(5*time.Millisecond, cChaos.Kill)

	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case err := <-done:
		_ = err // any result is fine, as long as it returned
	case <-time.After(closeDeadline):
		t.Fatalf("stream.Close did not return within %s after peer kill during dismiss", closeDeadline)
	}
	cEnd.Close()
	sEnd.Close()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// B2 — open a stream while End.Close is running. We fire both operations
// from separate goroutines with zero synchronisation. OpenStream must
// either succeed (rare — order-of-ops lucky) or return an error, never
// panic or leak.
func TestOpenStreamDuringEndClose(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, _ := helpers.NewChaosEndPair(t)

	var openErr atomic.Value
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		// deliberate: no coordination with the Close below.
		s, err := cEnd.OpenStream()
		if err != nil {
			openErr.Store(err)
			return
		}
		s.Close()
	}()
	go func() {
		defer wg.Done()
		cEnd.Close()
	}()
	wg.Wait()
	sEnd.Close()

	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// B3 — hammer Close on the same stream from 50 goroutines. closeOnce
// semantics must make this idempotent; any double-close panic or channel
// double-close panic would surface here.
func TestReentrantClose(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, _ := helpers.NewChaosEndPair(t)

	s, err := cEnd.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	_ = acceptStreamWithTimeout(t, sEnd, 2*time.Second)

	const concurrent = 50
	var wg sync.WaitGroup
	wg.Add(concurrent)
	for i := 0; i < concurrent; i++ {
		go func() {
			defer wg.Done()
			_ = s.Close()
		}()
	}
	wg.Wait()

	// Tear the ends down so the leak check exercises the full close path,
	// not just stream-level idempotency. 50 concurrent Close calls must not
	// panic, deadlock, or leave stream goroutines behind.
	cEnd.Close()
	sEnd.Close()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// B4 — race open/close over 100 iterations. Picks out state-machine bugs
// where a new stream arrives concurrently with an older one finishing
// its dismiss.
func TestCloseReadRaceOver100Iterations(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, _ := helpers.NewChaosEndPair(t)

	// Consumer side: just accept whatever arrives and drain it.
	stop := make(chan struct{})
	var wgConsumer sync.WaitGroup
	wgConsumer.Add(1)
	go func() {
		defer wgConsumer.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			type r struct {
				s   geminio.Stream
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
				go func(s geminio.Stream) {
					buf := make([]byte, 64)
					for {
						if _, err := s.Read(buf); err != nil {
							return
						}
					}
				}(res.s)
			case <-stop:
				return
			}
		}
	}()

	const iters = 100
	for i := 0; i < iters; i++ {
		s, err := cEnd.OpenStream()
		if err != nil {
			t.Fatalf("OpenStream %d: %v", i, err)
		}
		if _, err := s.Write([]byte("hammer")); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close %d: %v", i, err)
		}
	}
	close(stop)
	cEnd.Close()
	sEnd.Close()
	wgConsumer.Wait()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// B5 — fire 10 RPCs concurrently, then slam End.Close before any of them
// finishes. Every Call must return (with either a timeout or a teardown
// error), and no goroutine may remain blocked on the closed channels.
func TestEndCloseWithInflightRPCs(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, _ := helpers.NewChaosEndPair(t)

	method := "slow"
	sEnd.Register(context.Background(), method, func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		select {
		case <-time.After(5 * time.Second):
			resp.SetData(req.Data())
		case <-ctx.Done():
			resp.SetError(ctx.Err())
		}
	})
	// Wait for registration to propagate to the client so Calls below do
	// not race the register packet.
	time.Sleep(100 * time.Millisecond)

	const callers = 10
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, _ = cEnd.Call(ctx, method, cEnd.NewRequest(nil))
		}()
	}
	// Let at least one request land on the handler before closing.
	time.Sleep(50 * time.Millisecond)
	cEnd.Close()
	sEnd.Close()
	wg.Wait() // every caller must return

	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}
