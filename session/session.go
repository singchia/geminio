package session

import (
	"io"
	"sync"
	"time"

	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/iodefine"
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

// TODO 性能
type Session struct {
	SessionID uint64
	Meta      []byte
	sm        *SessionMgr
	fsm       *yafsm.FSM
	onceFini  *sync.Once

	sessionOK  bool
	sessionMtx sync.RWMutex

	// session data
	// to conn layer
	readDownCh                chan packet.Packet
	writeFromUpCh, readToUpCh chan packet.Packet

	// session control
	writeCh chan packet.Packet
}

type SessionOption func(*Session)

func OptionSessionState(state string) SessionOption {
	return func(sn *Session) {
		sn.fsm.SetState(state)
	}
}

func NewSession(sm *SessionMgr, opts ...SessionOption) *Session {
	sn := &Session{
		SessionID: packet.SessionIDNull,
		Meta:      sm.cn.Meta(),
		sm:        sm,
		fsm:       yafsm.NewFSM(),
		onceFini:  new(sync.Once),
		// session control
		sessionOK: true,
		writeCh:   make(chan packet.Packet, 218),
	}
	for _, opt := range opts {
		opt(sn)
	}
	sn.readDownCh = make(chan packet.Packet, 218)
	sn.writeFromUpCh = make(chan packet.Packet, 218)
	sn.readToUpCh = make(chan packet.Packet, 218)
	return sn
}

func (sn *Session) Write(pkt packet.Packet) error {
	// TODO 性能优化
	sn.sessionMtx.RLock()
	defer sn.sessionMtx.RUnlock()

	if !sn.sessionOK {
		return io.EOF
	}
	sn.writeFromUpCh <- pkt
	return nil
}

func (sn *Session) Read() (packet.Packet, error) {
	pkt, ok := <-sn.readToUpCh
	if !ok {
		return nil, io.EOF
	}
	return pkt, nil
}

func (sn *Session) init() error {
	sn.initFSM()
	// TODO some session handshake works
	return nil
}

func (sn *Session) initFSM() {
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

func (sn *Session) Open(meta []byte) error {
	sn.sm.log.Debugf("session is opening, clientId: %d, sessionId: %d",
		sn.sm.cn.ClientID(), sn.SessionID)

	if sn.fsm.InStates(INIT) {
		var pkt *packet.SessionPacket
		pkt = sn.sm.pf.NewSessionPacket(sn.sm.getID(), meta)
		sn.sm.addInflightSession(pkt.PacketID, sn)

		sn.sessionMtx.RLock()
		if !sn.sessionOK {
			sn.sessionMtx.RUnlock()
			return io.EOF
		}
		sn.writeCh <- pkt
		sn.sessionMtx.RUnlock()

		sync := sn.sm.shub.New(pkt.PacketID, synchub.WithTimeout(30*time.Second))
		event := <-sync.C()
		sn.sm.delInflightSession(pkt.PacketID)
		return event.Error
	}
	// TODO
	return nil
}

// 主动关闭，返回即关闭session
func (sn *Session) Close() error {
	sn.sessionMtx.RLock()
	if !sn.sessionOK {
		sn.sessionMtx.RUnlock()
		return io.EOF
	}

	sn.sm.log.Debugf("session is closing, clientId: %d, sessionId: %d",
		sn.sm.cn.ClientID(), sn.SessionID)

	if sn.fsm.InStates(SESSIONED) {
		pkt := sn.sm.pf.NewDismissPacket(sn.SessionID)
		sn.writeCh <- pkt
		sn.sessionMtx.RUnlock()

		sync := sn.sm.shub.New(pkt.PacketID, synchub.WithTimeout(30*time.Second))
		event := <-sync.C()
		if event.Error != nil {
			sn.sm.log.Debugf("session close err: %s, clientId: %d, sessionId: %d",
				event.Error, sn.sm.cn.ClientID(), sn.SessionID)
			return event.Error
		}
		sn.sm.log.Debugf("session closed, clientId: %d, sessionId: %d",
			sn.sm.cn.ClientID(), sn.SessionID)
		return nil
	}
	sn.sessionMtx.RUnlock()
	return nil
}

func (sn *Session) Start() error {
	err := sn.init()
	if err != nil {
		return err
	}
	go sn.readPkt()
	go sn.writePkt()
	return nil
}

func (sn *Session) writePkt() {

	for {
		select {
		case pkt, ok := <-sn.writeCh:
			if !ok {
				sn.sm.log.Debugf("write packet EOF, clientId: %d, sessionId: %d",
					sn.sm.cn.ClientID(), sn.SessionID)
				return
			}
			ie := sn.handlePkt(pkt, iodefine.OUT)
			switch ie {
			case iodefine.IOSuccess:
				continue
			case iodefine.IOData:
				err := sn.sm.cn.Write(pkt)
				if err != nil {
					sn.sm.log.Debugf("write down err: %s, clientId: %d, sessionId: %d",
						err, sn.sm.cn.ClientID(), sn.SessionID)
					goto CLOSED
				}
			case iodefine.IOClosed:
				goto CLOSED

			case iodefine.IOErr:
				sn.fsm.EmitEvent(ET_ERROR)
				sn.sm.log.Errorf("handle packet return err, clientId: %d, sessionId: %d", sn.sm.cn.ClientID(), sn.SessionID)
				goto CLOSED
			}
		case pkt, ok := <-sn.writeFromUpCh:
			if !ok {
				sn.sm.log.Infof("write from up EOF, clientId: %d, sessionId: %d", sn.sm.cn.ClientID(), sn.SessionID)
				continue
			}
			sn.sm.log.Tracef("to write down, clientId: %d, sessionId: %d, packetId: %d, packetType: %s",
				sn.sm.cn.ClientID(), sn.SessionID, pkt.ID(), pkt.Type().String())
			err := sn.sm.cn.Write(pkt)
			if err != nil {
				if err == io.EOF {
					sn.fsm.EmitEvent(ET_EOF)
					sn.sm.log.Infof("write down EOF, clientId: %d, sessionId: %d, packetId: %d, packetType: %s",
						sn.sm.cn.ClientID(), sn.SessionID, pkt.ID(), pkt.Type().String())
				} else {
					sn.fsm.EmitEvent(ET_ERROR)
					sn.sm.log.Infof("write down err: %s, clientId: %d, sessionId: %d, packetId: %d, packetType: %s",
						err, sn.sm.cn.ClientID(), sn.SessionID, pkt.ID(), pkt.Type().String())

				}
				goto CLOSED
			}
			continue
		}
	}
CLOSED:
	sn.fini()
}

func (sn *Session) readPkt() {
	for {
		pkt, ok := <-sn.readDownCh
		if !ok {
			sn.sm.log.Debugf("read down EOF, clientId: %d, sessionId: %d",
				sn.sm.cn.ClientID(), sn.SessionID)
			return
		}
		sn.sm.log.Tracef("read %s, clientId: %d, sessionId: %d, packetId: %d",
			pkt.Type().String(), sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
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

func (sn *Session) handlePktWrapper(pkt packet.Packet, iotype iodefine.IOType) iodefine.IORet {
	ie := sn.handlePkt(pkt, iodefine.IN)
	switch ie {
	case iodefine.IONewActive:
		return iodefine.IOSuccess

	case iodefine.IONewPassive:
		return iodefine.IOSuccess

	case iodefine.IOClosed:
		return iodefine.IOClosed

	case iodefine.IOData:
		sn.sessionMtx.RLock()
		// TODO
		if !sn.sessionOK {
			sn.sessionMtx.RUnlock()
			return iodefine.IOSuccess
		}
		sn.readToUpCh <- pkt
		sn.sessionMtx.RUnlock()
		return iodefine.IOSuccess

	case iodefine.IOErr:
		// TODO 在遇到IOErr之后，还有必要发送Close吗，需要区分情况
		sn.fsm.EmitEvent(ET_ERROR)
		return iodefine.IOClosed

	default:
		return iodefine.IOSuccess
	}
}

func (sn *Session) handlePkt(pkt packet.Packet, iotype iodefine.IOType) iodefine.IORet {

	switch iotype {
	case iodefine.OUT:
		switch realPkt := pkt.(type) {
		case *packet.SessionPacket:
			err := sn.fsm.EmitEvent(ET_SESSIONSENT)
			if err != nil {
				sn.sm.log.Errorf("emit ET_SESSIONSENT err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.sm.cn.ClientID(), realPkt.SessionID, pkt.ID())
				return iodefine.IOErr
			}
			err = sn.sm.cn.Write(realPkt)
			if err != nil {
				sn.sm.log.Errorf("write SESSION err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.sm.cn.ClientID(), realPkt.SessionID, pkt.ID())
				return iodefine.IOErr
			}
			sn.sm.log.Debugf("write session down succeed, clientId: %d, sessionId: %d, packetId: %d",
				sn.sm.cn.ClientID(), realPkt.SessionID, pkt.ID())
			return iodefine.IOSuccess

		case *packet.DismissPacket:

			if sn.fsm.InStates(DISMISS_RECV, DISMISSED) {
				// TODO 两边同时Close的场景
				sn.sm.shub.Ack(realPkt.PacketID, nil)
				sn.sm.log.Debugf("already been dismissed, clientId: %d, sessionId: %d, packetId: %d",
					sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
				return iodefine.IOSuccess
			}

			err := sn.fsm.EmitEvent(ET_DISMISSSENT)
			if err != nil {
				sn.sm.log.Errorf("emit ET_SESSIONSENT err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
				return iodefine.IOErr
			}
			err = sn.sm.cn.Write(realPkt)
			if err != nil {
				sn.sm.log.Errorf("write DISMISS err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
				return iodefine.IOErr
			}
			sn.sm.log.Debugf("write dismiss down succeed, clientId: %d, sessionId: %d, packetId: %d",
				sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
			return iodefine.IOSuccess

		case *packet.DismissAckPacket:
			err := sn.fsm.EmitEvent(ET_DISMISSACK)
			if err != nil {
				sn.sm.log.Errorf("emit ET_DISMISSACK err: %s, clientId: %d, sessionId: %d, packetId: %d, state: %s",
					err, sn.sm.cn.ClientID(), sn.SessionID, pkt.ID(), sn.fsm.State())
				return iodefine.IOErr
			}
			err = sn.sm.cn.Write(pkt)
			if err != nil {
				sn.sm.log.Errorf("write DISMISSACK err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
				return iodefine.IOErr
			}
			sn.sm.log.Debugf("write dismiss ack down succeed, clientId: %d, sessionId: %d, packetId: %d",
				sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
			return iodefine.IOClosed

		default:
			return iodefine.IOData
		}

	case iodefine.IN:
		switch realPkt := pkt.(type) {
		case *packet.SessionPacket:
			sn.sm.log.Debugf("read session packet, clientId: %d, sessionId: %d, packetId: %d",
				sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
			err := sn.fsm.EmitEvent(ET_SESSIONRECV)
			if err != nil {
				sn.sm.log.Debugf("emit ET_SESSIONRECV err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
				return iodefine.IOErr
			}
			//  分配session id
			sessionId := realPkt.SessionID
			if realPkt.SessionID == packet.SessionIDNull {
				sessionId = sn.sm.getID()
			}
			sn.SessionID = sessionId
			sn.Meta = realPkt.SessionData.Meta
			// TODO session冲突

			// return
			retPkt := sn.sm.pf.NewSessionAckPacket(realPkt.PacketID, sessionId, nil)
			err = sn.sm.cn.Write(retPkt)
			if err != nil {
				sn.sm.log.Errorf("write SESSIONACK err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
				return iodefine.IOErr
			}

			// TODO 端到端一致性
			err = sn.fsm.EmitEvent(ET_SESSIONACK)
			if err != nil {
				sn.sm.log.Debugf("emit ET_SESSIONACK err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
				return iodefine.IOErr
			}
			sn.sm.log.Debugf("write session ack down succeed, clientId: %d, sessionId: %d, packetId: %d",
				sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
			sn.sm.addSession(sn, true)
			// accept session
			// 被动打开，创建session
			return iodefine.IONewPassive

		case *packet.SessionAckPacket:
			sn.sm.log.Debugf("read session ack packet, clientId: %d, sessionId: %d, packetId: %d",
				sn.sm.cn.ClientID(), realPkt.SessionID, pkt.ID())
			err := sn.fsm.EmitEvent(ET_SESSIONACK)
			if err != nil {
				sn.sm.log.Debugf("emit ET_SESSIONACK err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
				return iodefine.IOErr
			}
			sn.SessionID = realPkt.SessionID
			sn.Meta = realPkt.SessionData.Meta
			// 主动打开成功，创建session
			sn.sm.shub.Ack(pkt.ID(), nil)
			sn.sm.addSession(sn, false)

			return iodefine.IONewActive

		case *packet.DismissPacket:
			sn.sm.log.Debugf("read dismiss packet, clientId: %d, sessionId: %d, packetId: %d",
				sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())

			if sn.fsm.InStates(DISMISS_SENT, DISMISSED) {
				// TODO 两端同时发起Close的场景
				retPkt := sn.sm.pf.NewDismissAckPacket(realPkt.PacketID,
					realPkt.SessionID, nil)
				sn.sessionMtx.RLock()
				if !sn.sessionOK {
					sn.sessionMtx.RUnlock()
					return iodefine.IOErr
				}
				sn.writeCh <- retPkt
				sn.sessionMtx.RUnlock()
				return iodefine.IOSuccess

			}
			err := sn.fsm.EmitEvent(ET_DISMISSRECV)
			if err != nil {
				sn.sm.log.Debugf("emit ET_DISMISSRECV err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
				return iodefine.IOErr
			}

			// return
			retPkt := sn.sm.pf.NewDismissAckPacket(realPkt.PacketID,
				realPkt.SessionID, nil)
			sn.sessionMtx.RLock()
			if !sn.sessionOK {
				sn.sessionMtx.RUnlock()
				return iodefine.IOErr
			}
			sn.writeCh <- retPkt
			sn.sessionMtx.RUnlock()

			//return iodefine.IOClosed
			return iodefine.IOSuccess

		case *packet.DismissAckPacket:
			sn.sm.log.Debugf("read dismiss ack packet, clientId: %d, sessionId: %d, packetId: %d",
				sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
			err := sn.fsm.EmitEvent(ET_DISMISSACK)
			if err != nil {
				sn.sm.log.Debugf("emit ET_DISMISSACK err: %s, clientId: %d, sessionId: %d, packetId: %d",
					err, sn.sm.cn.ClientID(), sn.SessionID, pkt.ID())
				return iodefine.IOErr
			}
			sn.sm.shub.Ack(realPkt.PacketID, nil)
			// 主动关闭成功，关闭session
			return iodefine.IOClosed

		default:
			return iodefine.IOData
		}
	}
	return iodefine.IOErr
}

func (sn *Session) close() {}

func (sn *Session) closeWrapper(_ *yafsm.Event) {
	sn.Close()
}

func (sn *Session) fini() {
	sn.onceFini.Do(func() {
		sn.sm.log.Debugf("session finished, clientId: %d, sessionId: %d", sn.sm.cn.ClientID(), sn.SessionID)
		sn.sm.delSession(sn)

		sn.sessionMtx.Lock()
		sn.sessionOK = false
		close(sn.writeCh)

		close(sn.readDownCh)
		close(sn.writeFromUpCh)
		close(sn.readToUpCh)

		sn.sessionMtx.Unlock()

		sn.fsm.EmitEvent(ET_FINI)
		sn.fsm.Close()
	})
}
