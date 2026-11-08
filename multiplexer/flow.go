package multiplexer

import (
	"context"
	"github.com/singchia/geminio"
	"sync"
	"time"

	"github.com/singchia/geminio/packet"
)

// All dialogue traffic except the initial handshake is budgeted, including
// MessageAck/Response payloads and graceful close packets. Window updates bypass
// dialogue queues. No reordering of the existing dialogue state machine occurs.
func flowControlled(p packet.Packet) bool {
	switch p.Type() {
	case packet.TypeStreamResetPacket, packet.TypeSessionPacket, packet.TypeSessionAckPacket, packet.TypeWindowUpdatePacket:
		return false
	}
	return true
}
func outgoingCost(p packet.Packet) (uint32, error) {
	if raw, ok := p.(*packet.StreamPacket); ok {
		if len(raw.Data)+8 > packet.MaxDecodablePacketLen {
			return 0, packet.ErrPacketTooLarge
		}
		return uint32(len(raw.Data) + 22), nil
	}
	encoded, err := p.Encode()
	if err != nil {
		return 0, err
	}
	if len(encoded)-14 > packet.MaxDecodablePacketLen {
		return 0, packet.ErrPacketTooLarge
	}
	return uint32(len(encoded)), nil
}
func incomingCost(p packet.Packet) uint32 {
	return uint32(p.(interface{ WireLength() int }).WireLength())
}

type dialogueFlow struct {
	mu                             sync.Mutex
	sendBytes, sendPackets         uint32
	recvBytes, recvPackets         uint32
	returnedBytes, returnedPackets uint32
	wake                           chan struct{}
}

func newDialogueFlow() *dialogueFlow {
	return &dialogueFlow{sendBytes: packet.FlowWindowBytes, recvBytes: packet.FlowWindowBytes, sendPackets: packet.FlowWindowPackets, recvPackets: packet.FlowWindowPackets, wake: make(chan struct{}, 1)}
}
func (f *dialogueFlow) acquire(ctx context.Context, n uint32) error {
	if n > packet.FlowWindowBytes {
		return packet.ErrPacketTooLarge
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		f.mu.Lock()
		if f.sendBytes >= n && f.sendPackets > 0 {
			f.sendBytes -= n
			f.sendPackets--
			f.mu.Unlock()
			return nil
		}
		f.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.wake:
		}
	}
}
func (f *dialogueFlow) update(n, count uint32) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Subtraction avoids uint32 overflow on hostile updates.
	if n == 0 || count == 0 || n > packet.FlowWindowBytes-f.sendBytes || count > packet.FlowWindowPackets-f.sendPackets {
		return false
	}
	f.sendBytes += n
	f.sendPackets += count
	select {
	case f.wake <- struct{}{}:
	default:
	}
	return true
}
func (f *dialogueFlow) reserveReceive(n uint32) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n > f.recvBytes || f.recvPackets == 0 {
		return false
	}
	f.recvBytes -= n
	f.recvPackets--
	return true
}

// Batch credit returns without a timer. The byte threshold stays BELOW
// window - maximum legal packet, so withholding a partial batch cannot strand
// a sender waiting to send a large packet. Likewise the packet threshold is
// smaller than the packet window. Idle flows need no trailing update.
func (f *dialogueFlow) releaseReceive(n uint32) (uint32, uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recvBytes += n
	f.recvPackets++
	f.returnedBytes += n
	f.returnedPackets++
	if f.returnedBytes < packet.FlowWindowBytes/4 && f.returnedPackets < packet.FlowWindowPackets/2 {
		return 0, 0
	}
	credit, count := f.returnedBytes, f.returnedPackets
	f.returnedBytes, f.returnedPackets = 0, 0
	return credit, count
}

func (dm *dialogueMgr) deliver(dg *dialogue, ch chan packet.Packet, p packet.Packet) {
	if dg.flow == nil { // Legacy peers retain the existing wire behavior.
		select {
		case ch <- p:
		case <-dg.ctx.Done():
		case <-dm.flowCtx.Done():
		}
		return
	}
	if dg.ctx.Err() != nil {
		return
	}
	if flowControlled(p) && !dg.flow.reserveReceive(incomingCost(p)) {
		dm.cn.ForceClose() // Peer exceeded negotiated capacity: protocol violation.
		return
	}
	// Credit guarantees room: credited packets plus two handshake slots. Never let one dialogue block the shared dispatcher.
	select {
	case ch <- p:
	case <-dg.ctx.Done():
	default:
		dm.cn.ForceClose()
	}
}
func (dm *dialogueMgr) applyCredit(p *packet.WindowUpdatePacket) {
	capable, ok := dm.cn.(interface{ FlowControl() uint32 })
	if !ok || capable.FlowControl() != packet.FlowControlV1 {
		dm.cn.ForceClose()
		return
	}
	dm.mtx.RLock()
	dg := dm.dialogues[p.DialogueID]
	if dg == nil {
		dg = dm.negotiatingDialogues[p.DialogueID]
	}
	dm.mtx.RUnlock()
	// Late updates for closed dialogues are harmless; IDs are not reused.
	if dg == nil || dg.ctx.Err() != nil {
		return
	}
	if dg.flow == nil || !dg.flow.update(p.Credit, p.Packets) {
		dm.cn.ForceClose()
	}
}
func (dm *dialogueMgr) returnCredit(dg *dialogue, id uint64, n, count uint32) {
	dm.creditMu.Lock()
	if dm.flowCtx.Err() != nil {
		dm.creditMu.Unlock()
		return
	}
	p := dm.credits[dg]
	if p == nil {
		p = packet.NewWindowUpdatePacket(id, 0, 0)
		dm.credits[dg] = p
	}
	p.Credit += n
	p.Packets += count
	dm.creditMu.Unlock()
	select {
	case dm.creditWake <- struct{}{}:
	default:
	}
}

// One writer per connection coalesces returns without blocking the dispatcher or
// any dialogue FSM. A dialogue has at most one pending entry, bounded by its
// outstanding credit; ForceClose/connection teardown interrupt blocked writes.
func (dm *dialogueMgr) writeCredits() {
	defer close(dm.creditDone)
	defer func() {
		if r := recover(); r != nil {
			dm.log.Errorf("window writer panic: %v", r)
			dm.cn.ForceClose()
		}
	}()
	for {
		select {
		case <-dm.flowCtx.Done():
			return
		case <-dm.creditWake:
		}
		dm.creditMu.Lock()
		resets := dm.resets
		dm.resets = make(map[uint64]*packet.StreamResetPacket)
		batch := dm.credits
		dm.credits = make(map[*dialogue]*packet.WindowUpdatePacket)
		dm.creditMu.Unlock()
		for _, p := range resets {
			if !dm.writeControl(p) {
				return
			}
			dm.resetWritten()
		}
		for dg, p := range batch {
			if dg.ctx.Err() != nil {
				continue
			}
			if !dm.writeControl(p) {
				return
			}
		}
	}
}

// Both queues bypass per-dialogue data and credit. A stalled transport cannot
// retain control notifications forever; closing it releases all peer dialogues.
func (dm *dialogueMgr) writeControl(p packet.Packet) bool {
	ctx, cancel := context.WithTimeout(dm.flowCtx, 30*time.Second)
	defer cancel()
	var err error
	if reset, ok := p.(*packet.StreamResetPacket); ok {
		if writer, ok := dm.cn.(interface {
			WriteResetContext(context.Context, *packet.StreamResetPacket) error
		}); ok {
			err = writer.WriteResetContext(ctx, reset)
		} else {
			err = dm.cn.WriteContext(ctx, p)
		}
	} else {
		err = dm.cn.WriteContext(ctx, p)
	}
	if err != nil {
		dm.cn.ForceClose()
		return false
	}
	return true
}
func (dm *dialogueMgr) resetCapable() bool {
	c, ok := dm.cn.(interface{ StreamReset() uint32 })
	return ok && c.StreamReset() == packet.StreamResetV1
}
func (dm *dialogueMgr) queueReset(dg *dialogue) error {
	if !dm.resetCapable() {
		return geminio.ErrStreamAbortUnsupported
	}
	// Only established IDs can be sent. OpenStreamContext owns negotiation cancellation.
	dm.mtx.RLock()
	var id uint64
	for key, current := range dm.dialogues {
		if current == dg {
			id = key
			break
		}
	}
	dm.mtx.RUnlock()
	if id == 0 {
		return nil
	}
	dm.creditMu.Lock()
	if dm.flowCtx.Err() != nil {
		dm.creditMu.Unlock()
		return nil
	}
	// Existing dialogues must always be able to publish their terminal notice.
	// A burst is bounded by already admitted dialogues, not an arbitrary queue
	// cap. Pause new handshakes instead of killing unrelated active streams.
	if _, exists := dm.resets[id]; !exists {
		dm.resetPending++
	}
	if dm.resetPending >= resetHighWater && dm.resetPressure == nil {
		dm.resetPressure = make(chan struct{})
	}
	dm.resets[id] = packet.NewStreamResetPacket(id)
	dm.resets[id].NegotiateID = dg.peerNegotiatingID
	delete(dm.credits, dg)
	dm.creditMu.Unlock()
	select {
	case dm.creditWake <- struct{}{}:
	default:
	}
	return nil
}
func (dm *dialogueMgr) applyReset(p *packet.StreamResetPacket) {
	if !dm.resetCapable() {
		dm.cn.ForceClose()
		return
	}
	dm.mtx.RLock()
	dg := dm.dialogues[p.DialogueID]
	if dg == nil && p.NegotiateID != 0 {
		dg = dm.negotiatingDialogues[p.NegotiateID]
	}
	dm.mtx.RUnlock()
	// Unknown and late resets are harmless. Never echo a received reset.
	if dg != nil {
		dg.receiveReset()
	}
}

// Hysteresis avoids waking every opener for every single completed reset.
const resetHighWater = 256
const resetLowWater = resetHighWater / 2

// waitResetDrain is only used by new-stream callers/handshake goroutines, never
// the shared packet dispatcher or Abort. Count in-flight writes as well as the
// pending map: moving a batch out of the map has not drained the transport.
func (dm *dialogueMgr) waitResetDrain(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if dm.flowCtx.Err() != nil {
			return ErrOperationOnClosedMultiplexer
		}
		dm.creditMu.Lock()
		pressure := dm.resetPressure
		dm.creditMu.Unlock()
		if pressure == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-dm.flowCtx.Done():
			return ErrOperationOnClosedMultiplexer
		case <-pressure:
		}
	}
}

func (dm *dialogueMgr) resetWritten() {
	dm.creditMu.Lock()
	defer dm.creditMu.Unlock()
	dm.resetPending--
	if dm.resetPending <= resetLowWater && dm.resetPressure != nil {
		close(dm.resetPressure)
		dm.resetPressure = nil
	}
}
