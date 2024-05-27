package multiplexer

import (
	"errors"
	"io"
	"sync"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/delegate"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
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
	// rolling up
	go dm.readPkt()
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
	dm.dialogues[dg.DialogueID()] = dg.(*dialogue)
	if dm.dlgt != nil {
		dm.dlgt.DialogueOnline(dg)
	}
	// notify outside that a dialogue is accepting
	if dm.dialogueAcceptFn != nil {
		dm.dialogueAcceptFn(dg.(Dialogue))

	} else if dm.dialogueAcceptCh != nil {
		// this must not be blocked, or else the whole system will stop
		dm.dialogueAcceptCh <- dg.(*dialogue)
	}
	return nil
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
		dg.fini()
		return nil, ErrOperationOnClosedMultiplexer
	}
	// the logic on negotiatingDialogues is tricky, be care of it.
	dm.dialogues[dg.dialogueID] = dg
	dm.mtx.Unlock()
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
		dg.readInCh <- pkt
		dm.mtx.Unlock()

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
		// TODO do we need handle the packet in time? before data or dismiss coming.
		dg.readInCh <- pkt
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
		dg.readInCh <- pkt
		dm.mtx.RUnlock()
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

func (dm *dialogueMgr) fini() {
	dm.log.Debugf("dialogue manager finishing, clientID: %d", dm.cn.ClientID())

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
