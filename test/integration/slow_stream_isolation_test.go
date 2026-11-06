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

// A stops reading until backpressure reaches its writer. B must still deliver
// data and C must negotiate on the SAME underlying connection.
func TestSlowStreamDoesNotBlockConnection(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		name := "client_to_server"
		if reverse {
			name = "server_to_client"
		}
		t.Run(name, func(t *testing.T) { testSlowStreamIsolation(t, reverse, false) })
		t.Run(name+"_default", func(t *testing.T) { testSlowStreamIsolation(t, reverse, true) })
	}
}

func testSlowStreamIsolation(t *testing.T, reverse, useDefault bool) {
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
	c, err := client.NewEndWithConn(rawC, co)
	if err != nil {
		t.Fatal(err)
	}
	res := <-ready
	if res.err != nil {
		t.Fatal(res.err)
	}
	s := res.end
	if reverse {
		c, s = s, c
	}
	t.Cleanup(func() { c.ForceClose(); s.ForceClose(); rawC.Close(); rawS.Close() })
	var a, aPeer geminio.Stream = c, s
	if !useDefault {
		a, err = c.OpenStream()
		if err != nil {
			t.Fatal(err)
		}
		aPeer, err = s.AcceptStream()
		if err != nil {
			t.Fatal(err)
		}
	}
	b, err := c.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	bPeer, err := s.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	a.SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
	block := bytes.Repeat([]byte{0x5a}, 16*1024)
	var accepted int64
	for {
		n, e := a.Write(block)
		accepted += int64(n)
		if e != nil {
			if !errors.Is(e, os.ErrDeadlineExceeded) {
				t.Fatalf("A should pause, got %v", e)
			}
			break
		}
		if accepted > 128*1024*1024 {
			t.Fatal("unread A did not apply bounded backpressure")
		}
	}
	b.SetDeadline(time.Now().Add(time.Second))
	bPeer.SetDeadline(time.Now().Add(time.Second))
	if _, err := b.Write([]byte("B alive")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 7)
	if _, err := io.ReadFull(bPeer, got); err != nil {
		t.Fatalf("A blocked B: %v", err)
	}
	if string(got) != "B alive" {
		t.Fatal("B payload corrupted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	next, err := geminio.OpenStreamContext(ctx, c)
	if err != nil {
		t.Fatalf("A blocked C opening: %v", err)
	}
	nextPeer, err := s.AcceptStream()
	if err != nil {
		t.Fatal(err)
	}
	defer next.ForceClose()
	defer nextPeer.ForceClose()
	// A remains usable: all writes accepted before the deadline arrive in order.
	a.SetWriteDeadline(time.Time{})
	aPeer.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len(block))
	for remaining := accepted; remaining > 0; {
		n := int64(len(buf))
		if remaining < n {
			n = remaining
		}
		if _, err := io.ReadFull(aPeer, buf[:n]); err != nil {
			t.Fatalf("A could not resume: %v", err)
		}
		if !bytes.Equal(buf[:n], block[:n]) {
			t.Fatal("A payload corrupted")
		}
		remaining -= n
	}
	if _, err := a.Write([]byte("resumed")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(aPeer, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "resumed" {
		t.Fatal("A failed to resume")
	}
}
