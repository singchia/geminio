package multiplexer

import (
	"io"
	"sync"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/geminio/pkg/iodefine"
	"github.com/singchia/go-timer/v2"
	"github.com/singchia/yafsm"
)

const (
	INIT         = "init"
	SESSION_SENT = "session_sent"
	SESSION_RECV = "session_recv"
	SESSIONED    = "sessioned"
	DISMISS_SENT = "dismiss_sent"
	DISMISS_RECV = "dismiss_recv"
	DISMISSED    = "dismissed"
	FINI         = "fini"

	ET_SESSIONSENT = "sessionsent"
	ET_SESSIONRECV = "sessionrecv"
	ET_SESSIONACK  = "sessionrecv"
	ET_ERROR       = "error"
	ET_EOF         = "eof"
	ET_DISMISSSENT = "dismisssent"
	ET_DISMISSRECV = "dismissrecv"
	ET_DISMISSACK  = "dismissack"
	ET_FINI        = "fini"
)

type sessionOpts struct {
	// packet factory
	pf *packet.PacketFactory
	// logger
	log log.Logger
	// delegate
	dlgt Delegate
	// timer
	tmr        timer.Timer
	tmrOutside bool
	// meta
	meta []byte
}

type session struct {
	// under layer
	cn conn.Conn
	// options
	sessionOpts
	// session id
	negotiatingID      uint64
	sessionIDPeersCall bool
	sessionID          uint64
	// synchub
	shub *synchub.SyncHub

	//sm       *sessionMgr
	fsm      *yafsm.FSM
	onceFini *sync.Once

	// mtx protect follows
	mtx       sync.RWMutex
	sessionOK bool

	// to conn layer
	readInCh                  chan packet.Packet
	writeFromUpCh, readToUpCh chan packet.Packet

	// session control
	writeCh chan packet.Packet
}

type DialogueOption func(*session)

// For the default session which is ready for rolling
func OptionDialogueState(state string) DialogueOption {
	return func(sn *session) {
		sn.fsm.SetState(state)
	}
}

// Set the packet factory for packet generating
func OptionDialoguePacketFactory(pf *packet.PacketFactory) DialogueOption {
	return func(sn *session) {
		sn.pf = pf
	}
}

func OptionDialogueLogger(log log.Logger) DialogueOption {
	return func(sn *session) {
		sn.log = log
	}
}

// Set delegate to know online and offline events
func OptionDialogueDelegate(dlgt Delegate) DialogueOption {
	return func(sn *session) {
		sn.dlgt = dlgt
	}
}

func OptionDialogueTimer(tmr timer.Timer) DialogueOption {
	return func(sn *session) {
		sn.tmr = tmr
		sn.tmrOutside = true
	}
}

// OptionDialogueMeta set the meta info for the session
func OptionDialogueMeta(meta []byte) DialogueOption {
	return func(sn *session) {
		sn.meta = meta
	}
}

func OptionDialogueNegotiatingID(negotiatingID uint64, sessionIDPeersCall bool) DialogueOption {
	return func(sn *session) {
		sn.negotiatingID = negotiatingID
		sn.sessionIDPeersCall = sessionIDPeersCall
	}
}

func NewDialogue(cn conn.Conn, opts ...DialogueOption) (*session, error) {
	sn := &session{
		sessionOpts: sessionOpts{
			meta: cn.Meta(),
		},
		sessionID: packet.SessionIDNull,
		cn:        cn,
		fsm:       yafsm.NewFSM(),
		onceFini:  new(sync.Once),
		sessionOK: true,
		writeCh:   make(chan packet.Packet, 128),
	}
	// options
	for _, opt := range opts {
		opt(sn)
	}
	// io size
	sn.readInCh = make(chan packet.Packet, 128)
	sn.writeFromUpCh = make(chan packet.Packet, 128)
	sn.readToUpCh = make(chan packet.Packet, 128)
	// timer
	if !sn.tmrOutside {
		sn.tmr = timer.NewTimer()
	}
	sn.shub = synchub.NewSyncHub(synchub.OptionTimer(sn.tmr))
	// packet factory
	if sn.pf == nil {
		sn.pf = packet.NewPacketFactory(id.NewIDCounter(id.Even))
	}
	// log
	if sn.log == nil {
		sn.log = log.DefaultLog
	}
	// states
	sn.initFSM()
	// rolling up
	go sn.readPkt()
	go sn.writePkt()
	return sn, nil
}

func (sn *session) Meta() []byte {
	return sn.meta
}

func (sn *session) DialogueID() uint64 {
	return sn.sessionID
}

func (sn *session) Side() Side {
	// TODO
	return ServerSide
}

func (sn *session) Write(pkt packet.Packet) error {
	sn.mtx.RLock()
	defer sn.mtx.RUnlock()

	if !sn.sessionOK {
		return io.EOF
	}
	sn.writeFromUpCh <- pkt
	return nil
}

func (sn *session) Read() (packet.Packet, error) {
	pkt, ok := <-sn.readToUpCh
	if !ok {
		return nil, io.EOF
	}
	return pkt, nil
}

func (sn *session) initFSM() {
	init := sn.fsm.AddState(INIT)
	sessionsent := sn.fsm.AddState(SESSION_SENT)
	sessionrecv := sn.fsm.AddState(SESSION_RECV)
	sessioned := sn.fsm.AddState(SESSIONED)
	dismisssent := sn.fsm.AddState(DISMISS_SENT)
	dismissrecv := sn.fsm.AddState(DISMISS_RECV)
	dismissed := sn.fsm.AddState(DISMISSED)
	fini := sn.fsm.AddState(FINI)
	sn.fsm.SetState(INIT)

	// sender
	sn.fsm.AddEvent(ET_SESSIONSENT, init, sessionsent)
	sn.fsm.AddEvent(ET_SESSIONACK, sessionsent, sessioned)
	sn.fsm.AddEvent(ET_ERROR, sessionsent, dismissed, sn.closeWrapper)
	sn.fsm.AddEvent(ET_EOF, sessionsent, dismissed)

	// receiver
	sn.fsm.AddEvent(ET_SESSIONRECV, init, sessionrecv)
	sn.fsm.AddEvent(ET_SESSIONACK, sessionrecv, sessioned)
	sn.fsm.AddEvent(ET_ERROR, sessionrecv, dismissed, sn.closeWrapper)
	sn.fsm.AddEvent(ET_EOF, sessionrecv, dismissed)

	// both
	sn.fsm.AddEvent(ET_ERROR, sessioned, dismissed, sn.closeWrapper)
	sn.fsm.AddEvent(ET_EOF, sessioned, dismissed)
	sn.fsm.AddEvent(ET_DISMISSSENT, sessioned, dismisssent)
	sn.fsm.AddEvent(ET_DISMISSRECV, sessioned, dismissrecv)
	sn.fsm.AddEvent(ET_DISMISSACK, dismisssent, dismissed)
	sn.fsm.AddEvent(ET_DISMISSACK, dismissrecv, dismissed)

	// fini
	sn.fsm.AddEvent(ET_FINI, init, fini)
	sn.fsm.AddEvent(ET_FINI, sessionsent, fini)
	sn.fsm.AddEvent(ET_FINI, sessioned, fini)
	sn.fsm.AddEvent(ET_FINI, dismisssent, fini)
	sn.fsm.AddEvent(ET_FINI, dismissrecv, fini)
	sn.fsm.AddEvent(ET_FINI, dismissed, fini)
}

func (sn *session) Open() error {
	sn.log.Debugf("session is opening, clientId: %d, sessionId: %d",
		sn.cn.ClientID(), sn.sessionID)

	var pkt *packet.SessionPacket
	pkt = sn.pf.NewSessionPacket(sn.negotiatingID, sn.sessionIDPeersCall, sn.meta)

	sn.mtx.RLock()
	if !sn.sessionOK {
		sn.mtx.RUnlock()
		return io.EOF
	}
	sn.writeCh <- pkt
	sn.mtx.RUnlock()

	sync := sn.shub.New(pkt.PacketID, synchub.WithTimeout(30*time.Second))
	event := <-sync.C()
	return event.Error
}

func (sn *session) Close() {
	sn.mtx.RLock()
	if !sn.sessionOK {
		sn.mtx.RUnlock()
		return
	}

	sn.log.Debugf("session is closing, clientId: %d, sessionId: %d",
		sn.cn.ClientID(), sn.sessionID)

	if sn.fsm.InStates(SESSIONED) {
		pkt := sn.pf.NewDismissPacket(sn.sessionID)
		sn.writeCh <- pkt
		sn.mtx.RUnlock()

		sync := sn.shub.New(pkt.PacketID, synchub.WithTimeout(30*time.Second))
		event := <-sync.C()
		if event.Error != nil {
			sn.log.Debugf("session close err: %s, clientId: %d, sessionId: %d",
				event.Error, sn.cn.ClientID(), sn.sessionID)
			return
		}
		sn.log.Debugf("session closed, clientId: %d, sessionId: %d",
			sn.cn.ClientID(), sn.sessionID)
		return
	}
	sn.mtx.RUnlock()
	return
}

func (sn *session) CloseWait() {
	// send close packet and wait for the end
}

func (sn *session) writePkt() {

	for {
		select {
		case pkt, ok := <-sn.writeCh:
			if !ok {
				sn.log.Debugf("write packet EOF, clientId: %d, sessionId: %d",
					sn.cn.ClientID(), sn.sessionID)
				return
			}
			ie := sn.handlePkt(pkt, iodefine.OUT)
			switch ie {
			case iodefine.IOSuccess:
				continue
			case iodefine.IOData:
				err := sn.cn.Write(pkt)
				if err != nil {
					sn.log.Debugf("write down err: %s, clientId: %d, sessionId: %d",
						err, sn.cn.ClientID(), sn.sessionID)
					goto CLOSED
				}
			case iodefine.IOClosed:
				goto CLOSED

			case iodefine.IOErr:
				sn.fsm.EmitEvent(ET_ERROR)
				sn.log.Errorf("handle packet return err, clientId: %d, sessionId: %d", sn.cn.ClientID(), sn.sessionID)
				goto CLOSED
			}
		case pkt, ok := <-sn.writeFromUpCh:
			if !ok {
				sn.log.Infof("write from up EOF, clientId: %d, sessionId: %d", sn.cn.ClientID(), sn.sessionID)
				continue
			}
			sn.log.Tracef("to write down, clientId: %d, sessionId: %d, packetId: %d, packetType: %s",
				sn.cn.ClientID(), sn.sessionID, pkt.ID(), pkt.Type().String())
			err := sn.cn.Write(pkt)
			if err != nil {
				if err == io.EOF {
					sn.fsm.EmitEvent(ET_EOF)
					sn.log.Infof("write down EOF, clientId: %d, sessionId: %d, packetId: %d, packetType: %s",
						sn.cn.ClientID(), sn.sessionID, pkt.ID(), pkt.Type().String())
				} else {
					sn.fsm.EmitEvent(ET_ERROR)
					sn.log.Infof("write down err: %s, clientId: %d, sessionId: %d, packetId: %d, packetType: %s",
						err, sn.cn.ClientID(), sn.sessionID, pkt.ID(), pkt.Type().String())

				}
				goto CLOSED
			}
			continue
		}
	}
CLOSED:
	sn.fini()
}

func (sn *session) readPkt() {
	for {
		pkt, ok := <-sn.readInCh
		if !ok {
			sn.log.Debugf("read down EOF, clientId: %d, sessionId: %d",
				sn.cn.ClientID(), sn.sessionID)
			return
		}
		sn.log.Tracef("read %s, clientId: %d, sessionId: %d, packetId: %d",
			pkt.Type().String(), sn.cn.ClientID(), sn.sessionID, pkt.ID())
		ie := sn.handlePktWrapper(pkt, iodefine.IN)
		switch ie {
		case iodefine.IOSuccess:
			continue
		case iodefine.IOClosed:
			goto CLOSED
		}
	}
CLOSED:
	sn.fini()
}

func (sn *session) handlePktWrapper(pkt packet.Packet, iotype iodefine.IOType) iodefine.IORet {
	ie := sn.handlePkt(pkt, iodefine.IN)
	switch ie {
	case iodefine.IONewActive:
		return iodefine.IOSuccess

	case iodefine.IONewPassive:
		return iodefine.IOSuccess

	case iodefine.IOClosed:
		return iodefine.IOClosed

	case iodefine.IOData:
		sn.mtx.RLock()
		// TODO
		if !sn.sessionOK {
			sn.mtx.RUnlock()
			return iodefine.IOSuccess
		}
		sn.readToUpCh <- pkt
		sn.mtx.RUnlock()
		return iodefine.IOSuccess

	case iodefine.IOErr:
		// TODO 在遇到IOErr之后，还有必要发送Close吗，需要区分情况
		sn.fsm.EmitEvent(ET_ERROR)
		return iodefine.IOClosed

	default:
		return iodefine.IOSuccess
	}
}

func (sn *session) handlePkt(pkt packet.Packet, iotype iodefine.IOType) iodefine.IORet {

	switch iotype {
	case iodefine.OUT:
		switch realPkt := pkt.(type) {
		case *packet.SessionPacket:
			err := sn.fsm.EmitEvent(ET_SESSIONSENT)
			if err != nil {
				sn.log.Errorf("emit ET_SESSIONSENT err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.cn.ClientID(), realPkt.NegotiateID, pkt.ID())
				return iodefine.IOErr
			}
			err = sn.cn.Write(realPkt)
			if err != nil {
				sn.log.Errorf("write SESSION err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.cn.ClientID(), realPkt.NegotiateID, pkt.ID())
				return iodefine.IOErr
			}
			sn.log.Debugf("write session down succeed, clientId: %d, sessionId: %d, packetId: %d",
				sn.cn.ClientID(), realPkt.NegotiateID, pkt.ID())
			return iodefine.IOSuccess

		case *packet.DismissPacket:

			if sn.fsm.InStates(DISMISS_RECV, DISMISSED) {
				// TODO 两边同时Close的场景
				sn.shub.Ack(realPkt.PacketID, nil)
				sn.log.Debugf("already been dismissed, clientId: %d, sessionId: %d, packetId: %d",
					sn.cn.ClientID(), sn.sessionID, pkt.ID())
				return iodefine.IOSuccess
			}

			err := sn.fsm.EmitEvent(ET_DISMISSSENT)
			if err != nil {
				sn.log.Errorf("emit ET_SESSIONSENT err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.cn.ClientID(), sn.sessionID, pkt.ID())
				return iodefine.IOErr
			}
			err = sn.cn.Write(realPkt)
			if err != nil {
				sn.log.Errorf("write DISMISS err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.cn.ClientID(), sn.sessionID, pkt.ID())
				return iodefine.IOErr
			}
			sn.log.Debugf("write dismiss down succeed, clientId: %d, sessionId: %d, packetId: %d",
				sn.cn.ClientID(), sn.sessionID, pkt.ID())
			return iodefine.IOSuccess

		case *packet.DismissAckPacket:
			err := sn.fsm.EmitEvent(ET_DISMISSACK)
			if err != nil {
				sn.log.Errorf("emit ET_DISMISSACK err: %s, clientId: %d, sessionId: %d, packetId: %d, state: %s",
					err, sn.cn.ClientID(), sn.sessionID, pkt.ID(), sn.fsm.State())
				return iodefine.IOErr
			}
			err = sn.cn.Write(pkt)
			if err != nil {
				sn.log.Errorf("write DISMISSACK err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.cn.ClientID(), sn.sessionID, pkt.ID())
				return iodefine.IOErr
			}
			sn.log.Debugf("write dismiss ack down succeed, clientId: %d, sessionId: %d, packetId: %d",
				sn.cn.ClientID(), sn.sessionID, pkt.ID())
			return iodefine.IOClosed

		default:
			return iodefine.IOData
		}

	case iodefine.IN:
		switch realPkt := pkt.(type) {
		case *packet.SessionPacket:
			sn.log.Debugf("read session packet, clientId: %d, sessionId: %d, packetId: %d",
				sn.cn.ClientID(), sn.sessionID, pkt.ID())
			err := sn.fsm.EmitEvent(ET_SESSIONRECV)
			if err != nil {
				sn.log.Debugf("emit ET_SESSIONRECV err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.cn.ClientID(), sn.sessionID, pkt.ID())
				return iodefine.IOErr
			}
			//  分配session id
			sessionId := realPkt.NegotiateID
			if realPkt.SessionIDAcquire() {
				sessionId = sn.negotiatingID
			}
			sn.sessionID = sessionId
			sn.meta = realPkt.SessionData.Meta

			retPkt := sn.pf.NewSessionAckPacket(realPkt.PacketID, sessionId, nil)
			err = sn.cn.Write(retPkt)
			if err != nil {
				sn.log.Errorf("write SESSIONACK err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.cn.ClientID(), sn.sessionID, pkt.ID())
				return iodefine.IOErr
			}

			// TODO 端到端一致性
			err = sn.fsm.EmitEvent(ET_SESSIONACK)
			if err != nil {
				sn.log.Debugf("emit ET_SESSIONACK err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.cn.ClientID(), sn.sessionID, pkt.ID())
				return iodefine.IOErr
			}
			sn.log.Debugf("write session ack down succeed, clientId: %d, sessionId: %d, packetId: %d",
				sn.cn.ClientID(), sn.sessionID, pkt.ID())
			if sn.dlgt != nil {
				sn.dlgt.DialogueOnline(sn)
			}
			// accept session
			// 被动打开，创建session
			return iodefine.IONewPassive

		case *packet.SessionAckPacket:
			sn.log.Debugf("read session ack packet, clientId: %d, sessionId: %d, packetId: %d",
				sn.cn.ClientID(), realPkt.SessionID, pkt.ID())
			err := sn.fsm.EmitEvent(ET_SESSIONACK)
			if err != nil {
				sn.log.Debugf("emit ET_SESSIONACK err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.cn.ClientID(), sn.sessionID, pkt.ID())
				return iodefine.IOErr
			}
			sn.sessionID = realPkt.SessionID
			sn.meta = realPkt.SessionData.Meta
			// 主动打开成功，创建session
			sn.shub.Ack(pkt.ID(), nil)
			if sn.dlgt != nil {
				sn.dlgt.DialogueOnline(sn)
			}

			return iodefine.IONewActive

		case *packet.DismissPacket:
			sn.log.Debugf("read dismiss packet, clientId: %d, sessionId: %d, packetId: %d",
				sn.cn.ClientID(), sn.sessionID, pkt.ID())

			if sn.fsm.InStates(DISMISS_SENT, DISMISSED) {
				// TODO 两端同时发起Close的场景
				retPkt := sn.pf.NewDismissAckPacket(realPkt.PacketID,
					realPkt.SessionID, nil)
				sn.mtx.RLock()
				if !sn.sessionOK {
					sn.mtx.RUnlock()
					return iodefine.IOErr
				}
				sn.writeCh <- retPkt
				sn.mtx.RUnlock()
				return iodefine.IOSuccess

			}
			err := sn.fsm.EmitEvent(ET_DISMISSRECV)
			if err != nil {
				sn.log.Debugf("emit ET_DISMISSRECV err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.cn.ClientID(), sn.sessionID, pkt.ID())
				return iodefine.IOErr
			}

			// return
			retPkt := sn.pf.NewDismissAckPacket(realPkt.PacketID,
				realPkt.SessionID, nil)
			sn.mtx.RLock()
			if !sn.sessionOK {
				sn.mtx.RUnlock()
				return iodefine.IOErr
			}
			sn.writeCh <- retPkt
			sn.mtx.RUnlock()

			return iodefine.IOSuccess

		case *packet.DismissAckPacket:
			sn.log.Debugf("read dismiss ack packet, clientId: %d, sessionId: %d, packetId: %d",
				sn.cn.ClientID(), sn.sessionID, pkt.ID())
			err := sn.fsm.EmitEvent(ET_DISMISSACK)
			if err != nil {
				sn.log.Debugf("emit ET_DISMISSACK err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.cn.ClientID(), sn.sessionID, pkt.ID())
				return iodefine.IOErr
			}
			sn.shub.Ack(realPkt.PacketID, nil)
			// 主动关闭成功，关闭session
			return iodefine.IOClosed

		default:
			return iodefine.IOData
		}
	}
	return iodefine.IOErr
}

func (sn *session) close() {}

func (sn *session) closeWrapper(_ *yafsm.Event) {
	sn.Close()
}

func (sn *session) fini() {
	sn.onceFini.Do(func() {
		sn.log.Debugf("session finished, clientId: %d, sessionId: %d", sn.cn.ClientID(), sn.sessionID)

		sn.mtx.Lock()
		sn.sessionOK = false
		close(sn.writeCh)

		close(sn.readInCh)
		close(sn.writeFromUpCh)
		close(sn.readToUpCh)

		sn.mtx.Unlock()

		sn.fsm.EmitEvent(ET_FINI)
		sn.fsm.Close()
	})
}
