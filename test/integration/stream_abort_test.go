package integration

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/server"
	"github.com/singchia/geminio/test/harness"
)

func abortPair(t *testing.T) (geminio.End, geminio.End) {
	t.Helper()
	harness.LogSilence(t)
	a, b := harness.NewConnPair(t)
	co := client.NewEndOptions()
	co.SetBufferSize(1, 1)
	so := server.NewEndOptions()
	so.SetBufferSize(1, 1)
	type result struct {
		e   geminio.End
		err error
	}
	ch := make(chan result, 1)
	go func() { s, e := server.NewEndWithConn(a, so); ch <- result{s, e} }()
	c, e := client.NewEndWithConn(b, co)
	if e != nil {
		t.Fatal(e)
	}
	r := <-ch
	if r.err != nil {
		t.Fatal(r.err)
	}
	t.Cleanup(func() { c.ForceClose(); r.e.ForceClose(); a.Close(); b.Close() })
	return c, r.e
}
func waitStreams(t *testing.T, e geminio.End, n int) {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		if len(e.ListStreams()) == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("streams retained: got %d want %d", len(e.ListStreams()), n)
}
func fillUnread(t *testing.T, s geminio.Stream) {
	t.Helper()
	s.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	p := bytes.Repeat([]byte{0x73}, 32768)
	for {
		if _, e := s.Write(p); e != nil {
			if !errors.Is(e, os.ErrDeadlineExceeded) {
				t.Fatal(e)
			}
			break
		}
	}
	s.SetWriteDeadline(time.Time{})
}
func TestStreamAbortReclaimsBothPeers(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "client", true: "server"}[reverse], func(t *testing.T) {
			c, s := abortPair(t)
			if reverse {
				c, s = s, c
			}
			for round := 0; round < 12; round++ {
				a, e := c.OpenStream()
				if e != nil {
					t.Fatal(e)
				}
				b, e := s.AcceptStream()
				if e != nil {
					t.Fatal(e)
				}
				fillUnread(t, a)
				// The receive side aborts while the peer writer has exhausted its window.
				if e = geminio.AbortStream(b); e != nil {
					t.Fatal(e)
				}
				if e = geminio.AbortStream(b); e != nil {
					t.Fatal(e)
				}
				waitStreams(t, c, 1)
				waitStreams(t, s, 1)
				if _, e = a.Read(make([]byte, 1)); !errors.Is(e, geminio.ErrStreamAborted) {
					t.Fatalf("want reset, got %v", e)
				}
				if _, e = a.Write([]byte("late")); !errors.Is(e, geminio.ErrStreamAborted) {
					t.Fatalf("want reset write, got %v", e)
				}
			}
			// Same connection remains usable after all cancellations.
			a, e := c.OpenStream()
			if e != nil {
				t.Fatal(e)
			}
			b, e := s.AcceptStream()
			if e != nil {
				t.Fatal(e)
			}
			a.SetDeadline(time.Now().Add(time.Second))
			b.SetDeadline(time.Now().Add(time.Second))
			if _, e = a.Write([]byte("alive")); e != nil {
				t.Fatal(e)
			}
			data := make([]byte, 5)
			if _, e = io.ReadFull(b, data); e != nil || string(data) != "alive" {
				t.Fatalf("sibling: %s %v", data, e)
			}
			geminio.AbortStream(a)
			waitStreams(t, c, 1)
			waitStreams(t, s, 1)
		})
	}
}
func TestCloseTimeoutIncludesBlockedDrain(t *testing.T) {
	c, s := abortPair(t)
	a, e := c.OpenStream()
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.AcceptStream()
	if e != nil {
		t.Fatal(e)
	}
	fillUnread(t, a)
	if e = a.(interface{ CloseWithTimeout(time.Duration) error }).CloseWithTimeout(50 * time.Millisecond); e != nil {
		t.Fatal(e)
	}
	waitStreams(t, c, 1)
	waitStreams(t, s, 1)
	if _, e = b.Read(make([]byte, 1)); !errors.Is(e, geminio.ErrStreamAborted) {
		t.Fatalf("close timeout: %v", e)
	}
}

// AcceptStream can return before the SessionAck reaches the opener. An urgent
// RESET must still cancel that opening and release both manager registrations.
func TestAbortImmediatelyAfterAccept(t *testing.T) {
	c, s := abortPair(t)
	for i := 0; i < 50; i++ {
		done := make(chan error, 1)
		go func() {
			b, e := s.AcceptStream()
			if e == nil {
				e = geminio.AbortStream(b)
			}
			done <- e
		}()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		a, err := geminio.OpenStreamContext(ctx, c)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("reset lost during opening")
		}
		if a != nil {
			a.SetReadDeadline(time.Now().Add(time.Second))
			_, err = a.Read(make([]byte, 1))
			if !errors.Is(err, geminio.ErrStreamAborted) {
				t.Fatal(err)
			}
		}
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(time.Second):
			t.Fatal("accept abort blocked")
		}
		waitStreams(t, c, 1)
		waitStreams(t, s, 1)
	}
}
