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
	dialogueAcceptChOutsite bool

	dialogueClosedCh        chan *dialogue
	dialogueClosedChOutsite bool
}

type multiplexer struct {
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

type MultiplexerOption func(*multiplexer)

func OptionMultiplexerAcceptDialogue() MultiplexerOption {
	return func(mp *multiplexer) {
		mp.dialogueAcceptCh = make(chan *dialogue, 128)
		mp.dialogueAcceptChOutsite = false
	}
}

func OptionMultiplexerClosedDialogue() MultiplexerOption {
	return func(mp *multiplexer) {
		mp.dialogueAcceptCh = make(chan *dialogue, 128)
		mp.dialogueAcceptChOutsite = false
	}
}

func OptionDelegate(dlgt Delegate) MultiplexerOption {
	return func(mp *multiplexer) {
		mp.dlgt = dlgt
	}
}

func OptionPacketFactory(pf *packet.PacketFactory) MultiplexerOption {
	return func(mp *multiplexer) {
		mp.pf = pf
	}
}

func OptionLogger(log log.Logger) MultiplexerOption {
	return func(mp *multiplexer) {
		mp.log = log
	}
}

func OptionTimer(tmr timer.Timer) MultiplexerOption {
	return func(mp *multiplexer) {
		mp.tmr = tmr
		mp.tmrOutside = true
	}
}

func NewMultiplexer(cn conn.Conn, opts ...MultiplexerOption) (*multiplexer, error) {
	mp := &multiplexer{
		cn:    cn,
		mgrOK: true,
	}
	// dialogue id counter
	if mp.cn.Side() == conn.ServerSide {
		mp.dialogueIDs = id.NewIDCounter(id.Even)
		mp.dialogueIDs.ReserveID(packet.SessionID1)
	}
	// options
	for _, opt := range opts {
		opt(mp)
	}
	// sync hub
	if !mp.tmrOutside {
		mp.tmr = timer.NewTimer()
	}
	// log
	if mp.log == nil {
		mp.log = log.DefaultLog
	}
	// add default dialogue
	dg, err := NewDialogue(cn,
		OptionDialogueState(SESSIONED))
	if err != nil {
		mp.log.Errorf("new dialogue err: %s, clientID: %d, dialogueID: %d",
			err, cn.ClientID(), packet.SessionID1)
		return nil, err
	}
	dg.dialogueID = packet.SessionID1
	mp.defaultDialogue = dg
	mp.dialogues[packet.SessionID1] = dg
	go mp.readPkt()
	return mp, nil
}

func (mp *multiplexer) DialogueOnline(dg *dialogue, meta []byte) error {
	mp.mtx.Lock()
	defer mp.mtx.Unlock()

	if !mp.mgrOK {
		return ErrOperationOnClosedMultiplexer
	}
	// remove from the negotiating dialogues, and add to ready dialogues.
	_, ok := mp.negotiatingDialogues[dg.negotiatingID]
	if ok {
		delete(mp.negotiatingDialogues, dg.negotiatingID)
	} else {
		if mp.dlgt != nil {
			mp.dlgt.DialogueOnline(dg)
		}
		if mp.dialogueAcceptCh != nil {
			// this must not be blocked, or else the whole system will stop
			mp.dialogueAcceptCh <- dg
		}
	}
	mp.dialogues[dg.dialogueID] = dg
	return nil
}

func (mp *multiplexer) DialogueOffline(dg *dialogue, meta []byte) error {
	mp.log.Debugf("clientID: %d, del dialogueID: %d", mp.cn.ClientID, dg.DialogueID)
	mp.mtx.Lock()
	defer mp.mtx.Unlock()

	dg, ok := mp.dialogues[dg.dialogueID]
	if ok {
		delete(mp.dialogues, dg.dialogueID)
		if mp.dlgt != nil {
			mp.dlgt.DialogueOffline(dg)
		}
	}
	// unsucceed dialogue
	return ErrDialogueNotFound
}

func (mp *multiplexer) getID() uint64 {
	if mp.cn.Side() == conn.ClientSide {
		return packet.SessionIDNull
	}
	return mp.dialogueIDs.GetID()
}

func (mp *multiplexer) Close() error {
	mp.log.Debugf("dialogue manager is closing, clientID: %d", mp.cn.ClientID())
	mp.mtx.RLock()
	defer mp.mtx.RUnlock()

	wg := sync.WaitGroup{}
	wg.Add(len(mp.dialogues))
	wg.Add(len(mp.negotiatingDialogues))

	for _, dg := range mp.dialogues {
		go func(dg *dialogue) {
			defer wg.Done()
			dg.CloseWait()
		}(dg)
	}
	for _, dg := range mp.negotiatingDialogues {
		go func(dg *dialogue) {
			defer wg.Done()
			dg.CloseWait()
		}(dg)
	}
	close(mp.closeCh)
	mp.log.Debugf("dialogue manager closed, clientID: %d", mp.cn.ClientID)
	return nil
}

// OpenDialogue blocks until success or failed
func (mp *multiplexer) OpenDialogue(meta []byte) (Dialogue, error) {
	mp.mtx.RLock()
	if !mp.mgrOK {
		mp.mtx.RUnlock()
		return nil, ErrOperationOnClosedMultiplexer
	}
	mp.mtx.RUnlock()

	negotiatingID := mp.dialogueIDs.GetID()
	dialogueIDPeersCall := mp.cn.Side() == conn.ClientSide
	dg, err := NewDialogue(mp.cn, OptionDialogueNegotiatingID(negotiatingID, dialogueIDPeersCall))
	if err != nil {
		mp.log.Errorf("new dialogue err: %s, clientID: %d", err, mp.cn.ClientID())
		return nil, err
	}
	mp.mtx.Lock()
	mp.negotiatingDialogues[negotiatingID] = dg
	mp.mtx.Unlock()
	// Open only happends at client side
	// Open take times, shouldn't be locked
	err = dg.open()
	if err != nil {
		mp.log.Errorf("dialogue open err: %s, clientID: %d, negotiatingID: %d", err, mp.cn.ClientID(), dg.negotiatingID)
		mp.mtx.Lock()
		delete(mp.negotiatingDialogues, negotiatingID)
		mp.mtx.Unlock()
		return nil, err
	}
	mp.mtx.Lock()
	defer mp.mtx.Unlock()
	if !mp.mgrOK {
		// the logic on negotiatingDialogues is tricky, take care of it.
		delete(mp.negotiatingDialogues, negotiatingID)
		dg.Close()
		return nil, ErrOperationOnClosedMultiplexer
	}
	return dg, nil
}

// AcceptDialogue blocks until success or failed
func (mp *multiplexer) AcceptDialogue() (Dialogue, error) {
	if mp.dialogueAcceptCh == nil {
		return nil, ErrAcceptChNotEnabled
	}
	dg, ok := <-mp.dialogueAcceptCh
	if !ok {
		return nil, io.EOF
	}
	return dg, nil
}

// ClosedDialogue blocks until success or failed
func (mp *multiplexer) ClosedDialogue() (Dialogue, error) {
	if mp.dialogueClosedCh == nil {
		return nil, ErrClosedChNotEnabled
	}
	dg, ok := <-mp.dialogueClosedCh
	if !ok {
		return nil, io.EOF
	}
	return dg, nil
}

func (mp *multiplexer) readPkt() {
	for {
		select {
		case pkt, ok := <-mp.cn.ChannelRead():
			if !ok {
				mp.log.Debugf("dialogue mgr read done, clientID: %d", mp.cn.ClientID)
				goto FINI
			}
			mp.handlePkt(pkt)
		case <-mp.closeCh:
			goto FINI
		}
	}
FINI:
	// if the dialogue manager got an error, all dialogue must be finished in time
	mp.fini()
}

func (mp *multiplexer) handlePkt(pkt packet.Packet) {
	switch realPkt := pkt.(type) {
	case *packet.SessionPacket:
		// new negotiating dialogue
		negotiatingID := mp.dialogueIDs.GetID()
		dialogueIDPeersCall := mp.cn.Side() == conn.ClientSide
		dg, err := NewDialogue(mp.cn, OptionDialogueNegotiatingID(negotiatingID, dialogueIDPeersCall))
		if err != nil {
			mp.log.Errorf("new dialogue err: %s, clientID: %d", err, mp.cn.ClientID())
			return
		}
		mp.mtx.Lock()
		mp.negotiatingDialogues[negotiatingID] = dg
		mp.mtx.Unlock()
		dg.readInCh <- pkt

	case *packet.SessionAckPacket:
		mp.mtx.RLock()
		dg, ok := mp.negotiatingDialogues[realPkt.NegotiateID]
		mp.mtx.RUnlock()
		if !ok {
			// TODO we must warn the dialogue initiator
			mp.log.Errorf("clientID: %d, unable to find negotiating dialogueID: %d",
				mp.cn.ClientID, pkt.ID())
			return
		}
		dg.readInCh <- pkt

	default:
		dgPkt, ok := pkt.(packet.SessionAbove)
		if !ok {
			mp.log.Errorf("packet doedg't have dialogueID, clientID: %d, negotiatingID: %d, packetType: %s",
				mp.cn.ClientID, pkt.ID(), pkt.Type().String())
			return
		}
		dialogueID := dgPkt.SessionID()
		mp.mtx.RLock()
		dg, ok := mp.negotiatingDialogues[dialogueID]
		mp.mtx.RUnlock()
		if !ok {
			mp.log.Errorf("clientID: %d, unable to find dialogueID: %d, negotiatingID: %d, packetType: %s",
				mp.cn.ClientID, dialogueID, pkt.ID(), pkt.Type().String())
			return
		}

		mp.log.Tracef("clientID: %d, dialogueID: %d, negotiatingID: %d, read %s",
			mp.cn.ClientID, dialogueID, pkt.ID(), pkt.Type().String())
		dg.readInCh <- pkt
	}
}

func (mp *multiplexer) fini() {
	mp.log.Debugf("dialogue manager finishing, clientID: %d", mp.cn.ClientID)

	mp.mtx.Lock()
	defer mp.mtx.Unlock()

	// collect conn status
	mp.mgrOK = false
	// collect all dialogues
	for id, dg := range mp.dialogues {
		// cause the dialogue io err
		close(dg.readInCh)
		delete(mp.dialogues, id)
	}
	for id, dg := range mp.negotiatingDialogues {
		// cause the dialogue io err
		close(dg.readInCh)
		delete(mp.dialogues, id)
	}

	// collect timer
	if !mp.tmrOutside {
		mp.tmr.Close()
	}
	mp.tmr = nil
	// collect id
	mp.dialogueIDs.Close()
	mp.dialogueIDs = nil
	// collect channels
	if !mp.dialogueAcceptChOutsite {
		close(mp.dialogueAcceptCh)
	}
	if !mp.dialogueClosedChOutsite {
		close(mp.dialogueClosedCh)
	}
	mp.dialogueAcceptCh, mp.dialogueClosedCh = nil, nil

	mp.log.Debugf("dialogue manager finished, clientID: %d", mp.cn.ClientID)
}
