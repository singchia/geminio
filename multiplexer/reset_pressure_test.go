package multiplexer

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
)

// Delay only reset writes: ordinary data and the shared reader remain live.
type delayedResetConn struct {
	*conn.ServerConn
	release chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (c *delayedResetConn) WriteResetContext(ctx context.Context, p *packet.StreamResetPacket) error {
	c.once.Do(func() { close(c.entered) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.release:
	}
	return c.ServerConn.WriteResetContext(ctx, p)
}

func TestResetPressureKeepsExistingFlowAndResumesBothOpenDirections(t *testing.T) {
	c, s := contextTestPair(t)
	delayed := &delayedResetConn{ServerConn: s, release: make(chan struct{}), entered: make(chan struct{})}
	cm, err := NewDialogueMgr(c, OptionMultiplexerAcceptDialogue())
	if err != nil {
		t.Fatal(err)
	}
	sm, err := NewDialogueMgr(delayed, OptionMultiplexerAcceptDialogue())
	if err != nil {
		t.Fatal(err)
	}
	cd, sd := cm.(*dialogueMgr), sm.(*dialogueMgr)
	t.Cleanup(func() { close(cd.closeCh); close(sd.closeCh) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var keep, peer Dialogue
	var retiring []*dialogue
	for i := 0; i < 301; i++ {
		d, e := sd.OpenDialogueContext(ctx, nil, "")
		if e != nil {
			t.Fatal(e)
		}
		remote, e := cd.AcceptDialogue()
		if e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			keep, peer = d, remote
		} else {
			retiring = append(retiring, d.(*dialogue))
		}
	}
	for _, d := range retiring {
		if err := d.Abort(); err != nil {
			t.Fatal(err)
		}
	}
	awaitContextSignal(t, delayed.entered)
	sd.creditMu.Lock()
	pending, pressure := sd.resetPending, sd.resetPressure
	sd.creditMu.Unlock()
	if pending != 300 || pressure == nil {
		t.Fatalf("pending=%d, pressure=%v", pending, pressure)
	}
	// The in-flight batch must still count against admission.
	short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	_, err = sd.OpenDialogueContext(short, nil, "")
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("open during pressure: %v", err)
	}
	type result struct {
		d   Dialogue
		err error
	}
	incoming := make(chan result, 1)
	go func() { d, e := cd.OpenDialogueContext(ctx, nil, ""); incoming <- result{d, e} }()
	// Wait until the shared reader has dispatched the remote handshake.
	for {
		sd.mtx.RLock()
		n := len(sd.negotiatingDialogues)
		sd.mtx.RUnlock()
		if n > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("remote handshake not dispatched")
		case <-time.After(time.Millisecond):
		}
	}
	// Existing flow works while a new remote handshake is paused.
	data := []byte("old connection remains usable")
	pf := keep.(*dialogue).pf
	if err := keep.Write(pf.NewStreamPacketWithSessionID(keep.DialogueID(), data)); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-peer.ReadC():
		if p == nil || !bytes.Equal(p.(*packet.StreamPacket).Data, data) {
			t.Fatal("payload mismatch")
		}
	case <-ctx.Done():
		t.Fatal("existing stream stalled")
	}
	if err := peer.Write(pf.NewStreamPacketWithSessionID(peer.DialogueID(), data)); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-keep.ReadC():
		if p == nil || !bytes.Equal(p.(*packet.StreamPacket).Data, data) {
			t.Fatal("reverse payload mismatch")
		}
	case <-ctx.Done():
		t.Fatal("reverse flow stalled")
	}
	select {
	case r := <-incoming:
		t.Fatalf("remote open bypassed pressure: %v", r.err)
	default:
	}
	close(delayed.release)
	select {
	case r := <-incoming:
		if r.err != nil {
			t.Fatal(r.err)
		}
		defer r.d.ForceClose()
	case <-ctx.Done():
		t.Fatal("remote open did not recover")
	}
	accepted, e := sd.AcceptDialogue()
	if e != nil {
		t.Fatal(e)
	}
	defer accepted.ForceClose()
	next, e := sd.OpenDialogueContext(ctx, nil, "")
	if e != nil {
		t.Fatal(e)
	}
	defer next.ForceClose()
	nextPeer, e := cd.AcceptDialogue()
	if e != nil {
		t.Fatal(e)
	}
	defer nextPeer.ForceClose()
	// All terminal notices must eventually reach the original remote manager.
	for {
		sd.creditMu.Lock()
		n := sd.resetPending
		sd.creditMu.Unlock()
		if n == 0 && len(cd.ListDialogues()) == 4 {
			break
		} // default + keep + two new streams
		select {
		case <-ctx.Done():
			t.Fatalf("reset drain stalled: pending=%d remote=%d", n, len(cd.ListDialogues()))
		case <-time.After(time.Millisecond):
		}
	}
	keep.ForceClose()
	peer.ForceClose()
}

func TestResetPressureManagerCloseWakesAdmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dm := &dialogueMgr{flowCtx: ctx, resetPressure: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- dm.waitResetDrain(context.Background()) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrOperationOnClosedMultiplexer) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("admission stuck after close")
	}
}
