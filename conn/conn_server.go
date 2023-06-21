package conn

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/geminio/pkg/iodefine"
	"github.com/singchia/go-timer/v2"
	"github.com/singchia/yafsm"
)

type ServerConn struct {
	*baseConn

	dlgt ServerConnDelegate

	closeOnce *sync.Once
	//finiOnce    *sync.Once
	//offlineOnce *sync.Once

	clientIDs id.IDFactory // global IDs
}

type ServerConnOption func(*ServerConn)

func OptionServerConnPacketFactory(pf *packet.PacketFactory) ServerConnOption {
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

func NewServerConn(netconn net.Conn, opts ...ServerConnOption) (*ServerConn, error) {
	err := error(nil)
	sc := &ServerConn{
		baseConn: &baseConn{
			connOpts: connOpts{
				waitTimeout: 10,
			},
			fsm:        yafsm.NewFSM(),
			netconn:    netconn,
			side:       ServerSide,
			connOK:     true,
			readInCh:   make(chan packet.Packet, 16),
			writeOutCh: make(chan packet.Packet, 16),
			readOutCh:  make(chan packet.Packet, 16),
			writeInCh:  make(chan packet.Packet, 16),
		},

		closeOnce: new(sync.Once),
		//finiOnce:    new(sync.Once),
		//offlineOnce: new(sync.Once),
		clientIDs: id.NewIDCounter(id.Unique),
	}
	sc.cn = sc
	// options
	for _, opt := range opts {
		opt(sc)
	}
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
	go sc.readPkt()
	go sc.writePkt()
	go sc.handlePkt()
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
	event := <-sync.C()
	if event.Error != nil {
		sc.log.Errorf("wait conn timeout, clientID: %d, remote: %s, meta: %s",
			sc.clientID, sc.netconn.RemoteAddr(), string(sc.meta))
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
	sc.fsm.AddEvent(ET_ERROR, init, closed)
	sc.fsm.AddEvent(ET_ERROR, connrecv, connrecv, sc.closeWrapper)
	sc.fsm.AddEvent(ET_CLOSESENT, connrecv, closesent) // illegal conn
	sc.fsm.AddEvent(ET_CLOSESENT, conned, closesent)
	sc.fsm.AddEvent(ET_CLOSESENT, closerecv, closesent) // close and been closed at same time
	sc.fsm.AddEvent(ET_CLOSESENT, closehalf, closehalf) // close and been closed at same time
	sc.fsm.AddEvent(ET_CLOSERECV, closesent, closerecv) // illegal conn
	sc.fsm.AddEvent(ET_CLOSERECV, conned, closerecv)
	sc.fsm.AddEvent(ET_CLOSERECV, closesent, closerecv) // close and been closed at same time
	sc.fsm.AddEvent(ET_CLOSERECV, closehalf, closehalf) // close and been closed at same time
	sc.fsm.AddEvent(ET_CLOSEACK, closesent, closehalf)
	sc.fsm.AddEvent(ET_CLOSEACK, closerecv, closehalf)
	sc.fsm.AddEvent(ET_CLOSEACK, closehalf, closed)
	sc.fsm.AddEvent(ET_FINI, init, fini)
	sc.fsm.AddEvent(ET_FINI, connrecv, fini)
	sc.fsm.AddEvent(ET_FINI, conned, fini)
	sc.fsm.AddEvent(ET_FINI, closesent, fini)
	sc.fsm.AddEvent(ET_FINI, closerecv, fini)
	sc.fsm.AddEvent(ET_FINI, closehalf, fini)
	sc.fsm.AddEvent(ET_FINI, closed, fini)
}

func (sc *ServerConn) Read() (packet.Packet, error) {
	pkt, ok := <-sc.readOutCh
	if !ok {
		sc.readOutCh = nil
		return nil, io.EOF
	}
	return pkt, nil
}

func (sc *ServerConn) Write(pkt packet.Packet) error {
	sc.connMtx.RLock()
	defer sc.connMtx.RUnlock()
	if !sc.connOK {
		return io.EOF
	}
	sc.writeInCh <- pkt
	return nil
}

func (sc *ServerConn) writePkt() {
	writeOutCh := sc.writeOutCh
	err := error(nil)

	for {
		select {
		case pkt, ok := <-writeOutCh:
			if !ok {
				sc.log.Infof("conn write done, clientID: %d", sc.clientID)
				return
			}
			sc.log.Tracef("conn write down, clientID: %d, packetID: %d, packetType: %s",
				sc.clientID, pkt.ID(), pkt.Type().String())
			err = sc.dowritePkt(pkt, true)
			if err != nil {
				return
			}
		}
	}
}

func (sc *ServerConn) dowritePkt(pkt packet.Packet, record bool) error {
	err := packet.EncodeToWriter(pkt, sc.netconn)
	if err != nil {
		sc.log.Errorf("conn write down err: %s, clientID: %d, packetID: %d",
			err, sc.clientID, pkt.ID())
		if record && sc.failedCh != nil {
			sc.failedCh <- pkt
		}
	}
	return err
}

func (sc *ServerConn) readPkt() {
	readInCh := sc.readInCh
	for {
		pkt, err := packet.DecodeFromReader(sc.netconn)
		if err != nil {
			if iodefine.ErrUseOfClosedNetwork(err) {
				sc.log.Infof("conn read down closed, clientID: %d", sc.clientID)
			} else {
				sc.log.Infof("conn read down err: %s, clientID: %d",
					err, sc.clientID)
			}
			goto FINI
		}
		sc.log.Tracef("read %s , clientID: %d, packetID: %d, packetType: %s",
			pkt.Type().String(), sc.clientID, pkt.ID(), pkt.Type().String())
		readInCh <- pkt
	}
FINI:
	close(readInCh)
}

func (sc *ServerConn) handlePkt() {
	readInCh := sc.readInCh
	writeInCh := sc.writeInCh
	for {
		select {
		case pkt, ok := <-readInCh:
			if !ok {
				goto FINI
			}
			ret := sc.handleIn(pkt)
			if ret == iodefine.IOErr {
				sc.log.Errorf("handle in packet err, clientID: %d", sc.clientID)
				goto FINI
			}
			if ret == iodefine.IOClosed {
				sc.log.Infof("handle in packet done, clientID: %d", sc.clientID)
				goto FINI
			}
		case pkt := <-writeInCh:
			ret := sc.handleOut(pkt)
			if ret == iodefine.IOErr {
				sc.log.Errorf("handle out packet err, clientID: %d", sc.clientID)
				goto FINI
			}
			if ret == iodefine.IOClosed {
				sc.log.Infof("handle out packet done, clientID: %d", sc.clientID)
				goto FINI
			}
		}
	}
FINI:
	sc.log.Debugf("handle pkt done, clientID: %d", sc.clientID)
	if sc.dlgt != nil && sc.clientID != 0 {
		//sc.offlineOnce.Do(func() { sc.dlgt.Offline(sc.clientID, sc.meta, sc.RemoteAddr()) })
		sc.dlgt.Offline(sc.clientID, sc.meta, sc.RemoteAddr())
	}
	// only handlePkt leads to close other channels
	sc.fini()
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
	sc.log.Debugf("read conn succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(pkt.ConnData.Meta))

	err := sc.fsm.EmitEvent(ET_CONNRECV)
	if err != nil {
		sc.log.Errorf("emit ET_CONNRECV err: %s, clientID: %d, packetID: %d, state: %s",
			err, sc.clientID, pkt.ID(), sc.fsm.State())
		sc.shub.Error(sc.getSyncID(), err)
		return iodefine.IOErr
	}

	sc.meta = pkt.ConnData.Meta
	if pkt.ClientID == 0 {
		if sc.dlgt != nil {
			sc.clientID, err = sc.dlgt.GetClientIDByMeta(sc.meta)
		} else {
			sc.clientID, err = sc.clientIDs.GetIDByMeta(sc.meta)
		}
		if err != nil {
			sc.log.Errorf("get ID err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
				err, sc.clientID, pkt.ID(), string(sc.meta))
			retPkt := sc.pf.NewConnAckPacket(pkt.PacketID, sc.clientID, err)
			sc.writeInCh <- retPkt
			return iodefine.IOSuccess
		}
	}

	if sc.dlgt != nil {
		err = sc.dlgt.Online(sc.clientID, sc.meta, sc.RemoteAddr())
		if err != nil {
			sc.log.Errorf("online err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
				err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))

			retPkt := sc.pf.NewConnAckPacket(pkt.PacketID, sc.clientID, err)
			sc.writeInCh <- retPkt
			return iodefine.IOSuccess
		}
	}
	// the first packet received.
	sc.shub.Ack(sc.getSyncID(), nil)
	retPkt := sc.pf.NewConnAckPacket(pkt.PacketID, sc.clientID, nil)
	sc.writeInCh <- retPkt

	// set the heartbeat
	sc.heartbeat = pkt.Heartbeat
	sc.hbTick = sc.tmr.Add(time.Duration(sc.heartbeat)*2*time.Second, timer.WithHandler(sc.waitHBTimeout))
	return iodefine.IOSuccess
}

func (sc *ServerConn) handleInDisConnPacket(pkt *packet.DisConnPacket) iodefine.IORet {
	sc.log.Debugf("recv dis conn succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
	err := sc.fsm.EmitEvent(ET_CLOSERECV)
	if err != nil {
		sc.log.Errorf("emit ET_CLOSERECV err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta), sc.fsm.State())
		return iodefine.IOErr
	}
	retPkt := sc.pf.NewDisConnAckPacket(pkt.PacketID, nil)
	sc.writeInCh <- retPkt
	sc.Close()
	return iodefine.IOSuccess
}

func (sc *ServerConn) handleInDisConnAckPacket(pkt *packet.DisConnAckPacket) iodefine.IORet {
	sc.log.Debugf("read dis conn ack packet, clientID: %d, packetID: %d, remote: %s, meta: %s",
		sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
	err := sc.fsm.EmitEvent(ET_CLOSEACK)
	if err != nil {
		sc.log.Errorf("emit in ET_CLOSEACK err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta), sc.fsm.State())
		return iodefine.IOErr
	}
	if sc.fsm.State() == CLOSE_HALF {
		return iodefine.IOSuccess
	}
	return iodefine.IOClosed
}

func (sc *ServerConn) handleInHeartbeatPacket(pkt *packet.HeartbeatPacket) iodefine.IORet {
	ok := sc.fsm.InStates(CONNED)
	if !ok {
		sc.log.Errorf("heartbeat at non-CONNED state, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta), sc.fsm.State())
		return iodefine.IODiscard
	}
	// reset hearbeat
	sc.hbTick.Cancel()
	sc.hbTick = sc.tmr.Add(time.Duration(sc.heartbeat)*2*time.Second, timer.WithHandler(sc.waitHBTimeout))

	retPkt := sc.pf.NewHeartbeatAckPacket(pkt.PacketID)
	sc.writeInCh <- retPkt
	if sc.dlgt != nil {
		sc.dlgt.Heartbeat(sc.clientID, sc.meta, sc.netconn.RemoteAddr())
	}
	return iodefine.IOSuccess
}

func (sc *ServerConn) handleInDataPacket(pkt packet.Packet) iodefine.IORet {
	ok := sc.fsm.InStates(CONNED)
	if !ok {
		sc.log.Debugf("data at non CONNED, clientID: %d, packetID: %d, remote: %s, meta: %s",
			sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
		return iodefine.IODiscard
	}
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
		sc.writeOutCh <- pkt
		return iodefine.IOSuccess
	}
	err := sc.fsm.EmitEvent(ET_CONNACK)
	if err != nil {
		sc.log.Errorf("emit ET_CONNACK err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta), sc.fsm.State())
		return iodefine.IOErr
	}
	sc.writeOutCh <- pkt
	sc.log.Debugf("send conn ack succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
	return iodefine.IOSuccess
}

func (sc *ServerConn) handleOutDisConnPacket(pkt *packet.DisConnPacket) iodefine.IORet {
	err := sc.fsm.EmitEvent(ET_CLOSESENT)
	if err != nil {
		sc.log.Errorf("emit out ET_CLOSESENT err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta), sc.fsm.State())
		return iodefine.IOErr
	}
	sc.writeOutCh <- pkt
	sc.log.Debugf("send dis conn succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
	return iodefine.IOSuccess
}

func (sc *ServerConn) handleOutDisConnAckPacket(pkt *packet.DisConnAckPacket) iodefine.IORet {
	err := sc.fsm.EmitEvent(ET_CLOSEACK)
	if err != nil {
		sc.log.Errorf("emit out ET_CLOSEACK err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta), sc.fsm.State())
		return iodefine.IOErr
	}
	// make sure this packet is flushed before writeOutCh closed
	err = sc.dowritePkt(pkt, false)
	if err != nil {
		return iodefine.IOErr
	}
	sc.log.Debugf("send dis conn ack succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
	if sc.fsm.State() == CLOSE_HALF {
		return iodefine.IOSuccess
	}
	return iodefine.IOClosed
}

func (sc *ServerConn) handleOutHeartbeatAckPacket(pkt *packet.HeartbeatAckPacket) iodefine.IORet {
	sc.writeOutCh <- pkt
	sc.log.Debugf("send heartbeat succeed, clientID: %d, PacketID: %d, packetType: %s",
		sc.clientID, pkt.ID(), pkt.Type().String())
	return iodefine.IOSuccess
}

func (sc *ServerConn) handleOutDataPacket(pkt packet.Packet) iodefine.IORet {
	/*
		ok := sc.fsm.InStates(CONNED)
		if !ok {
			sc.log.Debugf("data at non CONNED, clientID: %d, packetID: %d, remote: %s, meta: %s",
				sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
			return iodefine.IODiscard
		}
	*/
	sc.writeOutCh <- pkt
	sc.log.Tracef("send data succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
	return iodefine.IOSuccess
}

// TODO 优雅关闭
func (sc *ServerConn) Close() {
	sc.closeOnce.Do(func() {
		sc.connMtx.RLock()
		defer sc.connMtx.RUnlock()
		if !sc.connOK {
			return
		}

		sc.log.Debugf("client is closing, clientID: %d, remote: %s, meta: %s",
			sc.clientID, sc.netconn.RemoteAddr(), string(sc.meta))

		pkt := sc.pf.NewDisConnPacket()
		sc.writeInCh <- pkt
	})
}

func (sc *ServerConn) close() {}

func (sc *ServerConn) closeWrapper(_ *yafsm.Event) {
	sc.Close()
}

// 回收资源
func (sc *ServerConn) fini() {
	// collect shub
	sc.shub.Close()
	sc.shub = nil
	// collect net.Conn
	sc.netconn.Close()
	sc.connMtx.Lock()
	sc.connOK = false
	close(sc.writeInCh)
	sc.connMtx.Unlock()
	for pkt := range sc.writeInCh {
		if sc.failedCh != nil && !packet.ConnLayer(pkt) {
			sc.failedCh <- pkt
		}
	}
	// the outside should care about channel status
	close(sc.readOutCh)

	close(sc.writeOutCh)
	for pkt := range sc.writeOutCh {
		if sc.failedCh != nil && !packet.ConnLayer(pkt) {
			sc.failedCh <- pkt
		}
	}

	// collect timer
	if sc.hbTick != nil {
		sc.hbTick.Cancel()
		sc.hbTick = nil
	}
	if !sc.tmrOutside {
		sc.tmr.Close()
	}
	sc.tmr = nil
	// collect fsm
	sc.fsm.EmitEvent(ET_FINI)
	sc.fsm.Close()
	sc.fsm = nil
	// collect id
	sc.clientIDs.Close()
	sc.clientIDs = nil
	// collect channels
	sc.readInCh, sc.writeInCh, sc.writeOutCh = nil, nil, nil

	sc.log.Debugf("client finished, clientID: %d, remote: %s, meta: %s",
		sc.clientID, sc.netconn.RemoteAddr(), string(sc.meta))
}

func (sc *ServerConn) waitHBTimeout(event *timer.Event) {
	sc.log.Errorf("wait HEARTBEAT timeout, clientID: %d, remote: %s, meta: %s",
		sc.clientID, sc.netconn.RemoteAddr(), string(sc.meta))
	sc.netconn.Close()
}
