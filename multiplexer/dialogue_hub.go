package multiplexer

import (
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

func (dh *dialogueHub) handlePkt(pkt packet.Packet) {
	switch realPkt := pkt.(type) {
	case *packet.SessionPacket:
		// new negotiating dialogue
		negotiatingID := dh.dialogueIDs.GetID()
		dg, err := NewDialogue()
	}
}
