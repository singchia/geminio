package multiplexer

import (
	"io"
	"strconv"
	"sync"

	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
)

// For server side only
type dialogueHub struct {
	// options
	multiplexerOpts

	// close channel
	closeCh chan struct{}

	// dialogues
	dialogueIDs id.IDFactory

	// mtx protect follows
	mtx                  sync.RWMutex
	hubOK                bool
	defaultDialogues     map[string]*dialogue
	dialogues            map[string]*dialogue
	negotiatingDialogues map[string]*dialogue

	conns map[uint64]conn.Conn

	// io
	readInCh chan packet.Packet
}

func (dh *dialogueHub) DialogueOnline(dg DialogueDescriber) error {
	dh.log.Debugf("dialogue online, clientID: %d, add dialogueID: %d", dg.ClientID(), dg.DialogueID())
	dh.mtx.Lock()
	defer dh.mtx.Unlock()

	if !dh.hubOK {
		return ErrOperationOnClosedMultiplexer
	}
	key := dialogueKey(dg.ClientID(), dg.NegotiatingID())
	// remove from the negotiating dialogues, and add to ready dialogues.
	_, ok := dh.negotiatingDialogues[key]
	if ok {
		delete(dh.negotiatingDialogues, key)
	}
	key = dialogueKey(dg.ClientID(), dg.DialogueID())
	dh.dialogues[key] = dg.(*dialogue)
	if dh.dlgt != nil {
		dh.dlgt.DialogueOnline(dg)
	}
	if dh.dialogueAcceptCh != nil {
		// this must not be blocked, or else the whole system will stop
		dh.dialogueAcceptCh <- dg.(*dialogue)
	}
	return nil
}

func (dh *dialogueHub) DialogueOffline(dg DialogueDescriber) error {
	dh.log.Debugf("dialogue offline, clientID: %d, del dialogueID: %d", dg.ClientID(), dg.DialogueID())
	dh.mtx.Lock()
	defer dh.mtx.Unlock()

	key := dialogueKey(dg.ClientID(), dg.DialogueID())
	dg, ok := dh.dialogues[key]
	if ok {
		delete(dh.dialogues, key)
		if dh.dlgt != nil {
			dh.dlgt.DialogueOffline(dg)
		}
		return nil
	}
	// unsucceed dialogue
	return ErrDialogueNotFound
}

func (dh *dialogueHub) ConnOnline(cn conn.ConnDescriber) error {
	return nil
}

func (dh *dialogueHub) ConnOffline(cn conn.ConnDescriber) error {
	return nil
}

func (dh *dialogueHub) OpenDialogue(clientID uint64, meta []byte) (Dialogue, error) {
	dh.mtx.RLock()
	if !dh.hubOK {
		dh.mtx.RUnlock()
		return nil, ErrOperationOnClosedMultiplexer
	}
	dh.mtx.RUnlock()

	cn, ok := dh.getConn(clientID)
	if !ok {
		return nil, ErrConnNotFound
	}
	negotiatingID := dh.dialogueIDs.GetID()
	dg, err := NewDialogue(cn,
		OptionDialogueNegotiatingID(negotiatingID, false),
		OptionDialogueDelegate(dh))
	if err != nil {
		dh.log.Errorf("new dialogue err: %s, clientID: %d", err, clientID)
		return nil, err
	}
	key := dialogueKey(clientID, negotiatingID)
	dh.mtx.Lock()
	dh.negotiatingDialogues[key] = dg
	dh.mtx.Unlock()
	// Open take times, shouldn't be locked
	err = dg.open()
	if err != nil {
		dh.log.Errorf("dialogue open err: %s, clientID: %d, negotiatingID: %d",
			err, clientID, negotiatingID)
		dh.mtx.Lock()
		delete(dh.negotiatingDialogues, key)
		dh.mtx.Unlock()
		return nil, err
	}

	dh.mtx.Lock()
	defer dh.mtx.Unlock()

	delete(dh.negotiatingDialogues, key)

	if !dh.hubOK {
		// !hubOK only happends after dialogueMgr fini, so fini the dialogue
		dg.fini()
		return nil, ErrOperationOnClosedMultiplexer
	}

	key = dialogueKey(clientID, dg.dialogueID)
	dh.dialogues[key] = dg
	return dg, nil
}

// AcceptDialogue blocks until success or end
func (dh *dialogueHub) AcceptDialogue() (Dialogue, error) {
	if dh.dialogueAcceptCh == nil {
		return nil, ErrAcceptChNotEnabled
	}
	dg, ok := <-dh.dialogueAcceptCh
	if !ok {
		return nil, io.EOF
	}
	return dg, nil
}

func (dh *dialogueHub) ClosedDialogue() (Dialogue, error) {
	if dh.dialogueClosedCh == nil {
		return nil, ErrClosedChNotEnabled
	}
	dg, ok := <-dh.dialogueClosedCh
	if !ok {
		return nil, io.EOF
	}
	return dg, nil
}

func (dh *dialogueHub) readPkt() {
	for {
		select {
		case pkt, ok := <-dh.readInCh:
			if !ok {
				dh.log.Debugf("dialogue hub read done")
				goto FINI
			}
			dh.handlePkt(pkt)
		case <-dh.closeCh:
			// the closeCh should be the last part of dialogue hub
			goto FINI
		}
	}
FINI:
}

func (dh *dialogueHub) getConn(clientID uint64) (conn.Conn, bool) {
	dh.mtx.RLock()
	defer dh.mtx.RUnlock()

	cn, ok := dh.conns[clientID]
	return cn, ok
}

func (dh *dialogueHub) handlePkt(pkt packet.Packet) {
	switch realPkt := pkt.(type) {
	case *packet.SessionPacket:
		clientID := realPkt.ClientID()
		cn, ok := dh.getConn(clientID)
		if !ok {
			dh.log.Errorf("unable to find conn with clientID: %d", clientID)
			return
		}
		// new negotiating dialogue
		negotiatingID := dh.dialogueIDs.GetID()
		dg, err := NewDialogue(cn, OptionDialogueNegotiatingID(negotiatingID, false),
			OptionDialogueDelegate(dh))
		if err != nil {
			dh.log.Errorf("new dialogue err: %s, clientID: %d", err, clientID)
			return
		}
		key := dialogueKey(clientID, negotiatingID)
		dh.mtx.Lock()
		dh.negotiatingDialogues[key] = dg
		dh.mtx.Unlock()
		dg.readInCh <- pkt

	case *packet.SessionAckPacket:
		clientID := realPkt.ClientID()
		key := dialogueKey(clientID, realPkt.NegotiateID())
		dh.mtx.RLock()
		dg, ok := dh.negotiatingDialogues[key]
		dh.mtx.RUnlock()
		if !ok {
			// TODO we must warn the dialogue initiator in stead of timeout
			dh.log.Errorf("clientID: %d, unable to find negotiatingID: %d",
				clientID, realPkt.NegotiateID())
			return
		}
		dg.readInCh <- pkt

	default:
		clientID := realPkt.(packet.ConnAbove).ClientID()
		dgPkt, ok := pkt.(packet.SessionAbove)
		if !ok {
			dh.log.Errorf("packet don't have dialogueID, clientID: %d, packetID: %d, packetType: %s",
				pkt.(packet.ConnAbove).ClientID(), pkt.ID(), pkt.Type().String())
			return
		}
		dialogueID := dgPkt.SessionID()
		key := dialogueKey(clientID, dialogueID)
		dh.mtx.RLock()
		dg, ok := dh.dialogues[key]
		dh.mtx.RUnlock()
		if !ok {
			dh.log.Errorf("clientID: %d, unable to find dialogueID: %d, packetID: %d, packetType: %s",
				clientID, dialogueID, pkt.ID(), pkt.Type().String())
			return
		}

		dh.log.Tracef("write to dialogue, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			clientID, dialogueID, pkt.ID(), pkt.Type().String())
		dg.readInCh <- pkt
	}
}

func dialogueKey(clientID uint64, dialogueID uint64) string {
	return strconv.FormatUint(clientID, 10) + "-" + strconv.FormatUint(dialogueID, 10)
}

func (dh *dialogueHub) Close() {
	dh.log.Debugf("dialogue hubor is closing")

	wg := sync.WaitGroup{}
	dh.mtx.Lock()
	wg.Add(len(dh.dialogues))
	wg.Add(len(dh.negotiatingDialogues))

	for _, dg := range dh.dialogues {
		go func(dg *dialogue) {
			defer wg.Done()
			dg.CloseWait()
		}(dg)
	}
	for _, dg := range dh.negotiatingDialogues {
		go func(dg *dialogue) {
			defer wg.Done()
			dg.CloseWait()
		}(dg)
	}
	dh.mtx.RUnlock()

	wg.Wait()
	close(dh.closeCh)
	dh.log.Debugf("dialogue hubor closed")
	return
}

func (dh *dialogueHub) fini() {
	dh.log.Debugf("dialogue habor finishing")

	dh.mtx.Lock()
	defer dh.mtx.Unlock()

	// collect conn status
	dh.hubOK = false
	for key := range dh.conns {
		delete(dh.conns, key)
	}
	// collect all dialogues
	for id, dg := range dh.dialogues {
		// cause the dialogue io err
		close(dg.readInCh)
		delete(dh.dialogues, id)
	}
	for id, dg := range dh.negotiatingDialogues {
		// cause the dialogue io err
		close(dg.readInCh)
		delete(dh.dialogues, id)
	}

	// collect timer
	if !dh.tmrOutside {
		dh.tmr.Close()
	}
	dh.tmr = nil
	// collect id
	dh.dialogueIDs.Close()
	dh.dialogueIDs = nil
	// collect channels
	if !dh.dialogueAcceptChOutside && dh.dialogueAcceptCh != nil {
		close(dh.dialogueAcceptCh)
	}
	if !dh.dialogueClosedChOutside && dh.dialogueClosedCh != nil {
		close(dh.dialogueClosedCh)
	}
	dh.dialogueAcceptCh, dh.dialogueClosedCh = nil, nil

	dh.log.Debugf("dialogue manager finished")
}
