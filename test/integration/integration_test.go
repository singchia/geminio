// Package integration tests complete end-to-end flows spanning multiple
// protocol layers: conn → multiplexer → application → client/server End.
package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/test/harness"
)

// ─────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────

func mustNewEnd(t *testing.T, addr string) geminio.End {
	t.Helper()
	end, err := client.NewEnd("tcp", addr)
	if err != nil {
		t.Fatalf("client.NewEnd(%q): %v", addr, err)
	}
	t.Cleanup(func() { end.Close() })
	return end
}

func waitAccept(t *testing.T, ch <-chan geminio.End, d time.Duration) geminio.End {
	t.Helper()
	select {
	case end := <-ch:
		return end
	case <-time.After(d):
		t.Fatalf("timeout waiting for AcceptEnd")
		return nil
	}
}

// ─────────────────────────────────────────────
// Mixed: RPC + Message + Stream on same pair
// ─────────────────────────────────────────────

// TestMixedRPCAndMessage verifies that concurrent RPC calls and message
// publishes share one End pair without interfering.
func TestMixedRPCAndMessage(t *testing.T) {
	t.Parallel()
	harness.LogSilence(t)
	sEnd, cEnd := harness.NewEndPair(t)

	sEnd.Register(context.Background(), "echo", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData(req.Data())
	})
	go func() {
		for {
			msg, err := sEnd.Receive(context.Background())
			if err != nil {
				return
			}
			msg.Done()
		}
	}()

	const ops = 30
	var wg sync.WaitGroup

	for i := 0; i < ops; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			data := fmt.Sprintf("rpc-%d", id)
			resp, err := cEnd.Call(context.Background(), "echo", cEnd.NewRequest([]byte(data)))
			if err != nil {
				t.Errorf("RPC %d: %v", id, err)
				return
			}
			if string(resp.Data()) != data {
				t.Errorf("RPC %d mismatch: got %q", id, resp.Data())
			}
		}(i)
	}

	for i := 0; i < ops; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			payload := fmt.Appendf(nil, "msg-%d", id)
			if err := cEnd.Publish(context.Background(), cEnd.NewMessage(payload)); err != nil {
				t.Errorf("Publish %d: %v", id, err)
			}
		}(i)
	}

	wg.Wait()
}

// TestMixedStreamsAndRPC verifies multiple streams and concurrent RPC coexist.
func TestMixedStreamsAndRPC(t *testing.T) {
	t.Parallel()
	harness.LogSilence(t)
	sEnd, cEnd := harness.NewEndPair(t)

	sEnd.Register(context.Background(), "ping", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData([]byte("pong"))
	})

	const numStreams = 5
	type spair struct{ s, c geminio.Stream }
	pairs := make([]spair, numStreams)

	accepted := make(chan geminio.Stream, numStreams)
	go func() {
		for i := 0; i < numStreams; i++ {
			s, err := sEnd.AcceptStream()
			if err != nil {
				return
			}
			accepted <- s
		}
	}()

	for i := 0; i < numStreams; i++ {
		cs, err := cEnd.OpenStream()
		if err != nil {
			t.Fatalf("OpenStream %d: %v", i, err)
		}
		pairs[i].c = cs
		t.Cleanup(func() { cs.Close() })
	}

	timeout := time.After(3 * time.Second)
	for i := 0; i < numStreams; i++ {
		select {
		case s := <-accepted:
			pairs[i].s = s
			t.Cleanup(func() { s.Close() })
		case <-timeout:
			t.Fatalf("timeout accepting stream %d", i)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < numStreams; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			msg := fmt.Sprintf("stream-%d", idx)
			if _, err := pairs[idx].c.Write([]byte(msg)); err != nil {
				t.Errorf("stream %d write: %v", idx, err)
				return
			}
			buf := make([]byte, len(msg))
			n, err := pairs[idx].s.Read(buf)
			if err != nil {
				t.Errorf("stream %d read: %v", idx, err)
				return
			}
			if string(buf[:n]) != msg {
				t.Errorf("stream %d mismatch: got %q", idx, buf[:n])
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := cEnd.Call(context.Background(), "ping", cEnd.NewRequest(nil))
			if err != nil {
				t.Errorf("concurrent RPC: %v", err)
				return
			}
			if string(resp.Data()) != "pong" {
				t.Errorf("RPC bad response: %q", resp.Data())
			}
		}()
	}
	wg.Wait()
}

// ─────────────────────────────────────────────
// Multi-client to single server
// ─────────────────────────────────────────────

// TestMultipleClientsOneServer verifies that a listener handles concurrent
// clients, each doing independent RPC.
func TestMultipleClientsOneServer(t *testing.T) {
	t.Parallel()
	harness.LogSilence(t)
	ln := harness.NewListener(t)

	go func() {
		for {
			end, err := ln.AcceptEnd()
			if err != nil {
				return
			}
			go func(e geminio.End) {
				defer e.Close()
				e.Register(context.Background(), "echo", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
					resp.SetData(req.Data())
				})
				// drain messages to keep end alive
				for {
					_, err := e.Receive(context.Background())
					if err != nil {
						return
					}
				}
			}(end)
		}
	}()

	const numClients = 10
	var wg sync.WaitGroup
	errs := make(chan error, numClients)

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			payload := fmt.Sprintf("client-%d", id)
			// Wait until the server has registered "echo" on this connection.
			// Without this, the client may Call before the registration packet
			// has been processed and the peer replies with "no such rpc".
			opts := client.NewEndOptions()
			opts.SetWaitRemoteRPCs("echo")
			cEnd, err := client.NewEnd("tcp", ln.Addr().String(), opts)
			if err != nil {
				errs <- fmt.Errorf("client %d dial: %v", id, err)
				return
			}
			t.Cleanup(func() { cEnd.Close() })
			resp, err := cEnd.Call(context.Background(), "echo", cEnd.NewRequest([]byte(payload)))
			if err != nil {
				errs <- fmt.Errorf("client %d call: %v", id, err)
				return
			}
			if string(resp.Data()) != payload {
				errs <- fmt.Errorf("client %d mismatch: got %q", id, resp.Data())
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// ─────────────────────────────────────────────
// Reconnect
// ─────────────────────────────���───────────────

// TestReconnectAfterClose verifies a second client can connect to the same
// listener after the first session is fully closed.
func TestReconnectAfterClose(t *testing.T) {
	t.Parallel()
	ln := harness.NewListener(t)
	addr := ln.Addr().String()

	accepted := make(chan geminio.End, 4)
	go func() {
		for {
			end, err := ln.AcceptEnd()
			if err != nil {
				return
			}
			accepted <- end
		}
	}()

	c1 := mustNewEnd(t, addr)
	sEnd1 := waitAccept(t, accepted, 2*time.Second)
	c1.Close()
	sEnd1.Close()
	time.Sleep(50 * time.Millisecond)

	c2 := mustNewEnd(t, addr)
	sEnd2 := waitAccept(t, accepted, 2*time.Second)
	defer c2.Close()
	defer sEnd2.Close()
}

// ─────────────────────────────────────────────
// Message ordering
// ─────────────────────────────────────────────

// TestMessageOrdering verifies messages arrive in the same order they were
// published (single-sender, single-receiver).
func TestMessageOrdering(t *testing.T) {
	t.Parallel()
	sEnd, cEnd := harness.NewEndPair(t)

	const n = 100
	received := make(chan string, n)

	go func() {
		for {
			msg, err := sEnd.Receive(context.Background())
			if err != nil {
				return
			}
			s := string(msg.Data())
			msg.Done()
			received <- s
		}
	}()

	for i := 0; i < n; i++ {
		payload := fmt.Sprintf("msg-%04d", i)
		if err := cEnd.Publish(context.Background(), cEnd.NewMessage([]byte(payload))); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}

	for i := 0; i < n; i++ {
		select {
		case got := <-received:
			want := fmt.Sprintf("msg-%04d", i)
			if got != want {
				t.Errorf("order mismatch at %d: got %q, want %q", i, got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout at message %d", i)
		}
	}
}

// ─────────────────────────────────────────────
// Bidirectional RPC (both sides register handlers)
// ─────────────────────────────────────────────

// TestBidirectionalRPC verifies that both the server-side and client-side End
// can register handlers and call each other.
func TestBidirectionalRPC(t *testing.T) {
	t.Parallel()
	sEnd, cEnd := harness.NewEndPair(t)

	sEnd.Register(context.Background(), "s-echo", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData(append([]byte("from-server:"), req.Data()...))
	})
	cEnd.Register(context.Background(), "c-echo", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData(append([]byte("from-client:"), req.Data()...))
	})

	// client calls server
	resp, err := cEnd.Call(context.Background(), "s-echo", cEnd.NewRequest([]byte("hi")))
	if err != nil {
		t.Fatalf("client→server call: %v", err)
	}
	if string(resp.Data()) != "from-server:hi" {
		t.Errorf("c→s: got %q", resp.Data())
	}

	// server calls client
	resp2, err := sEnd.Call(context.Background(), "c-echo", sEnd.NewRequest([]byte("hello")))
	if err != nil {
		t.Fatalf("server→client call: %v", err)
	}
	if string(resp2.Data()) != "from-client:hello" {
		t.Errorf("s→c: got %q", resp2.Data())
	}
}
