package integration

import (
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/server"
	"github.com/singchia/geminio/test/harness"
	"io"
	"sync"
	"testing"
	"time"
)

func TestForceCloseAbandonedStream(t *testing.T) {
	t.Run("force_only", func(t *testing.T) { testForceCloseAbandonedStream(t, false) })
	t.Run("concurrent_close", func(t *testing.T) { testForceCloseAbandonedStream(t, true) })
}
func testForceCloseAbandonedStream(t *testing.T, normalClose bool) {
	harness.LogSilence(t)
	rawS, rawC := harness.NewConnPair(t)
	type result struct {
		end geminio.End
		err error
	}
	ready := make(chan result, 1)
	so := server.NewEndOptions()
	so.SetBufferSize(1, 1)
	co := client.NewEndOptions()
	co.SetBufferSize(1, 1)
	go func() { s, e := server.NewEndWithConn(rawS, so); ready <- result{s, e} }()
	c, e := client.NewEndWithConn(rawC, co)
	if e != nil {
		t.Fatal(e)
	}
	res := <-ready
	if res.err != nil {
		t.Fatal(res.err)
	}
	s := res.end
	// Break socket IO before library teardown, including on a regression failure.
	t.Cleanup(func() {
		// Closing the raw sockets also releases a regressed protocol loop.
		rawC.Close()
		rawS.Close()
		done := make(chan struct{}, 2)
		go func() { c.Close(); done <- struct{}{} }()
		go func() { s.Close(); done <- struct{}{} }()
		for i := 0; i < 2; i++ {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("endpoint cleanup did not complete")
				return
			}
		}
	})

	st, err := c.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	peer, err := s.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	writer := make(chan error, 1)
	go func() {
		p := make([]byte, 32768)
		for {
			if _, err := peer.Write(p); err != nil {
				writer <- err
				return
			}
		}
	}()
	t.Cleanup(func() {
		peer.ForceClose()
		select {
		case <-writer:
		case <-time.After(3 * time.Second):
			t.Error("peer writer did not stop")
		}
	})
	st.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := st.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	// Allow unread traffic to fill the small per-stream queues.
	time.Sleep(100 * time.Millisecond)
	var closers sync.WaitGroup
	closers.Add(9)
	go func() {
		defer closers.Done()
		if normalClose {
			st.Close()
		}
	}()
	for i := 0; i < 8; i++ {
		go func() { defer closers.Done(); st.ForceClose() }()
	}
	closers.Wait()

	// No reader drains st before the unrelated stream finishes its handshake.
	opened := make(chan geminio.Stream, 1)
	openErr := make(chan error, 1)
	go func() {
		x, e := c.OpenStream()
		if e != nil {
			openErr <- e
			return
		}
		opened <- x
	}()
	var next geminio.Stream
	select {
	case next = <-opened:
	case err := <-openErr:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("old abandoned stream blocked new stream")
	}
	other, err := s.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	defer other.ForceClose()
	defer next.ForceClose()
	next.SetDeadline(time.Now().Add(3 * time.Second))
	other.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := other.Write([]byte("alive")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 5)
	if _, err := io.ReadFull(next, b); err != nil || string(b) != "alive" {
		t.Fatalf("unrelated stream: %q %v", b, err)
	}
	st.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.Copy(io.Discard, st); err != nil {
		t.Fatalf("old reader failed to finish: %v", err)
	}
	if _, err := st.Write([]byte("closed")); err == nil {
		t.Fatal("write accepted after stream cleanup")
	}

}
