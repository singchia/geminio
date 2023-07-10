package multiplexer

import (
	"io"
	"sync"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/go-timer/v2"
)

type multiplexerOpts struct {
	// global client ID factory, set nil at client side
	dialogueIDs id.IDFactory
	// packet factory
	pf *packet.PacketFactory
	// logger
	log log.Logger
	// delegate
	dlgt Delegate
	// timer
	tmr        timer.Timer
	tmrOutside bool
	// for outside usage
	dialogueAcceptCh        chan *dialogue
	dialogueAcceptChOutside bool

	dialogueClosedCh        chan *dialogue
	dialogueClosedChOutside bool
}

type dialogueMgr struct {
	// under layer
	cn conn.Conn
	// options
	multiplexerOpts

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

type MultiplexerOption func(*dialogueMgr)

func OptionMultiplexerAcceptDialogue() MultiplexerOption {
	return func(dm *dialogueMgr) {
		dm.dialogueAcceptCh = make(chan *dialogue, 128)
		dm.dialogueAcceptChOutside = false
	}
}

func OptionMultiplexerClosedDialogue() MultiplexerOption {
	return func(dm *dialogueMgr) {
		dm.dialogueAcceptCh = make(chan *dialogue, 128)
		dm.dialogueAcceptChOutside = false
	}
}

func OptionDelegate(dlgt Delegate) MultiplexerOption {
	return func(dm *dialogueMgr) {
		dm.dlgt = dlgt
	}
}

func OptionPacketFactory(pf *packet.PacketFactory) MultiplexerOption {
	return func(dm *dialogueMgr) {
		dm.pf = pf
	}
}

func OptionLogger(log log.Logger) MultiplexerOption {
	return func(dm *dialogueMgr) {
		dm.log = log
	}
}

func OptionTimer(tmr timer.Timer) MultiplexerOption {
	return func(dm *dialogueMgr) {
		dm.tmr = tmr
		dm.tmrOutside = true
	}
}

func NewMultiplexer(cn conn.Conn, opts ...MultiplexerOption) (*dialogueMgr, error) {
	dm := &dialogueMgr{
		cn:                   cn,
		mgrOK:                true,
		dialogues:            make(map[uint64]*dialogue),
		negotiatingDialogues: make(map[uint64]*dialogue),
		closeCh:              make(chan struct{}),
	}
	// dialogue id counter
	if dm.cn.Side() == conn.ServerSide {
		dm.dialogueIDs = id.NewIDCounter(id.Even)
		dm.dialogueIDs.ReserveID(packet.SessionID1)
	} else {
		dm.dialogueIDs = id.NewIDCounter(id.Odd)
		dm.dialogueIDs.ReserveID(packet.SessionID1)
	}
	// options
	for _, opt := range opts {
		opt(dm)
	}
	// sync hub
	if !dm.tmrOutside {
		dm.tmr = timer.NewTimer()
	}
	// log
	if dm.log == nil {
		dm.log = log.DefaultLog
	}
	// add default dialogue
	dg, err := NewDialogue(cn,
		OptionDialogueState(SESSIONED),
		OptionDialogueDelegate(dm))
	if err != nil {
		dm.log.Errorf("new dialogue err: %s, clientID: %d, dialogueID: %d",
			err, cn.ClientID(), packet.SessionID1)
		return nil, err
	}
	dg.dialogueID = packet.SessionID1
	dm.defaultDialogue = dg
	dm.dialogues[packet.SessionID1] = dg
	go dm.readPkt()
	return dm, nil
}

func (dm *dialogueMgr) DialogueOnline(dg DialogueDescriber) error {
	dm.log.Debugf("dialogue online, clientID: %d, add dialogueID: %d", dm.cn.ClientID(), dg.DialogueID())
	dm.mtx.Lock()
	defer dm.mtx.Unlock()

	if !dm.mgrOK {
		return ErrOperationOnClosedMultiplexer
	}
	// remove from the negotiating dialogues, and add to ready dialogues.
	_, ok := dm.negotiatingDialogues[dg.NegotiatingID()]
	if ok {
		delete(dm.negotiatingDialogues, dg.NegotiatingID())
	} else {
		if dm.dlgt != nil {
			dm.dlgt.DialogueOnline(dg)
		}
		if dm.dialogueAcceptCh != nil {
			// this must not be blocked, or else the whole system will stop
			dm.dialogueAcceptCh <- dg.(*dialogue)
		}
	}
	dm.dialogues[dg.DialogueID()] = dg.(*dialogue)
	return nil
}

func (dm *dialogueMgr) DialogueOffline(dg DialogueDescriber) error {
	dm.log.Debugf("dialogue offline, clientID: %d, del dialogueID: %d", dm.cn.ClientID(), dg.DialogueID())
	dm.mtx.Lock()
	defer dm.mtx.Unlock()

	dg, ok := dm.dialogues[dg.DialogueID()]
	if ok {
		delete(dm.dialogues, dg.DialogueID())
		if dm.dlgt != nil {
			dm.dlgt.DialogueOffline(dg)
		}
		return nil
	}
	// unsucceed dialogue
	return ErrDialogueNotFound
}

func (dm *dialogueMgr) getID() uint64 {
	if dm.cn.Side() == conn.ClientSide {
		return packet.SessionIDNull
	}
	return dm.dialogueIDs.GetID()
}

// OpenDialogue blocks until success or failed
func (dm *dialogueMgr) OpenDialogue(meta []byte) (Dialogue, error) {
	dm.mtx.RLock()
	if !dm.mgrOK {
		dm.mtx.RUnlock()
		return nil, ErrOperationOnClosedMultiplexer
	}
	dm.mtx.RUnlock()

	negotiatingID := dm.dialogueIDs.GetID()
	dialogueIDPeersCall := dm.cn.Side() == conn.ClientSide
	dg, err := NewDialogue(dm.cn,
		OptionDialogueNegotiatingID(negotiatingID, dialogueIDPeersCall),
		OptionDialogueDelegate(dm))
	if err != nil {
		dm.log.Errorf("new dialogue err: %s, clientID: %d", err, dm.cn.ClientID())
		return nil, err
	}
	dm.mtx.Lock()
	dm.negotiatingDialogues[negotiatingID] = dg
	dm.mtx.Unlock()
	// Open only happends at client side
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
	defer dm.mtx.Unlock()

	delete(dm.negotiatingDialogues, negotiatingID)
	dm.dialogues[dg.dialogueID] = dg
	if !dm.mgrOK {
		// the logic on negotiatingDialogues is tricky, take care of it.
		delete(dm.dialogues, dg.dialogueID)
		// !mgrOK only happens after dialogueMgr fini, so fini the dialogue
		dg.fini()
		return nil, ErrOperationOnClosedMultiplexer
	}
	return dg, nil
}

// AcceptDialogue blocks until success or failed
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

// ClosedDialogue blocks until success or failed
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
		dialogueIDPeersCall := dm.cn.Side() == conn.ClientSide
		dg, err := NewDialogue(dm.cn,
			OptionDialogueNegotiatingID(negotiatingID, dialogueIDPeersCall),
			OptionDialogueDelegate(dm))
		if err != nil {
			dm.log.Errorf("new dialogue err: %s, clientID: %d", err, dm.cn.ClientID())
			return
		}
		dm.mtx.Lock()
		dm.negotiatingDialogues[negotiatingID] = dg
		dm.mtx.Unlock()
		dg.readInCh <- pkt

	case *packet.SessionAckPacket:
		dm.mtx.RLock()
		dg, ok := dm.negotiatingDialogues[realPkt.NegotiateID()]
		dm.mtx.RUnlock()
		if !ok {
			// TODO we must warn the dialogue initiator
			dm.log.Errorf("clientID: %d, unable to find negotiatingID: %d",
				dm.cn.ClientID(), realPkt.NegotiateID())
			return
		}
		// TODO do we need handle the packet in time? before data or dismiss coming.
		dg.readInCh <- pkt

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
		dm.mtx.RUnlock()
		if !ok {
			dm.log.Errorf("clientID: %d, unable to find dialogueID: %d, packetID: %d, packetType: %s",
				dm.cn.ClientID(), dialogueID, pkt.ID(), pkt.Type().String())
			return
		}

		dm.log.Tracef("write to dialogue, clientID: %d, dialogueID: %d, packetID: %d, read %s",
			dm.cn.ClientID(), dialogueID, pkt.ID(), pkt.Type().String())
		dg.readInCh <- pkt
	}
}

func (dm *dialogueMgr) Close() {
	dm.log.Debugf("dialogue manager is closing, clientID: %d", dm.cn.ClientID())
	wg := sync.WaitGroup{}
	dm.mtx.RLock()
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
		close(dg.readInCh)
		delete(dm.dialogues, id)
	}
	for id, dg := range dm.negotiatingDialogues {
		// cause the dialogue io err
		close(dg.readInCh)
		delete(dm.dialogues, id)
	}

	// collect timer
	if !dm.tmrOutside {
		dm.tmr.Close()
	}
	dm.tmr = nil
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
	dm.dialogueAcceptCh, dm.dialogueClosedCh = nil, nil

	dm.log.Debugf("dialogue manager finished, clientID: %d", dm.cn.ClientID())
}
