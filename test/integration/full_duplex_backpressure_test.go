package integration

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/server"
	"github.com/singchia/geminio/test/harness"
)

// Small queues force both protocol directions to apply backpressure. The
// application continues reading throughout: a stalled transfer is not a slow
// reader test. Verify sequence, payload, a concurrent stream and graceful EOF.
func TestFullDuplexBackpressure(t *testing.T) {
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
	st, e := c.OpenStream()
	if e != nil {
		t.Fatal(e)
	}
	peer, e := s.AcceptStream()
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(15 * time.Second)
	st.SetDeadline(deadline)
	peer.SetDeadline(deadline)
	const block = 32 * 1024
	const blocks = 8192 // 256 MiB in each direction, bounded memory.
	echoDone := make(chan error, 1)
	go func() { _, e := io.Copy(peer, peer); peer.Close(); echoDone <- e }()
	sent := make(chan error, 1)
	go func() {
		p := bytes.Repeat([]byte{0x5a}, block)
		for i := 0; i < blocks; i++ {
			binary.BigEndian.PutUint64(p, uint64(i))
			n, e := st.Write(p)
			if e != nil {
				sent <- e
				return
			}
			if n != len(p) {
				sent <- io.ErrShortWrite
				return
			}
		}
		sent <- nil
	}()
	opened := make(chan error, 1)
	accepted := make(chan error, 1)
	p := make([]byte, block)
	payload := bytes.Repeat([]byte{0x5a}, block-8)
	for i := 0; i < blocks; i++ {
		if _, e := io.ReadFull(st, p); e != nil {
			t.Fatalf("echo stalled at block %d: %v", i, e)
		}
		if binary.BigEndian.Uint64(p) != uint64(i) || !bytes.Equal(p[8:], payload) {
			t.Fatalf("corrupt or reordered block %d", i)
		}
		if i == blocks/2 {
			go func() {
				x, err := c.OpenStream()
				if err == nil {
					err = x.Close()
				}
				opened <- err
			}()
			go func() {
				x, err := s.AcceptStream()
				if err == nil {
					err = x.Close()
				}
				accepted <- err
			}()
		}
	}
	if e := <-sent; e != nil {
		t.Fatal(e)
	}
	for _, done := range []chan error{opened, accepted} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent stream did not complete")
		}
	}
	st.Close()
	select {
	case e := <-echoDone:
		if e != nil {
			t.Fatal(fmt.Errorf("echo shutdown: %w", e))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("echo did not exit after draining")
	}
}
