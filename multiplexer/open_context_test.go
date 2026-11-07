package multiplexer

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
)

type blockedSessionConn struct {
	conn.Conn
	entered chan struct{}
	exited  chan struct{}
}

func (c *blockedSessionConn) WriteContext(ctx context.Context, p packet.Packet) error {
	if p.Type() != packet.TypeSessionPacket {
		return c.Conn.WriteContext(ctx, p)
	}
	close(c.entered)
	<-ctx.Done()
	close(c.exited)
	return ctx.Err()
}

func contextTestPair(t *testing.T) (*conn.ClientConn, *conn.ServerConn) {
	t.Helper()
	a, b := net.Pipe()
	ready := make(chan *conn.ServerConn, 1)
	go func() {
		s, err := conn.NewServerConn(a)
		if err != nil {
			ready <- nil
			return
		}
		ready <- s
	}()
	c, err := conn.NewClientConn(b)
	if err != nil {
		t.Fatal(err)
	}
	s := <-ready
	if s == nil {
		t.Fatal("server handshake failed")
	}
	t.Cleanup(func() { c.ForceClose(); s.ForceClose() })
	return c, s
}
func awaitContextSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("operation did not finish")
	}
}
func nextSession(t *testing.T, s *conn.ServerConn) *packet.SessionPacket {
	t.Helper()
	select {
	case p := <-s.ChannelRead():
		sp, ok := p.(*packet.SessionPacket)
		if !ok {
			t.Fatalf("expected session, got %T", p)
		}
		return sp
	case <-time.After(2 * time.Second):
		t.Fatal("no session request")
		return nil
	}
}
func TestOpenDialogueContextCancelAndLateAck(t *testing.T) {
	c, s := contextTestPair(t)
	mux, err := NewDialogueMgr(c)
	if err != nil {
		t.Fatal(err)
	}
	dm := mux.(*dialogueMgr)
	t.Cleanup(func() { close(dm.closeCh) })
	pf := packet.NewPacketFactory(nil)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := dm.OpenDialogueContext(ctx, nil, ""); result <- err }()
	req := nextSession(t, s)
	dm.mtx.RLock()
	dg := dm.negotiatingDialogues[req.NegotiateID()]
	dm.mtx.RUnlock()
	if dg == nil {
		t.Fatal("missing negotiation")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not return")
	}
	awaitContextSignal(t, dg.Done())
	// The ACK arrives after cancellation returned. It cannot publish the old flow.
	if err := s.Write(pf.NewSessionAckPacket(req.ID(), req.NegotiateID(), 100, nil)); err != nil {
		t.Fatal(err)
	}
	// A later stream on the SAME transport must still negotiate normally.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	opened := make(chan Dialogue, 1)
	go func() {
		d, e := dm.OpenDialogueContext(ctx2, nil, "")
		if e != nil {
			result <- e
			return
		}
		opened <- d
	}()
	req2 := nextSession(t, s)
	if err := s.Write(pf.NewSessionAckPacket(req2.ID(), req2.NegotiateID(), 102, nil)); err != nil {
		t.Fatal(err)
	}
	var next Dialogue
	select {
	case next = <-opened:
	case e := <-result:
		t.Fatal(e)
	case <-time.After(2 * time.Second):
		t.Fatal("new stream blocked")
	}
	defer next.ForceClose()
	cancel2()
	select {
	case <-next.Done():
		t.Fatal("opening context closed a successful stream")
	default:
	}
	dm.mtx.RLock()
	pending := len(dm.negotiatingDialogues)
	_, resurrected := dm.dialogues[100]
	dm.mtx.RUnlock()
	if pending != 0 || resurrected {
		t.Fatalf("pending=%d resurrected=%v", pending, resurrected)
	}
}
func TestOpenDialogueContextCancelsBlockedWriter(t *testing.T) {
	c, _ := contextTestPair(t)
	blocked := &blockedSessionConn{Conn: c, entered: make(chan struct{}), exited: make(chan struct{})}
	mux, err := NewDialogueMgr(blocked)
	if err != nil {
		t.Fatal(err)
	}
	dm := mux.(*dialogueMgr)
	t.Cleanup(func() { close(dm.closeCh) })
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, e := dm.OpenDialogueContext(ctx, nil, ""); result <- e }()
	awaitContextSignal(t, blocked.entered)
	cancel()
	select {
	case e := <-result:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel blocked")
	}
	awaitContextSignal(t, blocked.exited)
	dm.mtx.RLock()
	pending := len(dm.negotiatingDialogues)
	dm.mtx.RUnlock()
	if pending != 0 {
		t.Fatal("negotiation leaked")
	}
}

func TestOpenDialogueContextDeadline(t *testing.T) {
	c, _ := contextTestPair(t)
	mux, err := NewDialogueMgr(c)
	if err != nil {
		t.Fatal(err)
	}
	dm := mux.(*dialogueMgr)
	t.Cleanup(func() { close(dm.closeCh) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = dm.OpenDialogueContext(ctx, nil, "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("ignored context deadline")
	}
	dm.mtx.RLock()
	pending := len(dm.negotiatingDialogues)
	dm.mtx.RUnlock()
	if pending != 0 {
		t.Fatal("deadline leaked negotiation")
	}
}

func TestOpenDialogueContextCancelAckRace(t *testing.T) {
	c, s := contextTestPair(t)
	mux, err := NewDialogueMgr(c)
	if err != nil {
		t.Fatal(err)
	}
	dm := mux.(*dialogueMgr)
	t.Cleanup(func() { close(dm.closeCh) })
	pf := packet.NewPacketFactory(nil)
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		type result struct {
			dg  Dialogue
			err error
		}
		done := make(chan result, 1)
		go func() { dg, err := dm.OpenDialogueContext(ctx, nil, ""); done <- result{dg, err} }()
		req := nextSession(t, s)
		sent := make(chan struct{})
		go func() {
			defer close(sent)
			s.Write(pf.NewSessionAckPacket(req.ID(), req.NegotiateID(), uint64(100+i), nil))
		}()
		cancel()
		select {
		case r := <-done:
			if r.err != nil && !errors.Is(r.err, context.Canceled) {
				t.Fatal(r.err)
			}
			if r.dg != nil {
				r.dg.ForceClose()
			}
		case <-time.After(time.Second):
			t.Fatal("ACK/cancel race blocked")
		}
		awaitContextSignal(t, sent)
	}
	dm.mtx.RLock()
	pending := len(dm.negotiatingDialogues)
	dm.mtx.RUnlock()
	if pending != 0 {
		t.Fatal("ACK race leaked negotiating entries")
	}
}
