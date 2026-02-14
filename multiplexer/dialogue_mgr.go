package multiplexer

import (
	"errors"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/delegate"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/geminio/pkg/iodefine"
	"github.com/singchia/go-timer/v2"
)

type opts struct {
	// timer
	tmr      timer.Timer
	tmrOwner interface{}
	// packet factory
	pf packet.PacketFactory
	// logger
	log log.Logger
	// delegate
	dlgt Delegate
}

type multiplexerOpts struct {
	*opts
	// global client ID factory, set nil at client side
	dialogueIDs id.IDFactory
	// for outside usage
	dialogueAcceptCh        chan *dialogue
	dialogueAcceptChOutside bool

	dialogueAcceptFn func(Dialogue)

	dialogueClosedCh        chan *dialogue
	dialogueClosedChOutside bool

	dialogueClosedFn func(Dialogue)

	readBufferSize, writeBufferSize int
}

type dialogueMgr struct {
	// options
	*multiplexerOpts
	// under layer
	cn conn.Conn

	// close channel
	closeCh chan struct{}

	// dialogues
	dialogueIDs     id.IDFactory // set nil in client
	defaultDialogue *dialogue
	// mtx protect follows
	mtx                  sync.RWMutex
	mgrOK                bool
	dialogues            map[uint64]*dialogue // key: dialogueID, value: dialogue
	negotiatingDialogues map[uint64]*dialogue

	// write scheduler for fair write scheduling across dialogues
	writeSchedulerStopCh chan struct{}
	writeSchedulerWg     sync.WaitGroup
	// schedulerDialogueChs: cached list of dialogue channels for scheduler (protected by schedulerMtx)
	schedulerMtx         sync.RWMutex
	schedulerDialogueChs []*dialogueWriteCh
	// schedulerUpdateCh: notification channel to trigger scheduler to rebuild cases when dialogues are added/removed
	schedulerUpdateCh chan struct{}
	// schedulerRoundRobinIndex: round-robin index for fair scheduling (using atomic operations for lock-free updates)
	schedulerRoundRobinIndex int64
}

type dialogueWriteCh struct {
	dg *dialogue
	ch chan packet.Packet
}

type MultiplexerOption func(*multiplexerOpts)

func OptionMultiplexerAcceptDialogue() MultiplexerOption {
	return func(opts *multiplexerOpts) {
		opts.dialogueAcceptCh = make(chan *dialogue, 32)
		opts.dialogueAcceptChOutside = false
	}
}

func OptionMultiplexerClosedDialogue() MultiplexerOption {
	return func(opts *multiplexerOpts) {
		opts.dialogueClosedCh = make(chan *dialogue, 32)
		opts.dialogueClosedChOutside = false
	}
}

// the function is prior to OptionMultiplexerAcceptDialogue
func OptionMultiplexerAcceptFunc(fn func(Dialogue)) MultiplexerOption {
	return func(opts *multiplexerOpts) {
		opts.dialogueAcceptFn = fn
	}
}

func OptionMultiplexerClosedFunc(fn func(Dialogue)) MultiplexerOption {
	return func(opts *multiplexerOpts) {
		opts.dialogueClosedFn = fn
	}
}

// Set delegate to know online and offline events
func OptionDelegate(dlgt Delegate) MultiplexerOption {
	return func(opts *multiplexerOpts) {
		opts.dlgt = dlgt
	}
}

// Set the packet factory for packet generating
func OptionPacketFactory(pf packet.PacketFactory) MultiplexerOption {
	return func(opts *multiplexerOpts) {
		opts.pf = pf
	}
}

func OptionLogger(log log.Logger) MultiplexerOption {
	return func(opts *multiplexerOpts) {
		opts.log = log
	}
}

func OptionTimer(tmr timer.Timer) MultiplexerOption {
	return func(opts *multiplexerOpts) {
		opts.tmr = tmr
		opts.tmrOwner = nil
	}
}

func OptionBufferSize(read, write int) MultiplexerOption {
	return func(opts *multiplexerOpts) {
		if read > 0 {
			opts.readBufferSize = read
		}
		if write > 0 {
			opts.writeBufferSize = write
		}
	}
}

func NewDialogueMgr(cn conn.Conn, mpopts ...MultiplexerOption) (Multiplexer, error) {
	dm := &dialogueMgr{
		multiplexerOpts: &multiplexerOpts{
			opts:            &opts{},
			readBufferSize:  -1,
			writeBufferSize: -1,
		},
		cn:                   cn,
		mgrOK:                true,
		dialogues:            make(map[uint64]*dialogue),
		negotiatingDialogues: make(map[uint64]*dialogue),
		closeCh:              make(chan struct{}),
	}
	// dialogue id counter
	if dm.cn.Side() == geminio.RecipientSide {
		dm.dialogueIDs = id.NewIDCounter(id.Even)
		dm.dialogueIDs.ReserveID(packet.SessionID1)
	} else {
		dm.dialogueIDs = id.NewIDCounter(id.Odd)
		dm.dialogueIDs.ReserveID(packet.SessionID1)
	}
	// options
	for _, opt := range mpopts {
		opt(dm.multiplexerOpts)
	}
	// sync hub
	if dm.tmr == nil {
		dm.tmr = timer.NewTimer()
		dm.tmrOwner = dm
	}
	// log
	if dm.log == nil {
		dm.log = log.DefaultLog
	}
	// add default dialogue
	dg, err := NewDialogue(cn, dm.multiplexerOpts.opts,
		OptionDialogueState(SESSIONED),
		OptionDialogueDelegate(dm),
		OptionDialogueLogger(dm.log),
		OptionDialoguePacketFactory(dm.pf),
		OptionDialogueMeta(cn.Meta()),
		OptionDialogueBufferSize(dm.readBufferSize, dm.writeBufferSize))
	if err != nil {
		dm.log.Errorf("new dialogue err: %s, clientID: %d, dialogueID: %d",
			err, cn.ClientID(), packet.SessionID1)
		goto ERR
	}
	dg.dialogueID = packet.SessionID1
	dm.defaultDialogue = dg
	dm.dialogues[packet.SessionID1] = dg
	// Initialize scheduler dialogue list with default dialogue
	dm.updateSchedulerDialogueList()
	// rolling up
	go dm.readPkt()
	// start write scheduler for fair write scheduling across dialogues
	dm.writeSchedulerStopCh = make(chan struct{})
	dm.schedulerUpdateCh = make(chan struct{}, 1) // buffered to avoid blocking
	dm.writeSchedulerWg.Add(1)
	go dm.writeScheduler()
	return dm, nil
ERR:
	if dm.tmrOwner == dm {
		dm.tmr.Close()
	}
	return nil, err
}

func (dm *dialogueMgr) DialogueOnline(dg delegate.DialogueDescriber) error {
	dm.log.Debugf("dialogue online, clientID: %d, add dialogueID: %d", dg.ClientID(), dg.DialogueID())
	dm.mtx.Lock()
	defer dm.mtx.Unlock()

	if !dm.mgrOK {
		return ErrOperationOnClosedMultiplexer
	}
	// remove from the negotiating dialogues, and add to ready dialogues.
	_, ok := dm.negotiatingDialogues[dg.NegotiatingID()]
	if ok {
		delete(dm.negotiatingDialogues, dg.NegotiatingID())
	}
	dialogue := dg.(*dialogue)
	dm.dialogues[dg.DialogueID()] = dialogue
	if dm.dlgt != nil {
		dm.dlgt.DialogueOnline(dg)
	}
	// notify outside that a dialogue is accepting
	if dm.dialogueAcceptFn != nil {
		dm.dialogueAcceptFn(dialogue)

	} else if dm.dialogueAcceptCh != nil {
		// this must not be blocked, or else the whole system will stop
		dm.dialogueAcceptCh <- dialogue
	}

	// Update scheduler's dialogue list
	dm.updateSchedulerDialogueList()
	// Notify scheduler to rebuild cases (non-blocking)
	select {
	case dm.schedulerUpdateCh <- struct{}{}:
	default:
		// Channel is full, notification already pending
	}
	return nil
}

// updateSchedulerDialogueList rebuilds the cached dialogue list for the scheduler
// This is called when dialogues are added or removed, avoiding frequent rebuilds in the scheduler
func (dm *dialogueMgr) updateSchedulerDialogueList() {
	dm.schedulerMtx.Lock()
	defer dm.schedulerMtx.Unlock()

	dm.schedulerDialogueChs = make([]*dialogueWriteCh, 0, len(dm.dialogues)+len(dm.negotiatingDialogues))

	// Add all dialogues - monitor writeOutCh (Write() sends here)
	// Scheduler will read from writeOutCh and process directly (no schedulerCh needed)
	for _, dg := range dm.dialogues {
		dg.mtx.RLock()
		if dg.dialogueOK && dg.writeOutCh != nil {
			dm.schedulerDialogueChs = append(dm.schedulerDialogueChs, &dialogueWriteCh{
				dg: dg,
				ch: dg.writeOutCh, // Monitor writeOutCh (Write() sends here)
			})
		}
		dg.mtx.RUnlock()
	}
	// Add negotiating dialogues
	for _, dg := range dm.negotiatingDialogues {
		dg.mtx.RLock()
		if dg.dialogueOK && dg.writeOutCh != nil {
			dm.schedulerDialogueChs = append(dm.schedulerDialogueChs, &dialogueWriteCh{
				dg: dg,
				ch: dg.writeOutCh, // Monitor writeOutCh (Write() sends here)
			})
		}
		dg.mtx.RUnlock()
	}
}

func (dm *dialogueMgr) DialogueOffline(dg delegate.DialogueDescriber) error {
	clientID := dg.ClientID()
	dialogueID := dg.DialogueID()

	dm.log.Debugf("dialogue offline, clientID: %d, del dialogueID: %d", clientID, dialogueID)
	dm.mtx.Lock()
	defer dm.mtx.Unlock()

	_, ok := dm.dialogues[dialogueID]
	if ok {
		delete(dm.dialogues, dialogueID)
		if dm.dlgt != nil {
			dm.dlgt.DialogueOffline(dg)
		}
	} else {
		dm.log.Warnf("dialogue offline, cliengID: %d, dialogueID: %d not found", clientID, dialogueID)
	}
	// notify outside that a dialogue is closed
	if dm.dialogueClosedFn != nil {
		dm.dialogueClosedFn(dg.(*dialogue))

	} else if dm.dialogueClosedCh != nil {
		// this must not be blocked, or else the whole system will stop
		dm.dialogueClosedCh <- dg.(*dialogue)

	}
	// Update scheduler's dialogue list
	dm.updateSchedulerDialogueList()
	// Notify scheduler to rebuild cases (non-blocking)
	select {
	case dm.schedulerUpdateCh <- struct{}{}:
	default:
		// Channel is full, notification already pending
	}
	// unsucceed dialogue
	return ErrDialogueNotFound
}

func (dm *dialogueMgr) getID() uint64 {
	if dm.cn.Side() == geminio.InitiatorSide {
		return packet.SessionIDNull
	}
	return dm.dialogueIDs.GetID()
}

// OpenDialogue blocks until succeed or failed
func (dm *dialogueMgr) OpenDialogue(meta []byte, peer string) (Dialogue, error) {
	dm.mtx.RLock()
	if !dm.mgrOK {
		dm.mtx.RUnlock()
		return nil, ErrOperationOnClosedMultiplexer
	}
	dm.mtx.RUnlock()

	negotiatingID := dm.dialogueIDs.GetID()
	dialogueIDPeersCall := dm.cn.Side() == geminio.InitiatorSide
	dg, err := NewDialogue(dm.cn, dm.multiplexerOpts.opts,
		OptionDialogueNegotiatingID(negotiatingID, dialogueIDPeersCall),
		OptionDialogueDelegate(dm),
		OptionDialogueLogger(dm.log),
		OptionDialoguePacketFactory(dm.pf),
		OptionDialogueMeta(meta),
		OptionDialoguePeer(peer))
	if err != nil {
		dm.log.Errorf("new dialogue err: %s, clientID: %d", err, dm.cn.ClientID())
		return nil, err
	}
	dm.mtx.Lock()
	dm.negotiatingDialogues[negotiatingID] = dg
	dm.mtx.Unlock()
	// Update scheduler's dialogue list to include negotiating dialogue
	// This ensures handshake packets (SessionPacket) can be processed by the scheduler
	dm.updateSchedulerDialogueList()
	// Notify scheduler to rebuild cases (non-blocking)
	select {
	case dm.schedulerUpdateCh <- struct{}{}:
	default:
		// Channel is full, notification already pending
	}
	// Open take times, shouldn't be locked
	err = dg.open()
	if err != nil {
		dm.log.Errorf("dialogue open err: %s, clientID: %d, negotiatingID: %d", err, dm.cn.ClientID(), dg.negotiatingID)
		dm.mtx.Lock()
		delete(dm.negotiatingDialogues, negotiatingID)
		dm.mtx.Unlock()
		return nil, err
	}
	dm.mtx.Lock()
	delete(dm.negotiatingDialogues, negotiatingID)
	if !dm.mgrOK {
		// delete(dm.dialogues, dg.dialogueID)
		// !mgrOK only happens after dialogueMgr fini, so fini the dialogue
		dm.mtx.Unlock()
		dg.finiOnce.Do(dg.fini)
		return nil, ErrOperationOnClosedMultiplexer
	}
	// the logic on negotiatingDialogues is tricky, be care of it.
	dm.dialogues[dg.dialogueID] = dg
	dm.mtx.Unlock()
	// Update scheduler's dialogue list after dialogue moves from negotiatingDialogues to dialogues
	// This ensures the scheduler uses the correct dialogue list
	dm.updateSchedulerDialogueList()
	// Notify scheduler to rebuild cases (non-blocking)
	select {
	case dm.schedulerUpdateCh <- struct{}{}:
	default:
		// Channel is full, notification already pending
	}
	return dg, nil
}

// AcceptDialogue blocks until success or end
func (dm *dialogueMgr) AcceptDialogue() (Dialogue, error) {
	if dm.dialogueAcceptCh == nil {
		return nil, ErrAcceptChNotEnabled
	}
	dg, ok := <-dm.dialogueAcceptCh
	if !ok {
		return nil, io.EOF
	}
	return dg, nil
}

// ClosedDialogue blocks until success or end
func (dm *dialogueMgr) ClosedDialogue() (Dialogue, error) {
	if dm.dialogueClosedCh == nil {
		return nil, ErrClosedChNotEnabled
	}
	dg, ok := <-dm.dialogueClosedCh
	if !ok {
		return nil, io.EOF
	}
	return dg, nil
}

func (dm *dialogueMgr) ListDialogues() []Dialogue {
	dialogues := []Dialogue{}
	dm.mtx.RLock()
	defer dm.mtx.RUnlock()

	for _, dialogue := range dm.dialogues {
		dialogues = append(dialogues, dialogue)
	}
	return dialogues
}

func (dm *dialogueMgr) GetDialogue(clientID, dialogueID uint64) (Dialogue, error) {
	if dm.cn.ClientID() != clientID {
		return nil, errors.New("unfound clientID")
	}
	dialogue, ok := dm.dialogues[dialogueID]
	if !ok {
		return nil, errors.New("unfound dialgoueID")
	}
	return dialogue, nil
}

func (dm *dialogueMgr) readPkt() {
	for {
		select {
		case pkt, ok := <-dm.cn.ChannelRead():
			if !ok {
				dm.log.Debugf("dialogue mgr read done, clientID: %d", dm.cn.ClientID())
				goto FINI
			}
			dm.handlePkt(pkt)
		case <-dm.closeCh:
			goto FINI
		}
	}
FINI:
	// if the dialogue manager got an error, all dialogue must be finished in time
	dm.fini()
}

func (dm *dialogueMgr) handlePkt(pkt packet.Packet) {
	switch realPkt := pkt.(type) {
	case *packet.SessionPacket:
		// new negotiating dialogue
		negotiatingID := dm.dialogueIDs.GetID()
		dialogueIDPeersCall := dm.cn.Side() == geminio.InitiatorSide
		dg, err := NewDialogue(dm.cn, dm.multiplexerOpts.opts,
			OptionDialogueNegotiatingID(negotiatingID, dialogueIDPeersCall),
			OptionDialogueDelegate(dm),
			OptionDialogueLogger(dm.log),
			OptionDialoguePacketFactory(dm.pf),
			OptionDialogueMeta(realPkt.SessionData.Meta),
			OptionDialoguePeer(realPkt.SessionData.Peer))
		if err != nil {
			dm.log.Errorf("new dialogue err: %s, clientID: %d", err, dm.cn.ClientID())
			return
		}
		dm.mtx.Lock()
		dm.negotiatingDialogues[negotiatingID] = dg
		// SessionPacket is critical for dialogue negotiation, block to ensure delivery
		// Use recover to handle panic if channel is closed (dialogue might be closing)
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Channel is closed, dialogue is being closed
					dm.log.Debugf("dialogue readInCh is closed (SessionPacket), packet dropped: clientID: %d, negotiatingID: %d, packetID: %d",
						dm.cn.ClientID(), negotiatingID, pkt.ID())
				}
			}()
			dg.readInCh <- pkt
		}()
		dm.mtx.Unlock()
		// Update scheduler's dialogue list to include negotiating dialogue
		// This ensures handshake packets (SessionAckPacket) can be processed by the scheduler
		dm.updateSchedulerDialogueList()
		// Notify scheduler to rebuild cases (non-blocking)
		select {
		case dm.schedulerUpdateCh <- struct{}{}:
		default:
			// Channel is full, notification already pending
		}

	case *packet.SessionAckPacket:
		dm.mtx.RLock()
		dg, ok := dm.negotiatingDialogues[realPkt.NegotiateID()]
		if !ok {
			// TODO we must warn the dialogue initiator
			dm.log.Errorf("clientID: %d, unable to find negotiatingID: %d",
				dm.cn.ClientID(), realPkt.NegotiateID())
			dm.mtx.RUnlock()
			return
		}
		// SessionAckPacket is critical for dialogue negotiation, block to ensure delivery
		// Use recover to handle panic if channel is closed (dialogue might be closing)
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Channel is closed, dialogue is being closed
					dm.log.Debugf("dialogue readInCh is closed (SessionAckPacket), packet dropped: clientID: %d, negotiatingID: %d, packetID: %d",
						dm.cn.ClientID(), realPkt.NegotiateID(), pkt.ID())
				}
			}()
			dg.readInCh <- pkt
		}()
		dm.mtx.RUnlock()

	default:
		dgPkt, ok := pkt.(packet.SessionAbove)
		if !ok {
			dm.log.Errorf("packet don't have dialogueID, clientID: %d, packetID: %d, packetType: %s",
				dm.cn.ClientID(), pkt.ID(), pkt.Type().String())
			return
		}
		dialogueID := dgPkt.SessionID()
		dm.mtx.RLock()
		dg, ok := dm.dialogues[dialogueID]
		if !ok {
			// maybe the dialogue is in negotiating
			dg, ok = dm.negotiatingDialogues[dialogueID]
			if !ok {
				dm.log.Errorf("clientID: %d, unable to find dialogueID: %d, packetID: %d, packetType: %s",
					dm.cn.ClientID(), dialogueID, pkt.ID(), pkt.Type().String())
				dm.mtx.RUnlock()
				return
			}
		}
		dm.log.Tracef("read to dialogue, clientID: %d, dialogueID: %d, packetID: %d, packetType %s",
			dm.cn.ClientID(), dialogueID, pkt.ID(), pkt.Type().String())
		// Check if dialogue is still valid before sending
		// Check dialogueOK to avoid sending to a closing dialogue
		dg.mtx.RLock()
		dialogueOK := dg.dialogueOK
		readInCh := dg.readInCh
		dg.mtx.RUnlock()
		dm.mtx.RUnlock()

		if !dialogueOK {
			// Dialogue is closing, skip sending
			dm.log.Debugf("dialogue is closing, packet dropped: clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
				dm.cn.ClientID(), dialogueID, pkt.ID(), pkt.Type().String())
			return
		}

		// Use recover to handle panic if channel is closed (race condition)
		// This prevents crash when dialogue closes readInCh between our check and send
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Channel is closed, dialogue is being closed
					dm.log.Debugf("dialogue readInCh is closed, packet dropped: clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
						dm.cn.ClientID(), dialogueID, pkt.ID(), pkt.Type().String())
				}
			}()
			// Data packets: block to ensure delivery, no packet loss
			// This ensures all packets are delivered to dialogue, even if it means slower processing
			// The blocking will wait for dialogue to process packets and make room
			readInCh <- pkt
		}()
	}
}

func (dm *dialogueMgr) Close() {
	dm.log.Debugf("dialogue manager is closing, clientID: %d", dm.cn.ClientID())
	wg := sync.WaitGroup{}
	dm.mtx.RLock()
	if !dm.mgrOK {
		dm.mtx.RUnlock()
		return
	}

	wg.Add(len(dm.dialogues))
	wg.Add(len(dm.negotiatingDialogues))

	for _, dg := range dm.dialogues {
		go func(dg *dialogue) {
			defer wg.Done()
			dg.CloseWait()
		}(dg)
	}
	for _, dg := range dm.negotiatingDialogues {
		go func(dg *dialogue) {
			defer wg.Done()
			dg.CloseWait()
		}(dg)
	}
	dm.mtx.RUnlock()

	wg.Wait()
	close(dm.closeCh)
	dm.log.Debugf("dialogue manager closed, clientID: %d", dm.cn.ClientID())
	return
}

// writeScheduler implements fair round-robin scheduling for writes across all dialogues
// This ensures that if one dialogue has a lot of data, it won't starve other dialogues
// The dialogue list is cached and only updated when dialogues are added/removed (via DialogueOnline/DialogueOffline)
// Uses round-robin with non-blocking reads: each dialogue gets one packet per round, ensuring fairness
func (dm *dialogueMgr) writeScheduler() {
	defer dm.writeSchedulerWg.Done()

	for {
		// Check for stop signal first (non-blocking)
		select {
		case <-dm.writeSchedulerStopCh:
			return
		case <-dm.schedulerUpdateCh:
			// Dialogue list updated, reset round-robin index
			atomic.StoreInt64(&dm.schedulerRoundRobinIndex, 0)
			// Drain the channel (non-blocking)
			select {
			case <-dm.schedulerUpdateCh:
			default:
			}
		default:
		}

		// Get cached dialogue list and round-robin index
		dm.schedulerMtx.RLock()
		dialogueChs := dm.schedulerDialogueChs
		dm.schedulerMtx.RUnlock()
		roundRobinIndex := int(atomic.LoadInt64(&dm.schedulerRoundRobinIndex))

		if len(dialogueChs) == 0 {
			// No dialogues available, wait a bit and retry
			select {
			case <-dm.writeSchedulerStopCh:
				return
			case <-dm.schedulerUpdateCh:
				// Dialogue list updated, reset round-robin index
				atomic.StoreInt64(&dm.schedulerRoundRobinIndex, 0)
				// Drain the channel (non-blocking)
				select {
				case <-dm.schedulerUpdateCh:
				default:
				}
				continue
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}

		// Round-robin: try to read one packet from each dialogue starting from current index
		// This ensures fairness: even if one dialogue has many packets, others get a chance
		processed := false
		for i := 0; i < len(dialogueChs); i++ {
			idx := (roundRobinIndex + i) % len(dialogueChs)
			dialogueCh := dialogueChs[idx]

			// Check if dialogue is still valid
			dialogueCh.dg.mtx.RLock()
			dialogueOK := dialogueCh.dg.dialogueOK
			writeOutCh := dialogueCh.ch
			dialogueCh.dg.mtx.RUnlock()

			if !dialogueOK || writeOutCh == nil {
				continue
			}

			// Try non-blocking read from this dialogue's channel
			select {
			case pkt, ok := <-writeOutCh:
				if !ok {
					// Channel closed: writeOutCh was closed, which means fini() was called
					// Update scheduler list to remove this dialogue
					dm.updateSchedulerDialogueList()
					// Reset round-robin index after list update
					atomic.StoreInt64(&dm.schedulerRoundRobinIndex, 0)
					processed = true
					break
				}

				// Check if dialogue is still valid before processing
				dialogueCh.dg.mtx.RLock()
				dialogueOK = dialogueCh.dg.dialogueOK
				dialogueCh.dg.mtx.RUnlock()

				if !dialogueOK {
					// Dialogue is closing, skip this packet
					// Move to next dialogue in round-robin
					atomic.StoreInt64(&dm.schedulerRoundRobinIndex, int64((idx+1)%len(dialogueChs)))
					processed = true
					break
				}

				// Process the packet directly (same as what writePkt() would do)
				ret := dialogueCh.dg.handleOut(pkt)
				switch ret {
				case iodefine.IONewPassive, iodefine.IOSuccess:
					// Success, move to next dialogue in round-robin
					atomic.StoreInt64(&dm.schedulerRoundRobinIndex, int64((idx+1)%len(dialogueChs)))
					processed = true
				case iodefine.IOClosed, iodefine.IOErr:
					// Error or closed: call fini() to clean up dialogue resources
					dm.log.Debugf("dialogue write error in scheduler, calling fini, clientID: %d, dialogueID: %d, ret: %v",
						dm.cn.ClientID(), dialogueCh.dg.dialogueID, ret)
					dialogueCh.dg.finiOnce.Do(dialogueCh.dg.fini)
					// Update scheduler list to remove this dialogue
					dm.updateSchedulerDialogueList()
					// Reset round-robin index after list update
					atomic.StoreInt64(&dm.schedulerRoundRobinIndex, 0)
					processed = true
				}
				// Break after processing one packet to ensure fairness
				break
			default:
				// No data in this channel, continue to next dialogue
				continue
			}
		}

		if processed {
			// Successfully processed a packet, continue to next round
			continue
		}

		// No dialogue had data available (all channels empty)
		// Use reflect.Select to block until at least one channel has data
		// Build select cases for all dialogue channels + stop channel + update notification channel
		cases := make([]reflect.SelectCase, 0, len(dialogueChs)+2)
		// Add stop channel first (index 0)
		cases = append(cases, reflect.SelectCase{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(dm.writeSchedulerStopCh),
		})
		// Add update notification channel (index 1)
		cases = append(cases, reflect.SelectCase{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(dm.schedulerUpdateCh),
		})
		// Add all dialogue channels
		validDialogueIndices := make([]int, 0, len(dialogueChs))
		for i := range dialogueChs {
			// Check if dialogue is still valid
			dialogueChs[i].dg.mtx.RLock()
			dialogueOK := dialogueChs[i].dg.dialogueOK
			writeOutCh := dialogueChs[i].ch
			dialogueChs[i].dg.mtx.RUnlock()

			if !dialogueOK || writeOutCh == nil {
				continue
			}

			cases = append(cases, reflect.SelectCase{
				Dir:  reflect.SelectRecv,
				Chan: reflect.ValueOf(writeOutCh),
			})
			validDialogueIndices = append(validDialogueIndices, i)
		}

		if len(cases) == 2 {
			// Only stop channel and update channel, no valid dialogues, wait and retry
			select {
			case <-dm.writeSchedulerStopCh:
				return
			case <-dm.schedulerUpdateCh:
				// Dialogue list updated, reset round-robin index
				atomic.StoreInt64(&dm.schedulerRoundRobinIndex, 0)
				// Drain the channel (non-blocking)
				select {
				case <-dm.schedulerUpdateCh:
				default:
				}
				continue
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}

		// Block until at least one channel has data (or stop signal or update notification)
		chosen, value, ok := reflect.Select(cases)
		if chosen == 0 {
			// Stop channel was selected
			return
		}
		if chosen == 1 {
			// Update notification channel was selected - reset round-robin index
			atomic.StoreInt64(&dm.schedulerRoundRobinIndex, 0)
			// Drain the channel (non-blocking)
			select {
			case <-dm.schedulerUpdateCh:
			default:
			}
			continue
		}

		// A dialogue channel was selected (chosen >= 2, so subtract 2 for dialogue index)
		dialogueIdx := validDialogueIndices[chosen-2]
		if !ok {
			// Channel closed: writeOutCh was closed, which means fini() was called
			// Update scheduler list to remove this dialogue
			dm.updateSchedulerDialogueList()
			// Reset round-robin index after list update
			atomic.StoreInt64(&dm.schedulerRoundRobinIndex, 0)
			continue
		}

		// Process packet directly: call handleOut() to avoid blocking on schedulerCh
		pkt := value.Interface().(packet.Packet)
		dg := dialogueChs[dialogueIdx].dg

		// Check if dialogue is still valid before processing
		dg.mtx.RLock()
		dialogueOK := dg.dialogueOK
		dg.mtx.RUnlock()

		if !dialogueOK {
			// Dialogue is closing, skip this packet
			// Move to next dialogue in round-robin
			atomic.StoreInt64(&dm.schedulerRoundRobinIndex, int64((dialogueIdx+1)%len(dialogueChs)))
			continue
		}

		// Process the packet directly (same as what writePkt() would do)
		ret := dg.handleOut(pkt)
		switch ret {
		case iodefine.IONewPassive, iodefine.IOSuccess:
			// Success, move to next dialogue in round-robin
			atomic.StoreInt64(&dm.schedulerRoundRobinIndex, int64((dialogueIdx+1)%len(dialogueChs)))
		case iodefine.IOClosed, iodefine.IOErr:
			// Error or closed: call fini() to clean up dialogue resources
			dm.log.Debugf("dialogue write error in scheduler, calling fini, clientID: %d, dialogueID: %d, ret: %v",
				dm.cn.ClientID(), dg.dialogueID, ret)
			dg.finiOnce.Do(dg.fini)
			// Update scheduler list to remove this dialogue
			dm.updateSchedulerDialogueList()
			// Reset round-robin index after list update
			atomic.StoreInt64(&dm.schedulerRoundRobinIndex, 0)
		}
	}
}

func (dm *dialogueMgr) fini() {
	dm.log.Debugf("dialogue manager finishing, clientID: %d", dm.cn.ClientID())

	// Stop write scheduler first
	if dm.writeSchedulerStopCh != nil {
		close(dm.writeSchedulerStopCh)
		dm.writeSchedulerWg.Wait()
	}

	dm.mtx.Lock()
	defer dm.mtx.Unlock()

	// collect conn status
	dm.mgrOK = false
	// collect all dialogues
	for id, dg := range dm.dialogues {
		// cause the dialogue io err
		dg.closeIO()
		delete(dm.dialogues, id)
	}
	for id, dg := range dm.negotiatingDialogues {
		// cause the dialogue io err
		dg.closeIO()
		delete(dm.negotiatingDialogues, id)
	}

	// collect id
	dm.dialogueIDs.Close()
	dm.dialogueIDs = nil
	// collect channels
	if !dm.dialogueAcceptChOutside && dm.dialogueAcceptCh != nil {
		close(dm.dialogueAcceptCh)
	}
	if !dm.dialogueClosedChOutside && dm.dialogueClosedCh != nil {
		close(dm.dialogueClosedCh)
	}
	// dm.dialogueAcceptCh, dm.dialogueClosedCh = nil, nil
	// collect timer
	if dm.tmrOwner == dm {
		dm.tmr.Close()
	}
	dm.tmr = nil

	dm.log.Debugf("dialogue manager finished, clientID: %d", dm.cn.ClientID())
}
