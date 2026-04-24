// Batch D — flow control chaos. Push the library against its write
// buffers, packet-size cap, and multi-stream ceilings. Any scenario
// where a slow or hostile peer can starve us or blow memory is a bug.
package chaos

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
	"github.com/singchia/geminio/test/chaos/helpers"
	"github.com/singchia/geminio/test/harness"
)

// D1 — server never Receive()s but client keeps publishing. Client
// backpressure must eventually surface as Publish errors (or ctx
// timeout), never as memory growth or a panic. We bound Publish with a
// short ctx so the test itself does not hang on a buggy library.
func TestServerNeverReadsFlood(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, _ := helpers.NewChaosEndPair(t)
	// Do NOT call sEnd.Receive — the server is a deliberate slow consumer.

	// Fire messages until we get an error or reach a cap. Each Publish
	// gets a very short ctx so a buggy block would be visible.
	sent, failed := 0, 0
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		err := cEnd.Publish(ctx, cEnd.NewMessage(make([]byte, 4*1024)))
		cancel()
		if err != nil {
			failed++
		} else {
			sent++
		}
		if sent+failed >= 2000 {
			break
		}
	}
	if sent == 0 && failed == 0 {
		t.Fatal("no Publish attempts completed — test bug or deadlock")
	}
	// Either some succeed (server has buffer capacity) or many fail
	// (backpressure kicked in). Both are fine; what we disallow is a
	// hang, which is bounded by the outer deadline above.

	cEnd.Close()
	sEnd.Close()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// D2 — Write a payload that exceeds the stream's per-packet size cap in
// a single call. The library must return ErrPacketTooLarge instead of
// serialising a packet with a declared length > MaxDecodablePacketLen
// (which would itself be rejected by the peer's decoder per the fix in
// packet/decode.go).
func TestSingleStreamHugePayload(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, _ := helpers.NewChaosEndPair(t)

	s, err := cEnd.OpenStream()
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	_ = acceptStreamWithTimeout(t, sEnd, 2*time.Second)

	// 20 MiB is above the 10 MiB DefaultMaxPacketSize. Write() should
	// reject this up front rather than attempt to send it.
	huge := make([]byte, 20*1024*1024)
	_, err = s.Write(huge)
	if err == nil {
		t.Fatal("Write accepted a 20 MiB payload — expected ErrPacketTooLarge")
	}

	s.Close()
	cEnd.Close()
	sEnd.Close()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}

// D3 — open a moderate number of streams concurrently (1k takes tens of
// seconds on macOS due to per-stream goroutines; we use 200 for a
// faster signal that still verifies goroutine accounting under load).
func TestManyConcurrentStreams(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, _ := helpers.NewChaosEndPair(t)

	// Consumer: accept-and-drain every stream the peer opens.
	stop := make(chan struct{})
	var consumerWG sync.WaitGroup
	consumerWG.Add(1)
	go func() {
		defer consumerWG.Done()
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
				consumerWG.Add(1)
				go func(s geminio.Stream) {
					defer consumerWG.Done()
					io.Copy(io.Discard, s)
				}(res.s)
			case <-stop:
				return
			}
		}
	}()

	const n = 200
	var wg sync.WaitGroup
	wg.Add(n)
	var opened atomic.Int64
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			s, err := cEnd.OpenStream()
			if err != nil {
				return
			}
			opened.Add(1)
			s.Write([]byte("pong"))
			s.Close()
		}()
	}
	wg.Wait()

	if got := opened.Load(); got < n/2 {
		t.Fatalf("only %d/%d streams opened — library rejected too many", got, n)
	}

	close(stop)
	cEnd.Close()
	sEnd.Close()
	consumerWG.Wait()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 30)
}

// D4 — transfer 4 MiB over one stream with a slow reader on the other
// end. The writer must not lose bytes; final payload integrity (md5 /
// byte-for-byte match) is asserted at the end.
func TestSlowReaderIntegrity(t *testing.T) {
	harness.LogSilence(t)
	before := harness.TakeSnapshot()

	sEnd, cEnd, _, _ := helpers.NewChaosEndPair(t)

	payload := make([]byte, 4*1024*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand: %v", err)
	}

	writerDone := make(chan error, 1)
	go func() {
		s, err := sEnd.OpenStream()
		if err != nil {
			writerDone <- err
			return
		}
		defer s.Close()
		_, err = io.Copy(s, bytes.NewReader(payload))
		writerDone <- err
	}()

	cStream := acceptStreamWithTimeout(t, cEnd, 2*time.Second)
	buf := &bytes.Buffer{}
	// Slow reader: read in 4 KiB chunks with a tiny sleep between each.
	readerDone := make(chan error, 1)
	go func() {
		chunk := make([]byte, 4096)
		for {
			n, err := cStream.Read(chunk)
			if n > 0 {
				buf.Write(chunk[:n])
			}
			if err != nil {
				readerDone <- err
				return
			}
			time.Sleep(50 * time.Microsecond)
		}
	}()

	select {
	case <-readerDone:
	case <-time.After(30 * time.Second):
		t.Fatal("slow reader did not complete within 30s")
	}
	<-writerDone
	if !bytes.Equal(buf.Bytes(), payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", buf.Len(), len(payload))
	}

	cEnd.Close()
	sEnd.Close()
	time.Sleep(500 * time.Millisecond)
	harness.AssertNoLeak(t, before, 20)
}
