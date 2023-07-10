package multiplexer

import (
	"strconv"
	"sync"

	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
)

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
	return nil
}

func (dh *dialogueHub) DialogueOffline(dg DialogueDescriber) error {
	return nil
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
