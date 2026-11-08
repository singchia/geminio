package multiplexer

import (
	"bytes"
	"context"
	"errors"
	"github.com/singchia/geminio/pkg/id"
	"testing"
	"time"

	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
)

func TestFlowCreditBlocksOnlyItsOwnerAndResumes(t *testing.T) {
	a, b := newDialogueFlow(), newDialogueFlow()
	if err := a.acquire(context.Background(), packet.FlowWindowBytes); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.acquire(ctx, 1) }()
	if err := b.acquire(ctx, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		t.Fatal("A sent beyond byte window")
	default:
	}
	if !a.update(packet.FlowWindowBytes, 1) {
		t.Fatal("valid return rejected")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestFlowPacketLimitCancellationAndInvalidUpdate(t *testing.T) {
	f := newDialogueFlow()
	if f.update(1, 1) {
		t.Fatal("unsolicited credit accepted")
	}
	for i := uint32(0); i < packet.FlowWindowPackets; i++ {
		if err := f.acquire(context.Background(), 22); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.acquire(ctx, 22) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked")
	}
	if f.update(^uint32(0), 1) || f.update(1, ^uint32(0)) {
		t.Fatal("overflow accepted")
	}
	if !f.update(22, 1) {
		t.Fatal("valid return rejected")
	}
	if err := f.acquire(context.Background(), 22); err != nil {
		t.Fatal(err)
	}
}
func TestFlowReceiveLimits(t *testing.T) {
	f := newDialogueFlow()
	if !f.reserveReceive(packet.FlowWindowBytes) || f.reserveReceive(1) {
		t.Fatal("byte window not enforced")
	}
	f.releaseReceive(packet.FlowWindowBytes)
	for i := uint32(0); i < packet.FlowWindowPackets; i++ {
		if !f.reserveReceive(22) {
			t.Fatal("early packet rejection")
		}
	}
	if f.reserveReceive(22) {
		t.Fatal("packet window not enforced")
	}
	f.releaseReceive(22)
	if !f.reserveReceive(22) {
		t.Fatal("released slot unavailable")
	}
}

func TestFlowCostIncludesRPCAndMessagePayload(t *testing.T) {
	pf := packet.NewPacketFactory(id.NewIDCounter(id.Odd))
	data := bytes.Repeat([]byte{0x7f}, 2*1024*1024)
	raw := pf.NewStreamPacketWithSessionID(1, data)
	msg := pf.NewMessagePacketWithIDAndSessionID(10, 1, nil, data)
	// Response embeds MessageAck and can contain a large application payload.
	response := pf.NewResponsePacket(11, nil, data, nil)
	response.SetSessionID(1)
	for _, p := range []packet.Packet{raw, msg, response, pf.NewDismissPacket(1)} {
		if !flowControlled(p) {
			t.Fatalf("unbudgeted type %T", p)
		}
		wire, err := p.Encode()
		if err != nil {
			t.Fatal(err)
		}
		sent, err := outgoingCost(p)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := packet.DecodeFromReader(bytes.NewReader(wire))
		if err != nil {
			t.Fatal(err)
		}
		if sent != uint32(len(wire)) || sent != incomingCost(decoded) {
			t.Fatalf("cost differs for %T: sent=%d received=%d wire=%d", p, sent, incomingCost(decoded), len(wire))
		}
	}
	f := newDialogueFlow()
	cost, err := outgoingCost(msg)
	if err != nil {
		t.Fatal(err)
	}
	for i := uint32(0); i < packet.FlowWindowBytes/cost; i++ {
		if err = f.acquire(context.Background(), cost); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err = f.acquire(ctx, cost); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("large messages exceeded byte budget: %v", err)
	}
}

type blockedCreditConn struct {
	conn.Conn
	entered, exited chan struct{}
}

func (c *blockedCreditConn) FlowControl() uint32 { return packet.FlowControlV1 }
func (c *blockedCreditConn) WriteContext(ctx context.Context, p packet.Packet) error {
	if p.Type() != packet.TypeWindowUpdatePacket {
		return c.Conn.WriteContext(ctx, p)
	}
	close(c.entered)
	<-ctx.Done()
	close(c.exited)
	return ctx.Err()
}
func TestFlowCreditWriterStopsOnManagerClose(t *testing.T) {
	c, s := contextTestPair(t)
	blocked := &blockedCreditConn{Conn: c, entered: make(chan struct{}), exited: make(chan struct{})}
	mux, err := NewDialogueMgr(blocked)
	if err != nil {
		t.Fatal(err)
	}
	dm := mux.(*dialogueMgr)
	// Trigger a real credit return through conn -> manager -> default dialogue.
	pf := packet.NewPacketFactory(id.NewIDCounter(id.Even))
	for i := uint32(0); i < packet.FlowWindowPackets/2; i++ {
		if err = s.Write(pf.NewStreamPacketWithSessionID(packet.SessionID1, []byte("data"))); err != nil {
			t.Fatal(err)
		}
	}
	awaitContextSignal(t, blocked.entered)
	close(dm.closeCh)
	awaitContextSignal(t, blocked.exited)
	awaitContextSignal(t, dm.creditDone)
	awaitContextSignal(t, dm.defaultDialogue.Done())
}

// A partial batch must leave enough credit for ANY next legal packet, even if
// there are no more incoming packets to trigger an update or timer.
func TestFlowBatchedReturnsCannotStrandLargePacket(t *testing.T) {
	sender, receiver := newDialogueFlow(), newDialogueFlow()
	sizes := []uint32{22, 22, packet.MaxDecodablePacketLen + 14, 22, 4*1024*1024 - 1, packet.MaxDecodablePacketLen + 14, 22}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < 1024; i++ {
		n := sizes[i%len(sizes)]
		if err := sender.acquire(ctx, n); err != nil {
			t.Fatalf("stranded at packet %d size %d: %v", i, n, err)
		}
		if !receiver.reserveReceive(n) {
			t.Fatal("receiver rejected credited data")
		}
		bytes, count := receiver.releaseReceive(n)
		if count > 0 && !sender.update(bytes, count) {
			t.Fatal("valid batch rejected")
		}
	}
}

// A burst of terminal notices must pause admission, not destroy the transport.
type resetQueueConn struct {
	conn.Conn
	closed bool
}

func (c *resetQueueConn) StreamReset() uint32 { return packet.StreamResetV1 }
func (c *resetQueueConn) ForceClose()         { c.closed = true }
func TestResetQueuePressurePreservesTransport(t *testing.T) {
	c := &resetQueueConn{}
	dm := &dialogueMgr{cn: c, flowCtx: context.Background(), resets: make(map[uint64]*packet.StreamResetPacket), credits: make(map[*dialogue]*packet.WindowUpdatePacket), creditWake: make(chan struct{}, 1), dialogues: make(map[uint64]*dialogue)}
	for i := uint64(1); i <= 257; i++ {
		dg := &dialogue{}
		dm.dialogues[i] = dg
		if err := dm.queueReset(dg); err != nil {
			t.Fatal(err)
		}
	}
	if c.closed {
		t.Fatal("reset pressure closed transport")
	}
	if len(dm.resets) != 257 || dm.resetPending != 257 {
		t.Fatal("terminal notice lost")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(dm.waitResetDrain(ctx), context.Canceled) {
		t.Fatal("admission ignored cancellation")
	}
	for dm.resetPending > resetLowWater {
		dm.resetWritten()
	}
	if err := dm.waitResetDrain(context.Background()); err != nil {
		t.Fatal(err)
	}
}
