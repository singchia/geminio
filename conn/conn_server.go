package conn

import (
	"errors"
	"net"
	"sync"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/geminio/pkg/iodefine"
	"github.com/singchia/go-timer/v2"
	"github.com/singchia/yafsm"
)

type ServerConn struct {
	*baseConn

	// delegation for who wants know the realtime event
	// linke Online Offline, etc.
	dlgt ServerConnDelegate

	// default global client ID factory
	clientIDs id.IDFactory

	closeOnce *sync.Once
	finiOnce  *sync.Once
}

type ServerConnOption func(*ServerConn)

func OptionServerConnPacketFactory(pf packet.PacketFactory) ServerConnOption {
	return func(sc *ServerConn) {
		sc.pf = pf
	}
}

func OptionServerConnTimer(tmr timer.Timer) ServerConnOption {
	return func(sc *ServerConn) {
		sc.tmr = tmr
		sc.tmrOutside = true
	}
}

func OptionServerConnDelegate(dlgt ServerConnDelegate) ServerConnOption {
	return func(sc *ServerConn) {
		sc.dlgt = dlgt
	}
}

func OptionServerConnLogger(log log.Logger) ServerConnOption {
	return func(sc *ServerConn) {
		sc.log = log
	}
}

func OptionServerConnFailedPacket(ch chan packet.Packet) ServerConnOption {
	return func(sc *ServerConn) {
		sc.failedCh = ch
	}
}

func OptionServerConnBufferSize(read, write int) ServerConnOption {
	return func(sc *ServerConn) {
		if read > 0 {
			sc.readOutSize = read
		}
		if write > 0 {
			sc.writeOutSize = write
		}
	}
}

func OptionServerConnClientID(clientID uint64) ServerConnOption {
	return func(sc *ServerConn) {
		sc.clientID = clientID
	}
}

func NewServerConn(netconn net.Conn, opts ...ServerConnOption) (*ServerConn, error) {
	err := error(nil)
	sc := &ServerConn{
		baseConn: &baseConn{
			connOpts: connOpts{
				waitTimeout: 10,
			},
			fsm:          yafsm.NewFSM(),
			netconn:      netconn,
			side:         geminio.RecipientSide,
			connOK:       true,
			readInSize:   32,
			writeOutSize: 32,
			readOutSize:  32,
		},

		closeOnce: new(sync.Once),
		finiOnce:  new(sync.Once),
		clientIDs: id.DefaultIncIDCounter,
	}
	sc.cn = sc
	// options
	for _, opt := range opts {
		opt(sc)
	}
	// io size
	sc.readInCh = make(chan packet.Packet, sc.readInSize)
	sc.writeOutCh = make(chan packet.Packet, sc.writeOutSize)
	sc.readOutCh = make(chan packet.Packet, sc.readOutSize)
	// timer
	if !sc.tmrOutside {
		sc.tmr = timer.NewTimer()
	}
	sc.shub = synchub.NewSyncHub(synchub.OptionTimer(sc.tmr))
	// packet factory
	if sc.pf == nil {
		sc.pf = packet.NewPacketFactory(id.NewIDCounter(id.Even))
	}
	// log
	if sc.log == nil {
		sc.log = log.DefaultLog
	}
	// states
	sc.initFSM()
	// rolling up
	go sc.writePkt()
	go sc.handlePkt()
	// Start channel monitoring for debugging memory issues
	sc.startChannelMonitor(30 * time.Second)
	err = sc.wait()
	if err != nil {
		goto ERR
	}
	return sc, nil
ERR:
	// let fini finish the end
	sc.netconn.Close()
	return nil, err
}

func (sc *ServerConn) wait() error {
	sync := sc.shub.New(sc.getSyncID(), synchub.WithTimeout(10*time.Second))
	// in case of the conn packet is inserted before the wait sync
	go sc.readPkt()
	event := <-sync.C()
	if event.Error != nil {
		sc.log.Infof("wait conn err: %s, clientID: %d, remote: %s, meta: %s",
			event.Error, sc.clientID, sc.netconn.RemoteAddr(), string(sc.meta))
		return event.Error
	}
	return nil
}

func (sc *ServerConn) getSyncID() string {
	return sc.netconn.RemoteAddr().String() + sc.netconn.LocalAddr().String()
}

func (sc *ServerConn) initFSM() {
	init := sc.fsm.AddState(INIT)
	connrecv := sc.fsm.AddState(CONN_RECV)
	conned := sc.fsm.AddState(CONNED)
	closesent := sc.fsm.AddState(CLOSE_SENT)
	closerecv := sc.fsm.AddState(CLOSE_RECV)
	closehalf := sc.fsm.AddState(CLOSE_HALF)
	closed := sc.fsm.AddState(FINI)
	fini := sc.fsm.AddState(FINI)
	sc.fsm.SetState(INIT)

	// events
	sc.fsm.AddEvent(ET_CONNRECV, init, connrecv)
	sc.fsm.AddEvent(ET_CONNACK, connrecv, conned)
	sc.fsm.AddEvent(ET_ERROR, connrecv, connrecv, sc.closeWrapper)

	// possible 4 case of close sent
	// illegal conn
	sc.fsm.AddEvent(ET_CLOSESENT, connrecv, closesent)
	sc.fsm.AddEvent(ET_CLOSESENT, conned, closesent)
	// close and been closed at same time
	sc.fsm.AddEvent(ET_CLOSESENT, closerecv, closesent)
	// close and been closed at same time
	sc.fsm.AddEvent(ET_CLOSESENT, closehalf, closehalf)

	// possible 4 case of close recv
	// illegal conn
	sc.fsm.AddEvent(ET_CLOSERECV, closesent, closerecv)
	sc.fsm.AddEvent(ET_CLOSERECV, conned, closerecv)
	// close and been closed at same time
	sc.fsm.AddEvent(ET_CLOSERECV, closesent, closerecv)
	// close and been closed at same time
	sc.fsm.AddEvent(ET_CLOSERECV, closehalf, closehalf)

	// the 4-way handshake
	sc.fsm.AddEvent(ET_CLOSEACK, closesent, closehalf)
	sc.fsm.AddEvent(ET_CLOSEACK, closerecv, closehalf)
	sc.fsm.AddEvent(ET_CLOSEACK, closehalf, closed)
	// fini at any time is possible
	sc.fsm.AddEvent(ET_FINI, init, fini)
	sc.fsm.AddEvent(ET_FINI, connrecv, fini)
	sc.fsm.AddEvent(ET_FINI, conned, fini)
	sc.fsm.AddEvent(ET_FINI, closesent, fini)
	sc.fsm.AddEvent(ET_FINI, closerecv, fini)
	sc.fsm.AddEvent(ET_FINI, closehalf, fini)
	sc.fsm.AddEvent(ET_FINI, closed, fini)
}

func (sc *ServerConn) writePkt() {
	writeOutCh := sc.writeOutCh
	lastLogTime := time.Now()
	packetCount := 0

	for {
		select {
		case pkt, ok := <-writeOutCh:
			if !ok {
				sc.log.Debugf("conn write done, clientID: %d", sc.clientID)
				return
			}
			packetCount++
			// Log periodically to ensure writePkt() is still running
			now := time.Now()
			if now.Sub(lastLogTime) > 10*time.Second {
				sc.log.Infof("writePkt() is running, clientID: %d, packets processed: %d, writeOutCh remaining: %d/%d",
					sc.clientID, packetCount, len(writeOutCh), sc.writeOutSize)
				lastLogTime = now
			}
			sc.log.Tracef("conn write down, clientID: %d, packetID: %d, packetType: %s, writeOutCh remaining: %d/%d",
				sc.clientID, pkt.ID(), pkt.Type().String(), len(writeOutCh), sc.writeOutSize)
			writeStart := time.Now()

			ret := sc.handleOut(pkt)
			if ret == iodefine.IOErr {
				sc.log.Errorf("handle out packet err, clientID: %d", sc.clientID)
				goto FINI
			}
			if ret == iodefine.IOClosed {
				sc.log.Debugf("handle out packet done, clientID: %d", sc.clientID)
				goto FINI
			}
			if ret == iodefine.IODiscard {
				// Log when packet is discarded to track if writeOutCh is full
				sc.log.Debugf("handle out packet discarded, clientID: %d, packetID: %d, packetType: %s",
					sc.clientID, pkt.ID(), pkt.Type().String())
			}

			writeDuration := time.Since(writeStart)
			if writeDuration > 5*time.Second {
				sc.log.Warnf("data packet write took too long, clientID: %d, packetID: %d, writeDuration: %v (network may be blocking)",
					sc.clientID, pkt.ID(), writeDuration)
			}
		}
	}
FINI:
	sc.log.Debugf("writePkt done, clientID: %d", sc.clientID)
	sc.finiOnce.Do(sc.fini)
}

func (sc *ServerConn) handlePkt() {
	readInCh := sc.readInCh
	for {
		select {
		case pkt, ok := <-readInCh:
			if !ok {
				goto FINI
			}
			sc.log.Tracef("conn read in packet, clientID: %d, packetID: %d, packetType: %s",
				sc.clientID, pkt.ID(), pkt.Type().String())
			ret := sc.handleIn(pkt)
			if ret == iodefine.IOErr {
				sc.log.Errorf("conn handle in packet err, clientID: %d", sc.clientID)
				goto FINI
			}
			if ret == iodefine.IOClosed {
				sc.log.Debugf("conn handle in packet done, clientID: %d", sc.clientID)
				goto FINI
			}
		}
	}
FINI:
	sc.log.Debugf("handle pkt done, clientID: %d", sc.clientID)

	// only handlePkt leads this fini, and reclaims all channels and other resources
	sc.finiOnce.Do(sc.fini)
}

func (sc *ServerConn) handleIn(pkt packet.Packet) iodefine.IORet {
	switch realPkt := pkt.(type) {
	case *packet.ConnPacket:
		return sc.handleInConnPacket(realPkt)
	case *packet.DisConnPacket:
		return sc.handleInDisConnPacket(realPkt)
	case *packet.DisConnAckPacket:
		return sc.handleInDisConnAckPacket(realPkt)
	case *packet.HeartbeatPacket:
		return sc.handleInHeartbeatPacket(realPkt)
	default:
		return sc.handleInDataPacket(pkt)
	}
}

func (sc *ServerConn) handleOut(pkt packet.Packet) iodefine.IORet {
	switch realPkt := pkt.(type) {
	case *packet.ConnAckPacket:
		return sc.handleOutConnAckPacket(realPkt)
	case *packet.DisConnPacket:
		return sc.handleOutDisConnPacket(realPkt)
	case *packet.DisConnAckPacket:
		return sc.handleOutDisConnAckPacket(realPkt)
	case *packet.HeartbeatAckPacket:
		return sc.handleOutHeartbeatAckPacket(realPkt)
	default:
		return sc.handleOutDataPacket(pkt)
	}
}

// input packet
func (sc *ServerConn) handleInConnPacket(pkt *packet.ConnPacket) iodefine.IORet {
	sc.log.Debugf("read conn packet succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(pkt.ConnData.Meta))

	err := sc.fsm.EmitEvent(ET_CONNRECV)
	if err != nil {
		sc.log.Errorf("emit ET_CONNRECV err: %s, clientID: %d, packetID: %d, state: %s",
			err, sc.clientID, pkt.ID(), sc.fsm.State())
		sc.shub.Error(sc.getSyncID(), err)
		return iodefine.IOErr
	}

	sc.meta = pkt.ConnData.Meta
	if pkt.ClientIDAcquire() {
		if sc.dlgt != nil {
			sc.clientID, err = sc.dlgt.GetClientID(pkt.ClientID, sc.meta)
		}
		if sc.clientID == 0 && err == nil {
			// if delegate returns 0 meaning use a ID by inner
			sc.clientID, err = sc.clientIDs.GetIDByMeta(sc.meta)
		}
		if err != nil {
			sc.log.Warnf("get ID err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
				err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
			retPkt := sc.pf.NewConnAckPacket(pkt.PacketID, sc.clientID, err)
			sc.writeOutCh <- retPkt
			return iodefine.IOSuccess
		}
	} else {
		// TODO server must use this clientID, we should check if the clientID legal
		sc.clientID = pkt.ClientID
	}

	if sc.dlgt != nil {
		err = sc.dlgt.ConnOnline(sc)
		if err != nil {
			sc.log.Errorf("online err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
				err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))

			retPkt := sc.pf.NewConnAckPacket(pkt.PacketID, sc.clientID, err)
			sc.writeOutCh <- retPkt
			return iodefine.IOSuccess
		}
	}
	// the first packet received.
	sc.shub.Ack(sc.getSyncID(), nil)
	retPkt := sc.pf.NewConnAckPacket(pkt.PacketID, sc.clientID, nil)
	sc.writeOutCh <- retPkt

	// set the heartbeat
	sc.heartbeat = pkt.Heartbeat
	sc.resetHeartbeatTimeout()
	return iodefine.IOSuccess
}

func (sc *ServerConn) handleInHeartbeatPacket(pkt *packet.HeartbeatPacket) iodefine.IORet {
	ok := sc.fsm.InStates(CONNED)
	if !ok {
		sc.log.Errorf("heartbeat at non-CONNED state, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta), sc.fsm.State())
		return iodefine.IODiscard
	}
	// reset heartbeat timeout
	sc.resetHeartbeatTimeout()

	retPkt := sc.pf.NewHeartbeatAckPacket(pkt.PacketID)
	sc.writeOutCh <- retPkt
	if sc.dlgt != nil {
		sc.dlgt.Heartbeat(sc)
	}
	sc.log.Debugf("send heartbeat ack succeed, clientID: %d, PacketID: %d, packetType: %s",
		sc.clientID, pkt.ID(), pkt.Type().String())
	return iodefine.IOSuccess
}

func (sc *ServerConn) handleInDataPacket(pkt packet.Packet) iodefine.IORet {
	ok := sc.fsm.InStates(CONNED)
	if !ok {
		sc.log.Debugf("data at non CONNED, clientID: %d, packetID: %d, remote: %s, meta: %s",
			sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
		if sc.failedCh != nil {
			sc.failedCh <- pkt
		}
		return iodefine.IODiscard
	}
	// Reset heartbeat timeout when receiving any data packet
	// Any packet indicates the connection is alive, so extend the heartbeat wait time
	sc.resetHeartbeatTimeout()
	sc.readOutCh <- pkt
	return iodefine.IOSuccess
}

// output packet
func (sc *ServerConn) handleOutConnAckPacket(pkt *packet.ConnAckPacket) iodefine.IORet {
	if pkt.RetCode == packet.RetCodeERR {
		err := sc.fsm.EmitEvent(ET_ERROR)
		if err != nil {
			sc.log.Errorf("emit ET_ERROR err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
				pkt.ConnData.Error, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
			return iodefine.IOErr
		}
		err = sc.dowritePkt(pkt, true)
		if err != nil {
			sc.log.Errorf("send conn ack packet err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
				err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
			return iodefine.IOErr
		}
		// this situation shouldn't be seen as connected, so don't set onlined.
		return iodefine.IOSuccess
	}
	err := sc.fsm.EmitEvent(ET_CONNACK)
	if err != nil {
		sc.log.Errorf("emit ET_CONNACK err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta), sc.fsm.State())
		return iodefine.IOErr
	}
	err = sc.dowritePkt(pkt, true)
	if err != nil {
		sc.log.Errorf("send conn ack packet err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
			err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
		return iodefine.IOErr
	}
	sc.log.Debugf("send conn ack succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
	sc.onlined = true
	return iodefine.IOSuccess
}

func (sc *ServerConn) Close() {
	sc.closeOnce.Do(func() {
		sc.connMtx.RLock()
		if !sc.connOK {
			sc.connMtx.RUnlock()
			return
		}

		sc.log.Debugf("client is closing, clientID: %d, remote: %s, meta: %s",
			sc.clientID, sc.netconn.RemoteAddr(), string(sc.meta))

		sc.connMtx.RUnlock()
		pkt := sc.pf.NewDisConnPacket()
		sc.writeOutCh <- pkt

	})
}

func (sc *ServerConn) closeWrapper(_ *yafsm.Event) {
	sc.Close()
}

// finish and reclaim resources
func (sc *ServerConn) fini() {
	sc.log.Debugf("client finishing, clientID: %d, remote: %s, meta: %s",
		sc.clientID, sc.netconn.RemoteAddr(), string(sc.meta))

	// only onlined Conn need to be notified
	if sc.dlgt != nil && sc.onlined {
		// not that delegate is different from client's delegate
		sc.dlgt.ConnOffline(sc)
	}

	// if we are in conn stage,
	sc.shub.Error(sc.getSyncID(), errors.New("connection closed"))

	// cancel the heartbeat
	if sc.hbTick != nil {
		sc.hbTick.Cancel()
		sc.hbTick = nil
	}

	// collect shub
	sc.shub.Close()
	sc.shub = nil
	// collect net.Conn
	sc.netconn.Close()
	// lock protect conn status and input resource
	sc.connMtx.Lock()
	sc.connOK = false
	sc.connMtx.Unlock()

	// the outside should care about channel status
	close(sc.readOutCh)
	// writeOutCh must be cared since writhPkt might quit first
	close(sc.writeOutCh)
	for pkt := range sc.writeOutCh {
		if sc.failedCh != nil && !packet.ConnLayer(pkt) {
			sc.failedCh <- pkt
		}
	}
	// collect timer
	if !sc.tmrOutside {
		sc.tmr.Close()
	}
	sc.tmr = nil
	// collect fsm
	sc.fsm.EmitEvent(ET_FINI)
	sc.fsm.Close()
	sc.fsm = nil
	// collect id
	sc.clientIDs = nil

	sc.readInCh, sc.writeOutCh = nil, nil

	sc.log.Debugf("client finished, clientID: %d, remote: %s, meta: %s",
		sc.clientID, sc.netconn.RemoteAddr(), string(sc.meta))
}

// resetHeartbeatTimeout resets the heartbeat timeout timer
// This should be called whenever any packet is received, as any packet indicates the connection is alive
func (sc *ServerConn) resetHeartbeatTimeout() {
	if sc.hbTick != nil {
		sc.hbTick.Cancel()
	}
	sc.hbTick = sc.tmr.Add(time.Duration(sc.heartbeat)*2*time.Second, timer.WithHandler(sc.waitHBTimeout))
}

func (sc *ServerConn) waitHBTimeout(event *timer.Event) {
	if event.Error == timer.ErrTimerForceClosed {
		sc.log.Debugf("wait HEARTBEAT err: %s, clientID: %d, remote: %s, meta: %s",
			event.Error, sc.clientID, sc.netconn.RemoteAddr(), string(sc.meta))
	} else {
		sc.log.Errorf("wait HEARTBEAT err: %s, clientID: %d, remote: %s, meta: %s",
			event.Error, sc.clientID, sc.netconn.RemoteAddr(), string(sc.meta))
	}
	// changed from sc.netconn.Close() to sc.Close()
	sc.Close()
}
