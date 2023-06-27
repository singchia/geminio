package session

import (
	"sync"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/go-timer/v2"
)

type sessionMgrOpts struct {
	// global client ID factory, set nil at client side
	sessionIDs id.IDFactory
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
	sessionAcceptCh        chan *session
	sessionAcceptChOutsite bool
}

type sessionMgr struct {
	// under layer
	cn conn.Conn
	// options
	sessionMgrOpts

	// sync hub
	shub *synchub.SyncHub

	// sessions
	sessionIDs     id.IDFactory // set nil in client
	defaultSession *session
	// mtx protect follows
	mtx                 sync.RWMutex
	mgrOK               bool
	sessions            map[uint64]*session // key: sessionID, value: session
	negotiatingSessions map[uint64]*session
}

type SessionMgrOption func(*sessionMgr)

func OptionSessionMgrAcceptCh() SessionMgrOption {
	return func(sm *sessionMgr) {
		sm.sessionAcceptCh = make(chan *session, 128)
	}
}

func OptionPacketFactory(pf *packet.PacketFactory) SessionMgrOption {
	return func(sm *sessionMgr) {
		sm.pf = pf
	}
}

func OptionLogger(log log.Logger) SessionMgrOption {
	return func(sm *sessionMgr) {
		sm.log = log
	}
}

func OptionTimer(tmr timer.Timer) SessionMgrOption {
	return func(sm *sessionMgr) {
		sm.tmr = tmr
		sm.tmrOutside = true
	}
}

func OptionDelegate(dlgt Delegate) SessionMgrOption {
	return func(sm *sessionMgr) {
		sm.dlgt = dlgt
	}
}

func NewSessionMgr(cn conn.Conn, opts ...SessionMgrOption) (*sessionMgr, error) {
	sm := &sessionMgr{
		cn:    cn,
		mgrOK: true,
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
	sn, err := NewSession(sm,
		OptionSessionState(SESSIONED))
	if err != nil {
		sm.log.Errorf("new session err: %s, clientID: %d, sessionID: %d",
			err, cn.ClientID(), packet.SessionID1)
		return nil, err
	}
	sn.sessionID = packet.SessionID1
	sm.defaultSession = sn
	sm.sessions[packet.SessionID1] = sn
	go sm.readPkt()
	return sm, nil
}

func (sm *sessionMgr) SessionOnline(sn *session, meta []byte) error {
	sm.mtx.Lock()
	defer sm.mtx.Unlock()

	if !sm.mgrOK {
		return ErrOperationOnClosedSessionMgr
	}
	// remove from the negotiating sessions, and add to ready sessions.
	_, ok := sm.negotiatingSessions[sn.negotiatingID]
	if ok {
		delete(sm.negotiatingSessions, sn.negotiatingID)
	} else {
		if sm.dlgt != nil {
			sm.dlgt.SessionOnline(sn)
		}
		if sm.sessionAcceptCh != nil {
			// this must not be blocked.
			sm.sessionAcceptCh <- sn
		}
	}
	sm.sessions[sn.sessionID] = sn
	return nil
}

func (sm *sessionMgr) SessionOffline(sn *session, meta []byte) error {
	sm.log.Debugf("clientID: %d, del sessionID: %d", sm.cn.ClientID, sn.SessionID)
	sm.mtx.Lock()
	defer sm.mtx.Unlock()

	sn, ok := sm.sessions[sn.sessionID]
	if ok {
		delete(sm.sessions, sn.sessionID)
		if sm.dlgt != nil {
			sm.dlgt.SessionOffline(sn)
		}
	}
	// unsucceed session
	return ErrSessionNotFound
}

func (sm *sessionMgr) getID() uint64 {
	if sm.cn.Side() == conn.ClientSide {
		return packet.SessionIDNull
	}
	return sm.sessionIDs.GetID()
}

func (sm *sessionMgr) Close() error {
	sm.log.Debugf("session manager is closing, clientID: %d", sm.cn.ClientID())
	sm.sessions.Range(func(key, value interface{}) bool {
		value.(*session).CloseWait()
		return true
	})
	sm.negotiatingSessions.Range(func(key, value interface{}) bool {
		value.(*session).CloseWait()
		return true
	})
	sm.log.Debugf("session manager closed, clientID: %d", sm.cn.ClientID)
	return nil
}

// OpenSession blocks until success or failed
func (sm *sessionMgr) OpenSession(meta []byte) (Session, error) {
	sm.mtx.RLock()
	if !sm.mgrOK {
		sm.mtx.RUnlock()
		return nil, ErrOperationOnClosedSessionMgr
	}
	sm.mtx.RUnlock()

	negotiatingID := sm.sessionIDs.GetID()
	sn, err := NewSession(sm, OptionSessionNegotiatingID(negotiatingID))
	if err != nil {
		sm.log.Errorf("new session err: %s, clientID: %d", err, sm.cn.ClientID())
		return nil, err
	}
	sm.mtx.Lock()
	sm.negotiatingSessions[negotiatingID] = sn
	sm.mtx.Unlock()
	// Open only happends at client side
	// Open take times, shouldn't be locked
	err = sn.Open()
	if err != nil {
		sm.log.Errorf("session open err: %s, clientID: %d, negotiatingID: %d", err, sm.cn.ClientID(), sn.negotiatingID)
		sm.mtx.Lock()
		delete(sm.negotiatingSessions, negotiatingID)
		sm.mtx.Unlock()
		return nil, err
	}
	sm.mtx.Lock()
	defer sm.mtx.Unlock()
	if !sm.mgrOK {
		// the logic on negotiatingSessions is tricky, take care of it.
		delete(sm.negotiatingSessions, negotiatingID)
		sn.Close()
		return nil, ErrOperationOnClosedSessionMgr
	}
	return sn, nil
}

// AcceptSession blocks until success or failed
func (sm *sessionMgr) AcceptSession() (Session, error) {
	sn, ok := <-sm.sessionAcceptCh
	if !ok {
		return nil, ErrOperationOnClosedSessionMgr
	}
	return sn, nil
}

func (sm *sessionMgr) readPkt() {
	for {
		pkt, err := sm.cn.Read()
		if err != nil {
			sm.log.Debugf("session mgr read down err: %s, clientID: %d", err, sm.cn.ClientID)
			goto FINI
		}
		sm.handlePkt(pkt)
	}
FINI:
	// if the session manager got an error, all session must be finished in time
	sm.fini()
}

func (sm *sessionMgr) handlePkt(pkt packet.Packet) {
	switch realPkt := pkt.(type) {
	case *packet.SessionPacket:
		// new negotiating session
		negotiatingID := sm.sessionIDs.GetID()
		sn, err := NewSession(sm, OptionSessionNegotiatingID(negotiatingID))
		if err != nil {
			sm.log.Errorf("new session err: %s, clientID: %d", err, sm.cn.ClientID())
			return
		}
		sm.mtx.Lock()
		sm.negotiatingSessions[negotiatingID] = sn
		sm.mtx.Unlock()
		sn.readInCh <- pkt

	case *packet.SessionAckPacket:
		sm.mtx.RLock()
		sn, ok := sm.negotiatingSessions[realPkt.NegotiateID]
		sm.mtx.RUnlock()
		if !ok {
			// TODO we must warn the session initiator
			sm.log.Errorf("clientID: %d, unable to find negotiating sessionID: %d",
				sm.cn.ClientID, pkt.ID())
			return
		}
		sn.readInCh <- pkt

	default:
		snPkt, ok := pkt.(packet.SessionLayer)
		if !ok {
			sm.log.Errorf("packet doesn't have sessionID, clientID: %d, negotiatingID: %d, packetType: %s",
				sm.cn.ClientID, pkt.ID(), pkt.Type().String())
			return
		}
		sessionID := snPkt.SessionID()
		sm.mtx.RLock()
		sn, ok := sm.negotiatingSessions[sessionID]
		sm.mtx.RUnlock()
		if !ok {
			sm.log.Errorf("clientID: %d, unable to find sessionID: %d, negotiatingID: %d, packetType: %s",
				sm.cn.ClientID, sessionID, pkt.ID(), pkt.Type().String())
			return
		}

		sm.log.Tracef("clientID: %d, sessionID: %d, negotiatingID: %d, read %s",
			sm.cn.ClientID, sessionID, pkt.ID(), pkt.Type().String())
		sn.readInCh <- pkt
	}
}

func (sm *sessionMgr) fini() {
	sm.negotiatingSessions.Range(func(key, value interface{}) bool {
		sn := value.(*session)
		return true
	})
	sm.sessions.Range(func(key, value interface{}) bool {
		sn := value.(*session)
		return true
	})

	// accept和close channel的关闭一定要在session能够接收到通知之后
	sm.sessionMgrMtx.Lock()
	sm.mgrOK = false
	close(sm.sessionAcceptCh)
	sm.sessionMgrMtx.Unlock()

	sm.shub.Close()
	if !sm.tmrOutside {
		sm.tmr.Close()
	}
	sm.log.Debugf("session manager finished, clientID: %d", sm.cn.ClientID)
}
