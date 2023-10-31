package multiplexer

import (
	"io"
	"sync"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/geminio/pkg/iodefine"
	gsync "github.com/singchia/geminio/pkg/sync"
	"github.com/singchia/yafsm"
)

const (
	INIT         = "init"
	SESSION_SENT = "session_sent"
	SESSION_RECV = "session_recv"
	SESSIONED    = "sessioned"
	DISMISS_SENT = "dismiss_sent"
	DISMISS_RECV = "dismiss_recv"
	DISMISS_HALF = "dismiss_half"
	DISMISSED    = "dismissed"
	FINI         = "fini"

	ET_SESSIONSENT = "sessionsent"
	ET_SESSIONRECV = "sessionrecv"
	ET_SESSIONACK  = "sessionrecv"
	ET_ERROR       = "error"
	ET_DISMISSSENT = "dismisssent"
	ET_DISMISSRECV = "dismissrecv"
	ET_DISMISSACK  = "dismissack"
	ET_FINI        = "fini"
)

type dialogue struct {
	// options for timer, packet factory, log, delegate and meta
	*opts
	// delegate
	dlgt Delegate
	// meta
	meta []byte

	// under layer
	cn conn.Conn

	onlined   bool
	closewait synchub.Sync
	// dialogue id
	negotiatingID       uint64
	dialogueIDPeersCall bool
	dialogueID          uint64
	// synchub
	shub *synchub.SyncHub

	fsm *yafsm.FSM

	// mtx protects follows
	mtx        sync.RWMutex
	dialogueOK bool

	// io
	readInCh, writeOutCh     chan packet.Packet
	readOutCh, writeInCh     chan packet.Packet
	readInSize, writeOutSize int
	readOutSize, writeInSize int
	failedCh                 chan packet.Packet

	closeOnce *gsync.Once
}

type DialogueOption func(*dialogue)

// For the default dialogue which is ready for rolling
func OptionDialogueState(state string) DialogueOption {
	return func(dg *dialogue) {
		dg.fsm.SetState(state)
	}
}

func OptionDialogueDelegate(dlgt Delegate) DialogueOption {
	return func(dg *dialogue) {
		dg.dlgt = dlgt
	}
}

// OptionDialogueMeta set the meta info for the dialogue
func OptionDialogueMeta(meta []byte) DialogueOption {
	return func(dg *dialogue) {
		dg.meta = meta
	}
}

func OptionDialogueNegotiatingID(negotiatingID uint64, dialogueIDPeersCall bool) DialogueOption {
	return func(dg *dialogue) {
		dg.negotiatingID = negotiatingID
		dg.dialogueIDPeersCall = dialogueIDPeersCall
	}
}

func NewDialogue(cn conn.Conn, baseOpts *opts, opts ...DialogueOption) (*dialogue, error) {
	dg := &dialogue{
		opts:         baseOpts,
		meta:         cn.Meta(),
		dialogueID:   packet.SessionIDNull,
		cn:           cn,
		fsm:          yafsm.NewFSM(yafsm.WithAsync()),
		closeOnce:    new(gsync.Once),
		dialogueOK:   true,
		readInSize:   128,
		writeOutSize: 128,
		readOutSize:  128,
		writeInSize:  128,
	}
	// states
	dg.initFSM()
	// options
	for _, opt := range opts {
		opt(dg)
	}
	// io size
	dg.readInCh = make(chan packet.Packet, dg.readInSize)
	dg.writeOutCh = make(chan packet.Packet, dg.writeOutSize)
	dg.readOutCh = make(chan packet.Packet, dg.readOutSize)
	dg.writeInCh = make(chan packet.Packet, dg.writeInSize)

	dg.shub = synchub.NewSyncHub(synchub.OptionTimer(dg.tmr))
	// packet factory
	if dg.pf == nil {
		dg.pf = packet.NewPacketFactory(id.NewIDCounter(id.Even))
	}
	// log
	if dg.log == nil {
		dg.log = log.DefaultLog
	}

	// rolling up
	go dg.handlePkt()
	go dg.writePkt()
	return dg, nil
}

func (dg *dialogue) Meta() []byte {
	return dg.meta
}

func (dg *dialogue) ClientID() uint64 {
	return dg.cn.ClientID()
}

func (dg *dialogue) NegotiatingID() uint64 {
	return dg.negotiatingID
}

func (dg *dialogue) DialogueID() uint64 {
	return dg.dialogueID
}

// TODO
func (dg *dialogue) Side() geminio.Side {
	return geminio.RecipientSide
}

func (dg *dialogue) Write(pkt packet.Packet) error {
	dg.mtx.RLock()
	defer dg.mtx.RUnlock()

	if !dg.dialogueOK {
		return io.EOF
	}
	pkt.(packet.SessionAbove).SetSessionID(dg.dialogueID)
	dg.writeInCh <- pkt
	return nil
}

func (dg *dialogue) Read() (packet.Packet, error) {
	pkt, ok := <-dg.readOutCh
	if !ok {
		return nil, io.EOF
	}
	return pkt, nil
}

func (dg *dialogue) ReadC() <-chan packet.Packet {
	return dg.readOutCh
}

func (dg *dialogue) initFSM() {
	init := dg.fsm.AddState(INIT)
	sessionsent := dg.fsm.AddState(SESSION_SENT)
	sessionrecv := dg.fsm.AddState(SESSION_RECV)
	sessioned := dg.fsm.AddState(SESSIONED)
	dismisssent := dg.fsm.AddState(DISMISS_SENT)
	dismissrecv := dg.fsm.AddState(DISMISS_RECV)
	dismisshalf := dg.fsm.AddState(DISMISS_HALF)
	dismissed := dg.fsm.AddState(DISMISSED)
	fini := dg.fsm.AddState(FINI)
	dg.fsm.SetState(INIT)

	// sender
	dg.fsm.AddEvent(ET_SESSIONSENT, init, sessionsent)
	dg.fsm.AddEvent(ET_SESSIONACK, sessionsent, sessioned)

	// receiver
	dg.fsm.AddEvent(ET_SESSIONRECV, init, sessionrecv)
	dg.fsm.AddEvent(ET_SESSIONACK, sessionrecv, sessioned)
	dg.fsm.AddEvent(ET_ERROR, sessionrecv, sessionrecv, dg.closeWrapper)

	// both
	dg.fsm.AddEvent(ET_DISMISSSENT, sessionrecv, dismisssent)
	dg.fsm.AddEvent(ET_DISMISSSENT, sessioned, dismisssent)
	dg.fsm.AddEvent(ET_DISMISSSENT, dismissrecv, dismisssent)
	dg.fsm.AddEvent(ET_DISMISSSENT, dismisshalf, dismisshalf)

	dg.fsm.AddEvent(ET_DISMISSRECV, sessionsent, dismissrecv)
	dg.fsm.AddEvent(ET_DISMISSRECV, sessioned, dismissrecv)
	dg.fsm.AddEvent(ET_DISMISSRECV, dismisssent, dismissrecv)
	dg.fsm.AddEvent(ET_DISMISSRECV, dismisshalf, dismisshalf)

	// the 4-way handshake
	dg.fsm.AddEvent(ET_DISMISSACK, dismisssent, dismisshalf)
	dg.fsm.AddEvent(ET_DISMISSACK, dismissrecv, dismisshalf)
	dg.fsm.AddEvent(ET_DISMISSACK, dismisshalf, dismissed)

	// fini
	dg.fsm.AddEvent(ET_FINI, init, fini)
	dg.fsm.AddEvent(ET_FINI, sessionsent, fini)
	dg.fsm.AddEvent(ET_FINI, sessionrecv, fini)
	dg.fsm.AddEvent(ET_FINI, sessioned, fini)
	dg.fsm.AddEvent(ET_FINI, dismisssent, fini)
	dg.fsm.AddEvent(ET_FINI, dismissrecv, fini)
	dg.fsm.AddEvent(ET_FINI, dismisshalf, fini)
	dg.fsm.AddEvent(ET_FINI, dismissed, fini)
}

func (dg *dialogue) open() error {
	dg.log.Debugf("dialogue is opening, clientID: %d, dialogueID: %d",
		dg.cn.ClientID(), dg.dialogueID)

	var pkt *packet.SessionPacket
	pkt = dg.pf.NewSessionPacket(dg.negotiatingID, dg.dialogueIDPeersCall, dg.meta)

	dg.mtx.RLock()
	if !dg.dialogueOK {
		dg.mtx.RUnlock()
		return io.EOF
	}
	dg.writeInCh <- pkt
	dg.mtx.RUnlock()

	sync := dg.shub.Add(pkt.PacketID, synchub.WithTimeout(30*time.Second))
	event := <-sync.C()
	if event.Error != nil {
		dg.log.Debugf("dialogue open err: %s, clientID: %d, dialogueID: %d",
			event.Error, dg.cn.ClientID(), dg.dialogueID)
		close(dg.readInCh)
	}
	return event.Error
}

// we may or not separate the goroutine because the underlay is still a channel
func (dg *dialogue) writePkt() {
	writeOutCh := dg.writeOutCh
	err := error(nil)

	for {
		select {
		case pkt, ok := <-writeOutCh:
			if !ok {
				dg.log.Debugf("dialogue write done, clientID: %d, dialogueID: %d",
					dg.cn.ClientID(), dg.dialogueID)
				return
			}
			dg.log.Tracef("dialogue write down, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
				dg.cn.ClientID(), dg.dialogueID, pkt.ID(), pkt.Type().String())
			err = dg.dowritePkt(pkt, true)
			if err != nil {
				dg.log.Errorf("dialogue write down err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
					err, dg.cn.ClientID(), dg.dialogueID, pkt.ID(), pkt.Type().String())
				return
			}
		}
	}
}

func (dg *dialogue) dowritePkt(pkt packet.Packet, record bool) error {
	err := dg.cn.Write(pkt)
	if err != nil {
		dg.log.Errorf("dialogue write down err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			err, dg.cn.ClientID(), dg.dialogueID, pkt.ID(), pkt.Type().String())
		if record && dg.failedCh != nil {
			// only upper layer packet need to be notified
			dg.failedCh <- pkt
		}
	}
	return err
}

func (dg *dialogue) handlePkt() {
	readInCh := dg.readInCh
	writeInCh := dg.writeInCh

	for {
		select {
		case pkt, ok := <-readInCh:
			if !ok {
				goto FINI
			}
			dg.log.Tracef("dialogue read in packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
				dg.cn.ClientID(), dg.dialogueID, pkt.ID(), pkt.Type().String())
			ret := dg.handleIn(pkt)
			switch ret {
			case iodefine.IONewActive, iodefine.IOSuccess:
				continue
			case iodefine.IOClosed:
				goto FINI
			case iodefine.IOErr:
				goto FINI
			}
		case pkt, ok := <-writeInCh:
			if !ok {
				// BUG! shoud never be here.
				goto FINI
			}
			dg.log.Tracef("dialogue write in packet, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
				dg.cn.ClientID(), dg.dialogueID, pkt.ID(), pkt.Type().String())
			ret := dg.handleOut(pkt)
			switch ret {
			case iodefine.IONewPassive, iodefine.IOSuccess:
				continue
			case iodefine.IOClosed:
				goto FINI
			case iodefine.IOErr:
				goto FINI
			}
		}
	}
FINI:
	dg.log.Debugf("dialogue handle pkt done, clientID: %d, dialogueID: %d",
		dg.cn.ClientID(), dg.dialogueID)

	// only onlined Dialogue need to be notified
	if dg.dlgt != nil && dg.onlined {
		dg.dlgt.DialogueOffline(dg)
	}
	// only handlePkt leads to this fini, and reclaims all channels and other resources
	dg.fini()
}

func (dg *dialogue) handleIn(pkt packet.Packet) iodefine.IORet {
	switch realPkt := pkt.(type) {
	case *packet.SessionPacket:
		return dg.handleInSessionPacket(realPkt)
	case *packet.SessionAckPacket:
		return dg.handleInSessionAckPacket(realPkt)
	case *packet.DismissPacket:
		return dg.handleInDismissPacket(realPkt)
	case *packet.DismissAckPacket:
		return dg.handleInDimssAckPacket(realPkt)
	default:
		return dg.handleInDataPacket(pkt)
	}
}

func (dg *dialogue) handleOut(pkt packet.Packet) iodefine.IORet {
	switch realPkt := pkt.(type) {
	case *packet.SessionPacket:
		return dg.handleOutSessionPacket(realPkt)
	case *packet.SessionAckPacket:
		return dg.handleOutSessionAckPacket(realPkt)
	case *packet.DismissPacket:
		return dg.handleOutDismissPacket(realPkt)
	case *packet.DismissAckPacket:
		return dg.handleOutDismissAckPacket(realPkt)
	default:
		return dg.handleOutDataPacket(pkt)
	}
}

// input packet
func (dg *dialogue) handleInSessionPacket(pkt *packet.SessionPacket) iodefine.IORet {
	dg.log.Debugf("read dialogue packet, clientID: %d, negotiateID: %d, dialogueID: %d, packetID: %d",
		dg.cn.ClientID(), pkt.NegotiateID(), dg.negotiatingID, pkt.ID())
	err := dg.fsm.EmitEvent(ET_SESSIONRECV)
	if err != nil {
		dg.log.Debugf("emit ET_SESSIONRECV err: %s, clientID: %d, dialogueID: %d, packetID: %d",
			err, dg.cn.ClientID(), dg.negotiatingID, pkt.ID())
		return iodefine.IOErr
	}
	// negotiate sessionID
	// if under conn is client, asking for a sessionID
	// if under conn is server, return prepared negotiatingID
	dialogueID := pkt.NegotiateID()
	if pkt.SessionIDAcquire() {
		dialogueID = dg.negotiatingID
	}
	dg.dialogueID = dialogueID
	dg.meta = pkt.SessionData.Meta

	retPkt := dg.pf.NewSessionAckPacket(pkt.PacketID, pkt.NegotiateID(), dialogueID, nil)
	dg.writeInCh <- retPkt
	return iodefine.IOSuccess
}

func (dg *dialogue) handleInSessionAckPacket(pkt *packet.SessionAckPacket) iodefine.IORet {
	dg.log.Debugf("read dialogue ack packet, clientID: %d, dialogueID: %d, packetID: %d",
		dg.cn.ClientID(), pkt.SessionID(), pkt.ID())
	err := dg.fsm.EmitEvent(ET_SESSIONACK)
	if err != nil {
		dg.log.Debugf("emit ET_SESSIONACK err: %s, clientID: %d, dialogueID: %d, packetID: %d",
			err, dg.cn.ClientID(), dg.dialogueID, pkt.ID())
		dg.shub.Error(pkt.ID(), err)
		return iodefine.IOErr
	}
	dg.dialogueID = pkt.SessionID()
	dg.meta = pkt.SessionData.Meta

	// the packetID is assigned by SessionPacket, originally from function open,
	// and open is waiting for the completion.
	dg.shub.Done(pkt.ID())
	dg.onlined = true
	return iodefine.IONewActive
}

func (dg *dialogue) handleInDismissPacket(pkt *packet.DismissPacket) iodefine.IORet {
	dg.log.Debugf("read dismiss packet, clientID: %d, dialogueID: %d, packetID: %d",
		dg.cn.ClientID(), dg.dialogueID, pkt.ID())
	err := dg.fsm.EmitEvent(ET_DISMISSRECV)
	if err != nil {
		dg.log.Debugf("emit ET_DISMISSRECV err: %s, clientID: %d, dialogueID: %d, packetID: %d",
			err, dg.cn.ClientID(), dg.dialogueID, pkt.ID())
		return iodefine.IOErr
	}
	retPkt := dg.pf.NewDismissAckPacket(pkt.ID(),
		pkt.SessionID(), nil)
	dg.writeInCh <- retPkt
	// send out side dismiss while receiving dismiss packet
	dg.Close()
	return iodefine.IOSuccess
}

func (dg *dialogue) handleInDimssAckPacket(pkt *packet.DismissAckPacket) iodefine.IORet {
	dg.log.Debugf("read dismiss ack packet, clientID: %d, dialogueID: %d, packetID: %d",
		dg.cn.ClientID(), dg.dialogueID, pkt.ID())
	err := dg.fsm.EmitEvent(ET_DISMISSACK)
	if err != nil {
		dg.log.Debugf("emit ET_DISMISSACK err: %s, clientID: %d, dialogueID: %d, packetID: %d",
			err, dg.cn.ClientID(), dg.dialogueID, pkt.ID())
		return iodefine.IOErr
	}
	if dg.fsm.State() == DISMISS_HALF {
		return iodefine.IOSuccess
	}
	if dg.closewait != nil {
		// and CloseWait is waiting for the completion.
		dg.closewait.Done()
	}
	return iodefine.IOClosed
}

func (dg *dialogue) handleInDataPacket(pkt packet.Packet) iodefine.IORet {
	ok := dg.fsm.InStates(SESSIONED)
	if !ok {
		dg.log.Debugf("data at non SESSIONED, clientID: %d, dialogueID: %d, packetID: %d",
			dg.cn.ClientID(), dg.dialogueID, pkt.ID())
		if dg.failedCh != nil {
			dg.failedCh <- pkt
		}
		return iodefine.IODiscard
	}
	dg.readOutCh <- pkt
	return iodefine.IOSuccess
}

// output packet
func (dg *dialogue) handleOutSessionPacket(pkt *packet.SessionPacket) iodefine.IORet {
	err := dg.fsm.EmitEvent(ET_SESSIONSENT)
	if err != nil {
		dg.log.Errorf("emit ET_SESSIONSENT err: %s, clientID: %d, dialogueID: %d, packetID: %d",
			err, dg.cn.ClientID(), pkt.NegotiateID(), pkt.ID())
		return iodefine.IOErr
	}
	dg.writeOutCh <- pkt
	dg.log.Debugf("send dialogue down succeed, clientID: %d, dialogueID: %d, packetID: %d",
		dg.cn.ClientID(), pkt.NegotiateID(), pkt.ID())
	return iodefine.IOSuccess
}

func (dg *dialogue) handleOutSessionAckPacket(pkt *packet.SessionAckPacket) iodefine.IORet {
	err := error(nil)
	if dg.dlgt != nil {
		// open dialogue passive
		// notify delegation the online event
		err = dg.dlgt.DialogueOnline(dg)
		if err != nil {
			pkt.SetError(err)
			err = dg.fsm.EmitEvent(ET_ERROR)
			if err != nil {
				dg.log.Errorf("emit ET_ERROR err: %s, clientID: %d, dialogueID: %d, packetID: %d",
					err, dg.cn.ClientID(), pkt.NegotiateID(), pkt.ID())
				return iodefine.IOErr
			}
			dg.writeOutCh <- pkt
			// to tell peer the dialogue handshake is error, and peer should dismiss the dialogue.
			// this situation shouldn't be seen as connected, so don't set onlined.
			return iodefine.IOSuccess
		}
	}
	err = dg.fsm.EmitEvent(ET_SESSIONACK)
	if err != nil {
		dg.log.Debugf("emit ET_SESSIONACK err: %s, clientID: %d, dialogueID: %d, packetID: %d",
			err, dg.cn.ClientID(), dg.dialogueID, pkt.ID())
		return iodefine.IOErr
	}
	dg.writeOutCh <- pkt
	dg.log.Debugf("dialogue write session ack down succeed, clientID: %d, dialogueID: %d, packetID: %d",
		dg.cn.ClientID(), dg.dialogueID, pkt.ID())

	dg.onlined = true
	return iodefine.IONewPassive
}

func (dg *dialogue) handleOutDismissPacket(pkt *packet.DismissPacket) iodefine.IORet {
	err := dg.fsm.EmitEvent(ET_DISMISSSENT)
	if err != nil {
		dg.log.Errorf("emit ET_SESSIONSENT err: %s, clientID: %d, dialogueID: %d, packetID: %d",
			err, dg.cn.ClientID(), dg.dialogueID, pkt.ID())
		return iodefine.IOErr
	}
	dg.writeOutCh <- pkt
	dg.log.Debugf("dialogue write dismiss down succeed, clientID: %d, dialogueID: %d, packetID: %d",
		dg.cn.ClientID(), dg.dialogueID, pkt.ID())
	return iodefine.IOSuccess
}

func (dg *dialogue) handleOutDismissAckPacket(pkt *packet.DismissAckPacket) iodefine.IORet {
	err := dg.fsm.EmitEvent(ET_DISMISSACK)
	if err != nil {
		dg.log.Errorf("emit ET_DISMISSACK err: %s, clientID: %d, dialogueID: %d, packetID: %d, state: %s",
			err, dg.cn.ClientID(), dg.dialogueID, pkt.ID(), dg.fsm.State())
		return iodefine.IOErr
	}
	dg.dowritePkt(pkt, false)
	// make sure this packet is flushed before writeOutCh closed
	dg.log.Debugf("dialogue write dismiss ack down succeed, clientID: %d, dialogueID: %d, packetID: %d",
		dg.cn.ClientID(), dg.dialogueID, pkt.ID())
	if dg.fsm.State() == DISMISS_HALF {
		return iodefine.IOSuccess
	}
	return iodefine.IOClosed
}

func (dg *dialogue) handleOutDataPacket(pkt packet.Packet) iodefine.IORet {
	dg.writeOutCh <- pkt
	dg.log.Tracef("dialogue write data down succeed, clientID: %d, dialogueID: %d, packetID: %d",
		dg.cn.ClientID(), dg.dialogueID, pkt.ID())
	return iodefine.IOSuccess
}

func (dg *dialogue) Close() {
	dg.closeOnce.Do(func() {
		dg.mtx.RLock()
		defer dg.mtx.RUnlock()
		if !dg.dialogueOK {
			return
		}

		dg.log.Debugf("dialogue async close, clientID: %d, dialogueID: %d",
			dg.cn.ClientID(), dg.dialogueID)

		pkt := dg.pf.NewDismissPacket(dg.dialogueID)
		dg.writeInCh <- pkt
		// we need a tick in case of never receiving the dismiss ack packet
		dg.closewait = dg.shub.New(pkt.PacketID, synchub.WithTimeout(30*time.Second),
			synchub.WithCallback(func(event *synchub.Event) {
				if event.Error != nil {
					dg.log.Debugf("dialogue close err: %s, clientID: %d, dialogueID: %d",
						event.Error, dg.cn.ClientID(), dg.dialogueID)
					if event.Error == synchub.ErrSyncTimeout {
						// timeout and exit the dialogue
						close(dg.readInCh)
					}
				}
			}))
	})
}

func (dg *dialogue) CloseWait() {
	// send close packet and wait for the end
	dg.closeOnce.Do(func() {
		dg.mtx.RLock()
		if !dg.dialogueOK {
			dg.mtx.RUnlock()
			return
		}

		dg.log.Debugf("dialogue is closing, clientID: %d, dialogueID: %d",
			dg.cn.ClientID(), dg.dialogueID)

		pkt := dg.pf.NewDismissPacket(dg.dialogueID)
		dg.writeInCh <- pkt
		dg.mtx.RUnlock()
		// the synchub shouldn't be locked
		dg.closewait = dg.shub.New(pkt.PacketID, synchub.WithTimeout(30*time.Second))
		event := <-dg.closewait.C()
		if event.Error != nil {
			dg.log.Debugf("dialogue close wait err: %s, clientID: %d, dialogueID: %d",
				event.Error, dg.cn.ClientID(), dg.dialogueID)
			if event.Error == synchub.ErrSyncTimeout {
				// timeout and exit the dialogue
				close(dg.readInCh)
			}
			return
		}
		dg.log.Debugf("dialogue closed, clientID: %d, dialogueID: %d",
			dg.cn.ClientID(), dg.dialogueID)
		return
	})
}

func (dg *dialogue) closeWrapper(_ *yafsm.Event) {
	dg.Close()
}

// finish and reclaim resources
func (dg *dialogue) fini() {
	dg.log.Debugf("dialogue finishing, clientID: %d, dialogueID: %d",
		dg.cn.ClientID(), dg.dialogueID)

	dg.mtx.Lock()
	// collect shub
	dg.shub.Close()
	dg.shub = nil

	// TODO should we move dialogueOK=false to Close and CloseWait?
	dg.dialogueOK = false
	close(dg.writeInCh)
	dg.mtx.Unlock()

	for pkt := range dg.writeInCh {
		if dg.failedCh != nil && !packet.SessionLayer(pkt) {
			dg.failedCh <- pkt
		}
	}
	// the outside should care about channel status
	close(dg.readOutCh)
	// writeOutCh must be cared since writhPkt might quit first
	close(dg.writeOutCh)
	for pkt := range dg.writeOutCh {
		if dg.failedCh != nil && !packet.SessionLayer(pkt) {
			dg.failedCh <- pkt
		}
	}
	// collect channels
	dg.writeInCh, dg.writeOutCh = nil, nil
	// TODO we left the readInCh buffer at some edge cases which may cause peer msg timeout

	// collect fsm
	dg.fsm.EmitEvent(ET_FINI)
	dg.fsm.Close()
	dg.fsm = nil

	dg.log.Debugf("dialogue finished, clientID: %d, dialogueID: %d",
		dg.cn.ClientID(), dg.dialogueID)

}
