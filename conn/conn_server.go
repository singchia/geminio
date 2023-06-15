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

type dirPkt struct {
	iotype iodefine.IOType
	pkt    packet.Packet
}

type ServerConn struct {
	*baseConn

	readInCh, writeOutCh chan packet.Packet // io neighbor channel
	readOutCh            chan packet.Packet // to outside
	writeInCh            chan packet.Packet
	failedCh             chan packet.Packet

	dlgt ServerConnDelegate

	closeOnce   *sync.Once
	finiOnce    *sync.Once
	offlineOnce *sync.Once

	clientIDs id.IDFactory // global IDs
}

type ServerConnOption func(*ServerConn)

func OptionServerConnPacketFactory(pf *packet.PacketFactory) ServerConnOption {
	return func(rc *ServerConn) {
		rc.pf = pf
	}
}

func OptionServerConnTimer(tmr timer.Timer) ServerConnOption {
	return func(rc *ServerConn) {
		rc.tmr = tmr
		rc.tmrOutside = true
	}
}

func OptionServerConnDelegate(dlgt ServerConnDelegate) ServerConnOption {
	return func(rc *ServerConn) {
		rc.dlgt = dlgt
	}
}

func OptionServerConnLogger(log log.Logger) ServerConnOption {
	return func(rc *ServerConn) {
		rc.log = log
	}
}

func OptionServerConnFailedPacket(ch chan packet.Packet) ServerConnOption {
	return func(rc *ServerConn) {

	}
}

func NewServerConn(netconn net.Conn, opts ...ServerConnOption) (*ServerConn, error) {
	err := error(nil)
	rc := &ServerConn{
		baseConn: &baseConn{
			connOpts: connOpts{
				waitTimeout: 10,
			},
			fsm:     yafsm.NewFSM(),
			netconn: netconn,
			side:    ServerSide,
			connOK:  true,
		},
		readInCh:   make(chan packet.Packet, 1024),
		writeOutCh: make(chan packet.Packet, 1024),
		readOutCh:  make(chan packet.Packet, 1024),
		writeInCh:  make(chan packet.Packet, 1024),

		closeOnce:   new(sync.Once),
		finiOnce:    new(sync.Once),
		offlineOnce: new(sync.Once),
		clientIDs:   id.NewIDCounter(id.Unique),
	}
	rc.cn = rc
	// options
	for _, opt := range opts {
		opt(rc)
	}
	//rc.writeFromUpCh = make(chan packet.Packet)
	//rc.readToUpCh = make(chan packet.Packet)
	// timer
	if !rc.tmrOutside {
		rc.tmr = timer.NewTimer()
	}
	rc.shub = synchub.NewSyncHub(synchub.OptionTimer(rc.tmr))

	if rc.pf == nil {
		rc.pf = packet.NewPacketFactory(id.NewIDCounter(id.Even))
	}
	if rc.log == nil {
		rc.log = log.DefaultLog
	}
	// states
	rc.initFSM()
	go rc.readPkt()
	go rc.writePkt()
	go rc.handlePkt()
	err = rc.wait()
	if err != nil {
		goto ERR
	}
	return rc, nil
ERR:
	rc.fini()
	return nil, err
}

func (rc *ServerConn) wait() error {
	sync := rc.shub.New(rc.getSyncID(), synchub.WithTimeout(10*time.Second))
	event := <-sync.C()
	if event.Error != nil {
		rc.log.Errorf("wait conn timeout, clientID: %d, remote: %s, meta: %s",
			rc.clientID, rc.netconn.RemoteAddr(), string(rc.meta))
		return event.Error
	}
	return nil
}

func (rc *ServerConn) getSyncID() string {
	return rc.netconn.RemoteAddr().String() + rc.netconn.LocalAddr().String()
}

func (rc *ServerConn) initFSM() {
	init := rc.fsm.AddState(INIT)
	connrecv := rc.fsm.AddState(CONN_RECV)
	conned := rc.fsm.AddState(CONNED)
	closesent := rc.fsm.AddState(CLOSE_SENT)
	closerecv := rc.fsm.AddState(CLOSE_RECV)
	closehalf := rc.fsm.AddState(CLOSE_HALF)
	closed := rc.fsm.AddState(FINI)
	fini := rc.fsm.AddState(FINI)
	rc.fsm.SetState(INIT)

	// events
	rc.fsm.AddEvent(ET_CONNRECV, init, connrecv)
	rc.fsm.AddEvent(ET_CONNACK, connrecv, conned)

	rc.fsm.AddEvent(ET_ERROR, init, closed)
	rc.fsm.AddEvent(ET_ERROR, connrecv, closed, rc.closeWrapper)
	rc.fsm.AddEvent(ET_ERROR, conned, closed, rc.closeWrapper)
	rc.fsm.AddEvent(ET_ERROR, closesent, closed, rc.closeWrapper)
	rc.fsm.AddEvent(ET_ERROR, closerecv, closed, rc.closeWrapper)

	rc.fsm.AddEvent(ET_EOF, connrecv, closed)
	rc.fsm.AddEvent(ET_EOF, conned, closed)

	rc.fsm.AddEvent(ET_CLOSESENT, conned, closesent)
	rc.fsm.AddEvent(ET_CLOSESENT, closerecv, closesent) // close and been closed at same time

	rc.fsm.AddEvent(ET_CLOSERECV, conned, closerecv)
	rc.fsm.AddEvent(ET_CLOSERECV, closesent, closerecv) // close and been closed at same time

	rc.fsm.AddEvent(ET_CLOSEACK, closesent, closehalf)
	rc.fsm.AddEvent(ET_CLOSEACK, closerecv, closehalf)
	rc.fsm.AddEvent(ET_CLOSEACK, closehalf, closed)
	// fini
	rc.fsm.AddEvent(ET_FINI, init, fini)
	rc.fsm.AddEvent(ET_FINI, connrecv, fini)
	rc.fsm.AddEvent(ET_FINI, conned, fini)
	rc.fsm.AddEvent(ET_FINI, closesent, fini)
	rc.fsm.AddEvent(ET_FINI, closerecv, fini)
	rc.fsm.AddEvent(ET_FINI, closehalf, fini)
	rc.fsm.AddEvent(ET_FINI, closed, fini)
}

func (rc *ServerConn) Read() (packet.Packet, error) {
	pkt, ok := <-rc.readOutCh
	if !ok {
		return nil, io.EOF
	}
	return pkt, nil
}

func (rc *ServerConn) Write(pkt packet.Packet) error {
	rc.connMtx.RLock()
	defer rc.connMtx.RUnlock()
	if !rc.connOK {
		return io.EOF
	}
	rc.writeInCh <- pkt
	return nil
}

func (rc *ServerConn) writePkt() {
	err := error(nil)

	for {
		select {
		case pkt, ok := <-rc.writeOutCh:
			if !ok {
				return
			}
			rc.log.Tracef("to write down, clientID: %d, packetID: %d, packetType: %s",
				rc.clientID, pkt.ID(), pkt.Type().String())
			err = rc.dowritePkt(pkt, true)
			if err != nil {
				return
			}
		}
	}
}

func (rc *ServerConn) dowritePkt(pkt packet.Packet, record bool) error {
	err := packet.EncodeToWriter(pkt, rc.netconn)
	if err != nil {
		if err == io.EOF {
			// eof means no need for graceful close
			rc.fsm.EmitEvent(ET_EOF)
			rc.log.Infof("conn write down EOF, clientID: %d, packetID: %d",
				rc.clientID, pkt.ID())
		} else {
			rc.fsm.EmitEvent(ET_ERROR)
			rc.log.Errorf("conn write down err: %s, clientID: %d, packetID: %d",
				err, rc.clientID, pkt.ID())
		}
		if record && rc.failedCh != nil {
			rc.failedCh <- pkt
		}
	}
	return err
}

func (rc *ServerConn) readPkt() {
	for {
		pkt, err := packet.DecodeFromReader(rc.netconn)
		if err != nil {
			if err == io.EOF {
				rc.fsm.EmitEvent(ET_EOF)
				rc.log.Debugf("conn read down EOF, clientID: %d, remote: %s, meta: %s",
					rc.clientID, rc.netconn.RemoteAddr(), string(rc.meta))
			} else if iodefine.ErrUseOfClosedNetwork(err) {
				rc.log.Infof("conn read down closed, clientID: %d", rc.clientID)
			} else {
				rc.fsm.EmitEvent(ET_ERROR)
				rc.log.Errorf("conn read down err: %s, clientID: %d",
					err, rc.clientID)
			}
			goto FINI
		}
		rc.log.Tracef("read %s , clientID: %d, packetID: %d, packetType: %s",
			pkt.Type().String(), rc.clientID, pkt.ID(), pkt.Type().String())
		rc.readInCh <- pkt
	}
FINI:
	close(rc.readInCh)
}

func (rc *ServerConn) handlePkt() {
	for {
		select {
		case pkt, ok := <-rc.readInCh:
			if !ok {
				goto FINI
			}
			ret := rc.handleIn(pkt)
			if ret == iodefine.IOErr {
				rc.log.Errorf("handle in packet err, clientID: %d", rc.clientID)
				goto FINI
			}
			if ret == iodefine.IOClosed {
				rc.log.Infof("handle in packet done, clientID: %d", rc.clientID)
				goto FINI
			}
		case pkt := <-rc.writeInCh:
			ret := rc.handleOut(pkt)
			if ret == iodefine.IOErr {
				rc.log.Errorf("handle out packet err, clientID: %d", rc.clientID)
				goto FINI
			}
			if ret == iodefine.IOClosed {
				rc.log.Infof("handle out packet done, clientID: %d", rc.clientID)
				goto FINI
			}
		}
	}
FINI:
	rc.log.Debugf("handle pkt done, clientID: %d", rc.clientID)
	if rc.dlgt != nil && rc.clientID != 0 {
		rc.offlineOnce.Do(func() { rc.dlgt.Offline(rc.clientID, rc.meta, rc.RemoteAddr()) })
	}
	// only handlePkt leads to close other channels
	rc.fini()
}

func (rc *ServerConn) handleIn(pkt packet.Packet) iodefine.IORet {
	switch realPkt := pkt.(type) {
	case *packet.ConnPacket:
		return rc.handleInConnPkt(realPkt)
	case *packet.DisConnPacket:
		return rc.handleInDisConnPacket(realPkt)
	case *packet.DisConnAckPacket:
		return rc.handleInDisConnAckPacket(realPkt)
	case *packet.HeartbeatPacket:
		return rc.handleInHeartbeatPacket(realPkt)
	default:
		return rc.handleInDataPacket(pkt)
	}
}

func (rc *ServerConn) handleOut(pkt packet.Packet) iodefine.IORet {
	switch realPkt := pkt.(type) {
	case *packet.ConnAckPacket:
		return rc.handleOutConnAckPacket(realPkt)
	case *packet.DisConnPacket:
		return rc.handleOutDisConnPacket(realPkt)
	case *packet.DisConnAckPacket:
		return rc.handleOutDisConnAckPacket(realPkt)
	case *packet.HeartbeatAckPacket:
		return rc.handleOutHeartbeatAckPacket(realPkt)
	default:
		return rc.handleOutDataPacket(pkt)
	}
}

// input packet
func (rc *ServerConn) handleInConnPkt(pkt *packet.ConnPacket) iodefine.IORet {
	rc.log.Debugf("read conn succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(pkt.ConnData.Meta))

	err := rc.fsm.EmitEvent(ET_CONNRECV)
	if err != nil {
		rc.log.Errorf("emit ET_CONNRECV err: %s, clientID: %d, packetID: %d, state: %s",
			err, rc.clientID, pkt.ID(), rc.fsm.State())
		rc.shub.Error(rc.getSyncID(), err)
		return iodefine.IOErr
	}

	rc.meta = pkt.ConnData.Meta
	if pkt.ClientID == 0 {
		if rc.dlgt != nil {
			rc.clientID, err = rc.dlgt.GetClientIDByMeta(rc.meta)
		} else {
			rc.clientID, err = rc.clientIDs.GetIDByMeta(rc.meta)
		}
		if err != nil {
			rc.log.Errorf("get ID err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
				err, rc.clientID, pkt.ID(), string(rc.meta))
			retPkt := rc.pf.NewConnAckPacket(pkt.PacketID, rc.clientID, err)
			rc.writeInCh <- retPkt
			return iodefine.IOSuccess
		}
	}

	if rc.dlgt != nil {
		err = rc.dlgt.Online(rc.clientID, rc.meta, rc.RemoteAddr())
		if err != nil {
			rc.log.Errorf("online err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
				err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))

			retPkt := rc.pf.NewConnAckPacket(pkt.PacketID, rc.clientID, err)
			rc.writeInCh <- retPkt
			return iodefine.IOSuccess
		}
	}
	// the first packet received.
	rc.shub.Ack(rc.getSyncID(), nil)
	retPkt := rc.pf.NewConnAckPacket(pkt.PacketID, rc.clientID, nil)
	rc.writeInCh <- retPkt

	// set the heartbeat
	rc.heartbeat = pkt.Heartbeat
	rc.hbTick = rc.tmr.Add(time.Duration(rc.heartbeat)*2*time.Second, timer.WithHandler(rc.waitHBTimeout))
	return iodefine.IOSuccess
}

func (rc *ServerConn) handleInDisConnPacket(pkt *packet.DisConnPacket) iodefine.IORet {
	rc.log.Debugf("recv dis conn succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
	err := rc.fsm.EmitEvent(ET_CLOSERECV)
	if err != nil {
		rc.log.Errorf("emit ET_CLOSERECV err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta), rc.fsm.State())
		return iodefine.IOErr
	}
	retPkt := rc.pf.NewDisConnAckPacket(pkt.PacketID, nil)
	rc.writeInCh <- retPkt
	rc.Close()
	return iodefine.IOSuccess
}

func (rc *ServerConn) handleInDisConnAckPacket(pkt *packet.DisConnAckPacket) iodefine.IORet {
	rc.log.Debugf("read dis conn ack packet, clientID: %d, packetID: %d, remote: %s, meta: %s",
		rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
	err := rc.fsm.EmitEvent(ET_CLOSEACK)
	if err != nil {
		rc.log.Errorf("emit in ET_CLOSEACK err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta), rc.fsm.State())
		return iodefine.IOErr
	}
	if rc.fsm.State() == CLOSE_HALF {
		return iodefine.IOSuccess
	}
	return iodefine.IOClosed
}

func (rc *ServerConn) handleInHeartbeatPacket(pkt *packet.HeartbeatPacket) iodefine.IORet {
	ok := rc.fsm.InStates(CONNED)
	if !ok {
		rc.log.Errorf("heartbeat at non-CONNED state, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta), rc.fsm.State())
		return iodefine.IODiscard
	}
	// reset hearbeat
	rc.hbTick.Cancel()
	rc.hbTick = rc.tmr.Add(time.Duration(rc.heartbeat)*2*time.Second, timer.WithHandler(rc.waitHBTimeout))

	retPkt := rc.pf.NewHeartbeatAckPacket(pkt.PacketID)
	rc.writeInCh <- retPkt
	if rc.dlgt != nil {
		rc.dlgt.Heartbeat(rc.clientID, rc.meta, rc.netconn.RemoteAddr())
	}
	return iodefine.IOSuccess
}

func (rc *ServerConn) handleInDataPacket(pkt packet.Packet) iodefine.IORet {
	ok := rc.fsm.InStates(CONNED)
	if !ok {
		rc.log.Debugf("data at non CONNED, clientID: %d, packetID: %d, remote: %s, meta: %s",
			rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
		return iodefine.IODiscard
	}
	rc.readOutCh <- pkt
	return iodefine.IOSuccess
}

// output packet
func (rc *ServerConn) handleOutConnAckPacket(pkt *packet.ConnAckPacket) iodefine.IORet {
	if pkt.RetCode == packet.RetCodeERR {
		err := rc.fsm.EmitEvent(ET_ERROR)
		if err != nil {
			rc.log.Errorf("emit ET_ERROR err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
				pkt.ConnData.Error, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
			return iodefine.IOErr
		}
		rc.writeOutCh <- pkt
		return iodefine.IOSuccess
	}
	err := rc.fsm.EmitEvent(ET_CONNACK)
	if err != nil {
		rc.log.Errorf("emit ET_CONNACK err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta), rc.fsm.State())
		return iodefine.IOErr
	}
	rc.writeOutCh <- pkt
	rc.log.Debugf("send conn ack succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
	return iodefine.IOSuccess
}

func (rc *ServerConn) handleOutDisConnPacket(pkt *packet.DisConnPacket) iodefine.IORet {
	err := rc.fsm.EmitEvent(ET_CLOSESENT)
	if err != nil {
		rc.log.Errorf("emit out ET_CLOSESENT err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta), rc.fsm.State())
		return iodefine.IOErr
	}
	rc.writeOutCh <- pkt
	rc.log.Debugf("send dis conn succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
	return iodefine.IOSuccess
}

func (rc *ServerConn) handleOutDisConnAckPacket(pkt *packet.DisConnAckPacket) iodefine.IORet {
	err := rc.fsm.EmitEvent(ET_CLOSEACK)
	if err != nil {
		rc.log.Errorf("emit out ET_CLOSEACK err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta), rc.fsm.State())
		return iodefine.IOErr
	}
	// make sure this packet is flushed before writeOutCh closed
	err = rc.dowritePkt(pkt, false)
	if err != nil {
		return iodefine.IOErr
	}
	rc.log.Debugf("send dis conn ack succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
	if rc.fsm.State() == CLOSE_HALF {
		return iodefine.IOSuccess
	}
	return iodefine.IOClosed
}

func (rc *ServerConn) handleOutHeartbeatAckPacket(pkt *packet.HeartbeatAckPacket) iodefine.IORet {
	rc.writeOutCh <- pkt
	return iodefine.IOSuccess
}

func (rc *ServerConn) handleOutDataPacket(pkt packet.Packet) iodefine.IORet {
	ok := rc.fsm.InStates(CONNED)
	if !ok {
		rc.log.Debugf("data at non CONNED, clientID: %d, packetID: %d, remote: %s, meta: %s",
			rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
		return iodefine.IODiscard
	}
	rc.writeOutCh <- pkt
	rc.log.Tracef("send data succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
	return iodefine.IOSuccess
}

// TODO 优雅关闭
func (rc *ServerConn) Close() {
	rc.closeOnce.Do(func() {
		rc.connMtx.RLock()
		if !rc.connOK {
			rc.connMtx.RUnlock()
			return
		}
		rc.connMtx.RUnlock()
		rc.log.Debugf("client is closing, clientID: %d, remote: %s, meta: %s",
			rc.clientID, rc.netconn.RemoteAddr(), string(rc.meta))

		pkt := rc.pf.NewDisConnPacket()
		rc.writeInCh <- pkt
	})
}

func (rc *ServerConn) close() {}

func (rc *ServerConn) closeWrapper(_ *yafsm.Event) {
	rc.Close()
}

// 回收资源
func (rc *ServerConn) fini() {
	rc.finiOnce.Do(func() {
		rc.log.Debugf("client finished, clientID: %d, remote: %s, meta: %s",
			rc.clientID, rc.netconn.RemoteAddr(), string(rc.meta))

		rc.connMtx.Lock()
		rc.connOK = false
		if rc.hbTick != nil {
			rc.hbTick.Cancel()
		}
		rc.netconn.Close()
		rc.shub.Close()

		close(rc.writeOutCh)
		for pkt := range rc.writeOutCh {
			if rc.failedCh != nil && !packet.ConnLayer(pkt) {
				rc.failedCh <- pkt
			}
		}
		close(rc.writeInCh)
		for pkt := range rc.writeInCh {
			if rc.failedCh != nil && !packet.ConnLayer(pkt) {
				rc.failedCh <- pkt
			}
		}
		// the outside should care about channel status
		close(rc.readOutCh)

		rc.connMtx.Unlock()
		if !rc.tmrOutside {
			rc.tmr.Close()
		}

		rc.fsm.EmitEvent(ET_FINI)
		rc.fsm.Close()
	})
}

func (rc *ServerConn) waitHBTimeout(event *timer.Event) {
	rc.log.Errorf("wait HEARTBEAT timeout, clientID: %d, remote: %s, meta: %s",
		rc.clientID, rc.netconn.RemoteAddr(), string(rc.meta))
	rc.fini()
}
