package session

import (
	"errors"
	"io"
	"sync"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/geminio/pkg/iodefine"
	"github.com/singchia/go-timer/v2"
)

type SessionMgr struct {
	cn conn.Conn

	sessionIDs id.IDFactory // set nil in client
	tmr        timer.Timer
	tmrOutside bool
	shub       *synchub.SyncHub
	pf         *packet.PacketFactory
	log        log.Logger

	DefaultSn        *Session
	sessions         sync.Map // key: sessionID, value: session
	inflightSessions sync.Map // key: packetId, value: session

	sessionMgrOK  bool
	sessionMgrMtx sync.RWMutex

	sessionAcceptCh chan *Session
	sessionCloseCh  chan *Session
}

type SessionMgrOption func(*SessionMgr)

func OptionSessionMgrAcceptCh() SessionMgrOption {
	return func(sm *SessionMgr) {
		sm.sessionAcceptCh = make(chan *Session, 128)
	}
}

func OptionSessionMgrCloseCh() SessionMgrOption {
	return func(sm *SessionMgr) {
		sm.sessionCloseCh = make(chan *Session, 128)
	}
}

func OptionPacketFactory(pf *packet.PacketFactory) SessionMgrOption {
	return func(sm *SessionMgr) {
		sm.pf = pf
	}
}

func OptionLoggy(log log.Logger) SessionMgrOption {
	return func(sm *SessionMgr) {
		sm.log = log
	}
}

func OptionTimer(tmr timer.Timer) SessionMgrOption {
	return func(sm *SessionMgr) {
		sm.tmr = tmr
		sm.tmrOutside = true
	}
}

func NewSessionMgr(cn conn.Conn, opts ...SessionMgrOption) (*SessionMgr, error) {
	sm := &SessionMgr{
		cn:           cn,
		sessionMgrOK: true,
	}

	// session id counter
	if sm.cn.Side() == conn.ServerSide {
		sm.sessionIDs = id.NewIDCounter(id.Even)
		sm.sessionIDs.ReserveID(packet.SessionID1)
	}
	// options
	for _, opt := range opts {
		opt(sm)
	}
	// sync hub
	if !sm.tmrOutside {
		sm.tmr = timer.NewTimer()
	}
	sm.shub = synchub.NewSyncHub(synchub.OptionTimer(sm.tmr))
	// log
	if sm.log == nil {
		sm.log = log.DefaultLog
	}
	// add default session
	sn := NewSession(sm,
		OptionSessionState(SESSIONED))
	sn.SessionID = packet.SessionID1
	err := sn.Start()
	if err != nil {
		sm.log.Errorf("session start err: %s, clientID: %d, sessionID: %d",
			err, cn.ClientID(), packet.SessionID1)
		return nil, err
	}
	sm.DefaultSn = sn
	sm.addSession(sn, false)
	return sm, nil
}

func (sm *SessionMgr) getID() uint64 {
	if sm.cn.Side() == conn.ClientSide {
		return packet.SessionIDNull
	}
	return sm.sessionIDs.GetID()
}

func (sm *SessionMgr) Start() error {
	go sm.readPkt()
	return nil
}

func (sm *SessionMgr) Close() error {
	sm.log.Debugf("session manager is closing, clientID: %d", sm.cn.ClientID())
	sm.sessions.Range(func(key, value interface{}) bool {
		sn := value.(*Session)
		sn.Close()
		return true
	})
	sm.log.Debugf("session manager closed, clientID: %d", sm.cn.ClientID)
	return nil
}

// 回收资源
func (sm *SessionMgr) fini() {
	sm.inflightSessions.Range(func(key, value interface{}) bool {
		sn := value.(*Session)
		sn.fini()
		return true
	})
	sm.sessions.Range(func(key, value interface{}) bool {
		sn := value.(*Session)
		sn.fini()
		return true
	})

	// accept和close channel的关闭一定要在session能够接收到通知之后
	sm.sessionMgrMtx.Lock()
	sm.sessionMgrOK = false
	if sm.sessionAcceptCh != nil {
		close(sm.sessionAcceptCh)
	}
	if sm.sessionCloseCh != nil {
		close(sm.sessionCloseCh)
	}
	sm.sessionMgrMtx.Unlock()
	sm.shub.Close()
	if !sm.tmrOutside {
		sm.tmr.Close()
	}
	sm.log.Debugf("session manager finished, clientID: %d", sm.cn.ClientID)
}

func (sm *SessionMgr) OpenSession(meta []byte) (*Session, error) {
	sm.sessionMgrMtx.RLock()
	if !sm.sessionMgrOK {
		sm.sessionMgrMtx.RUnlock()
		return nil, io.EOF
	}
	sm.sessionMgrMtx.RUnlock()

	sn := NewSession(sm)
	err := sn.Start()
	if err != nil {
		sm.log.Errorf("session start err: %s", err, sn.sm.cn.ClientID, packet.SessionID1)
		return nil, err
	}
	err = sn.Open(meta)
	if err != nil {
		sm.log.Errorf("session open err: %s", err, sn.sm.cn.ClientID, packet.SessionID1)
	}
	return sn, err
}

func (sm *SessionMgr) AcceptSession() (*Session, error) {
	if sm.sessionAcceptCh == nil {
		return nil, errors.New("uninitialized new session channel")
	}
	sn, ok := <-sm.sessionAcceptCh
	if !ok {
		return nil, io.EOF
	}
	return sn, nil
}

func (sm *SessionMgr) ClosedSession() (*Session, error) {
	if sm.sessionCloseCh == nil {
		return nil, errors.New("uninitialized closed session channel")
	}
	sn, ok := <-sm.sessionCloseCh
	if !ok {
		return nil, io.EOF
	}
	return sn, nil
}

func (sm *SessionMgr) addInflightSession(packetId uint64, sn *Session) {
	sm.inflightSessions.Store(packetId, sn)
}

func (sm *SessionMgr) delInflightSession(packetId uint64) {
	sm.inflightSessions.Delete(packetId)
}

func (sm *SessionMgr) addSession(sn *Session, passive bool) {
	sm.log.Debugf("clientID: %d, add sessionID: %d, passive: %v",
		sm.cn.ClientID, sn.SessionID, passive)
	sm.sessions.Store(sn.SessionID, sn)

	sm.sessionMgrMtx.RLock()
	if !sm.sessionMgrOK {
		sm.sessionMgrMtx.RUnlock()
		return
	}
	if passive && sm.sessionAcceptCh != nil {
		sm.sessionAcceptCh <- sn
	}
	sm.sessionMgrMtx.RUnlock()
}

func (sm *SessionMgr) delSession(sn *Session) {
	sm.log.Debugf("clientID: %d, del sessionID: %d", sm.cn.ClientID, sn.SessionID)
	sm.sessions.Delete(sn.SessionID)

	sm.sessionMgrMtx.RLock()
	if !sm.sessionMgrOK {
		sm.sessionMgrMtx.RUnlock()
		return
	}
	if sm.sessionCloseCh != nil {
		sm.sessionCloseCh <- sn
	}
	sm.sessionMgrMtx.RUnlock()
}

func (sm *SessionMgr) readPkt() {
	for {
		pkt, err := sm.cn.Read()
		if err != nil {
			if err == io.EOF {
				sm.log.Debugf("session mgr read down EOF, clientID: %d", sm.cn.ClientID)
			} else {
				sm.log.Debugf("session mgr read down err: %s, clientID: %d", err, sm.cn.ClientID)
			}
			goto CLOSED
		}
		sm.handlePkt(pkt)
	}
CLOSED:
	sm.fini()
}

func (sm *SessionMgr) handlePkt(pkt packet.Packet) {
	switch pkt.(type) {
	case *packet.SessionPacket:
		// new session
		sn := NewSession(sm)
		sn.Start()
		sn.readDownCh <- pkt

	case *packet.SessionAckPacket:
		sn, ok := sm.inflightSessions.Load(pkt.ID())
		if !ok {
			sm.log.Errorf("clientID: %d, unable to find inflight sessionID: %d",
				sm.cn.ClientID, pkt.ID())
			return
		}
		// 同步
		sn.(*Session).handlePktWrapper(pkt, iodefine.IN)

	default:
		sessionor, ok := pkt.(packet.SessionLayer)
		if !ok {
			sm.log.Errorf("packet doesn't have sessionID, clientID: %d, packetId: %d, packetType: %s",
				sm.cn.ClientID, pkt.ID(), pkt.Type().String())
			return
		}
		sessionID := sessionor.SessionID()
		sn, ok := sm.sessions.Load(sessionID)
		if !ok {
			sm.log.Errorf("clientID: %d, unable to find sessionID: %d, packetId: %d, packetType: %s",
				sm.cn.ClientID, sessionID, pkt.ID(), pkt.Type().String())
			return
		}

		sm.log.Tracef("clientID: %d, sessionID: %d, packetId: %d, read %s",
			sm.cn.ClientID, sessionID, pkt.ID(), pkt.Type().String())
		sn.(*Session).sessionMtx.RLock()
		if !sn.(*Session).sessionOK {
			sn.(*Session).sessionMtx.RUnlock()
			return
		}
		sn.(*Session).readDownCh <- pkt
		sn.(*Session).sessionMtx.RUnlock()
	}
}
