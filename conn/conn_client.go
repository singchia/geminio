package conn

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
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

type Dialer func() (net.Conn, error)

type ClientConn struct {
	*baseConn
	dlgt ClientConnDelegate

	finiOnce    *sync.Once
	closeOnce   *sync.Once
	offlineOnce *sync.Once
}

type ClientConnOption func(*ClientConn) error

func OptionClientConnPacketFactory(pf packet.PacketFactory) ClientConnOption {
	return func(cc *ClientConn) error {
		cc.pf = pf
		return nil
	}
}

func OptionClientConnTimer(tmr timer.Timer) ClientConnOption {
	return func(cc *ClientConn) error {
		cc.tmr = tmr
		cc.tmrOutside = true
		return nil
	}
}

func OptionClientConnDelegate(dlgt ClientConnDelegate) ClientConnOption {
	return func(cc *ClientConn) error {
		cc.dlgt = dlgt
		return nil
	}
}

func OptionClientConnLogger(log log.Logger) ClientConnOption {
	return func(cc *ClientConn) error {
		cc.log = log
		return nil
	}
}

func OptionClientConnMeta(meta []byte) ClientConnOption {
	return func(cc *ClientConn) error {
		cc.meta = meta
		return nil
	}
}

func OptionClientConnClientID(clientID uint64) ClientConnOption {
	return func(cc *ClientConn) error {
		cc.clientID = clientID
		return nil
	}
}

func OptionClientConnBufferSize(read, write int) ClientConnOption {
	return func(cc *ClientConn) error {
		if read > 0 {
			cc.readOutSize = read
		}
		if write > 0 {
			cc.writeOutSize = write
		}
		return nil
	}
}

func NewClientConn(netconn net.Conn, opts ...ClientConnOption) (*ClientConn, error) {
	return newClientConn(netconn, opts...)
}

func NewClientConnWithDialer(dialer Dialer, opts ...ClientConnOption) (*ClientConn, error) {
	netconn, err := dialer()
	if err != nil {
		return nil, err
	}
	return newClientConn(netconn, opts...)
}

func newClientConn(netconn net.Conn, opts ...ClientConnOption) (*ClientConn, error) {
	err := error(nil)
	cc := &ClientConn{
		baseConn: &baseConn{
			connOpts: connOpts{
				clientID:  packet.ClientIDNull,
				heartbeat: packet.Heartbeat20,
				meta:      []byte{},
			},
			netconn:      netconn,
			fsm:          yafsm.NewFSM(),
			side:         geminio.InitiatorSide,
			connOK:       true,
			readInSize:   256,
			writeOutSize: 256,
			readOutSize:  256,
		},
		finiOnce:  new(sync.Once),
		closeOnce: new(sync.Once),
	}
	cc.cn = cc
	// options
	for _, opt := range opts {
		err = opt(cc)
		if err != nil {
			return nil, err
		}
	}
	// io size
	cc.readInCh = make(chan packet.Packet, cc.readInSize)
	cc.writeOutCh = make(chan packet.Packet, cc.writeOutSize)
	cc.readOutCh = make(chan packet.Packet, cc.readOutSize)
	// timer
	if !cc.tmrOutside {
		cc.tmr = timer.NewTimer()
	}
	cc.shub = synchub.NewSyncHub(synchub.OptionTimer(cc.tmr))
	// packet factory
	if cc.pf == nil {
		cc.pf = packet.NewPacketFactory(id.NewIDCounter(id.Odd))
	}
	// log
	if cc.log == nil {
		cc.log = log.DefaultLog
	}
	// states
	cc.initFSM()
	// timer
	cc.hbTick = cc.tmr.Add(time.Duration(cc.heartbeat)*time.Second,
		timer.WithHandler(cc.sendHeartbeat), timer.WithCyclically())
	// start
	go cc.readPkt()
	go cc.writePkt()
	go cc.handlePkt()
	// Start channel monitoring for debugging memory issues
	cc.startChannelMonitor(30 * time.Second)
	err = cc.connect()
	if err != nil {
		goto ERR
	}
	return cc, nil
ERR:
	// cancel heartbeat tick before closing connection
	if cc.hbTick != nil {
		cc.hbTick.Cancel()
		cc.hbTick = nil
	}
	// let fini finish the end
	cc.netconn.Close()
	return nil, err
}

func (cc *ClientConn) initFSM() {
	init := cc.fsm.AddState(INIT)
	connsent := cc.fsm.AddState(CONN_SENT)
	conned := cc.fsm.AddState(CONNED)
	closesent := cc.fsm.AddState(CLOSE_SENT)
	closerecv := cc.fsm.AddState(CLOSE_RECV)
	closehalf := cc.fsm.AddState(CLOSE_HALF)
	closed := cc.fsm.AddState(CLOSED)
	fini := cc.fsm.AddState(FINI)
	cc.fsm.SetState(INIT)

	// events
	cc.fsm.AddEvent(ET_CONNSENT, init, connsent)
	cc.fsm.AddEvent(ET_CONNACK, connsent, conned)
	cc.fsm.AddEvent(ET_CLOSESENT, connsent, closesent) // illegal conn
	cc.fsm.AddEvent(ET_CLOSESENT, conned, closesent)
	cc.fsm.AddEvent(ET_CLOSESENT, closerecv, closesent) // close and been closed at same time
	cc.fsm.AddEvent(ET_CLOSESENT, closehalf, closehalf) // been closed and we acked, then start to close
	cc.fsm.AddEvent(ET_CLOSERECV, connsent, closerecv)  // illegal conn
	cc.fsm.AddEvent(ET_CLOSERECV, conned, closerecv)
	cc.fsm.AddEvent(ET_CLOSERECV, closesent, closerecv) // close and been closed at same time
	cc.fsm.AddEvent(ET_CLOSERECV, closehalf, closehalf)
	cc.fsm.AddEvent(ET_CLOSEACK, closesent, closehalf)
	cc.fsm.AddEvent(ET_CLOSEACK, closerecv, closehalf)
	cc.fsm.AddEvent(ET_CLOSEACK, closehalf, closed) // close and been closed at same time
	cc.fsm.AddEvent(ET_FINI, init, fini)
	cc.fsm.AddEvent(ET_FINI, connsent, fini)
	cc.fsm.AddEvent(ET_FINI, conned, fini)
	cc.fsm.AddEvent(ET_FINI, closesent, fini)
	cc.fsm.AddEvent(ET_FINI, closerecv, fini)
	cc.fsm.AddEvent(ET_FINI, closehalf, fini)
	cc.fsm.AddEvent(ET_FINI, closed, fini)
}

func (cc *ClientConn) connect() error {
	pkt := cc.pf.NewConnPacket(cc.clientID, true, cc.heartbeat, cc.meta)
	cc.writeOutCh <- pkt
	sync := cc.shub.New(pkt.PacketID, synchub.WithTimeout(10*time.Second))
	event := <-sync.C()

	if event.Error != nil {
		cc.log.Errorf("connect err: %s, clientID: %d, remote: %s",
			event.Error, cc.clientID, cc.netconn.RemoteAddr())
		return event.Error
	}
	cc.log.Debugf("connect succeed, clientID: %d, remote: %s",
		cc.clientID, cc.netconn.RemoteAddr())
	return nil
}

func (cc *ClientConn) writePkt() {
	writeOutCh := cc.writeOutCh
	lastLogTime := time.Now()
	packetCount := 0

	for {
		select {
		case pkt, ok := <-writeOutCh:
			if !ok {
				cc.log.Debugf("conn write done, clientID: %d", cc.clientID)
				return
			}
			packetCount++
			// Log periodically to ensure writePkt() is still running
			now := time.Now()
			if now.Sub(lastLogTime) > 10*time.Second {
				cc.log.Infof("writePkt() is running, clientID: %d, packets processed: %d, writeOutCh remaining: %d/%d",
					cc.clientID, packetCount, len(writeOutCh), cc.writeOutSize)
				lastLogTime = now
			}
			cc.log.Tracef("conn write down, clientID: %d, packetID: %d, packetType: %s, writeOutCh remaining: %d/%d",
				cc.clientID, pkt.ID(), pkt.Type().String(), len(writeOutCh), cc.writeOutSize)
			writeStart := time.Now()

			ret := cc.handleOut(pkt)
			if ret == iodefine.IOErr {
				cc.log.Errorf("handle out packet err, clientID: %d", cc.clientID)
				goto FINI
			}
			if ret == iodefine.IOClosed {
				cc.log.Debugf("handle out packet done, clientID: %d", cc.clientID)
				goto FINI
			}
			if ret == iodefine.IODiscard {
				// Log when packet is discarded to track if writeOutCh is full
				cc.log.Debugf("handle out packet discarded, clientID: %d, packetID: %d, packetType: %s",
					cc.clientID, pkt.ID(), pkt.Type().String())
			}

			writeDuration := time.Since(writeStart)
			if writeDuration > 5*time.Second {
				cc.log.Warnf("data packet write took too long, clientID: %d, packetID: %d, writeDuration: %v (network may be blocking)",
					cc.clientID, pkt.ID(), writeDuration)
			}
		}
	}
FINI:
	cc.log.Debugf("writePkt done, clientID: %d", cc.clientID)
	cc.finiOnce.Do(cc.fini)
	cc.collectSharedResources()
}

func (cc *ClientConn) handlePkt() {
	readInCh := cc.readInCh
	for {
		select {
		case pkt, ok := <-readInCh:
			if !ok {
				goto FINI
			}
			ret := cc.handleIn(pkt)
			if ret == iodefine.IOErr {
				cc.log.Errorf("handle in packet err, clientID: %d", cc.clientID)
				goto FINI
			}
			if ret == iodefine.IOClosed {
				cc.log.Debugf("handle in packet done, clientID: %d", cc.clientID)
				goto FINI
			}
		}
	}
FINI:
	cc.log.Debugf("handle pkt done, clientID: %d", cc.clientID)
	cc.finiOnce.Do(cc.fini)
	cc.collectSharedResources()
}

func (cc *ClientConn) handleIn(pkt packet.Packet) iodefine.IORet {
	switch realPkt := pkt.(type) {
	case *packet.ConnAckPacket:
		return cc.handleInConnAckPacket(realPkt)
	case *packet.DisConnPacket:
		return cc.handleInDisConnPacket(realPkt)
	case *packet.DisConnAckPacket:
		return cc.handleInDisConnAckPacket(realPkt)
	case *packet.HeartbeatAckPacket:
		return cc.handleInHeartbeatAckPacket(realPkt)
	default:
		return cc.handleInDataPacket(pkt)
	}
}

func (cc *ClientConn) handleOut(pkt packet.Packet) iodefine.IORet {
	switch realPkt := pkt.(type) {
	case *packet.ConnPacket:
		return cc.handleOutConnPacket(realPkt)
	case *packet.DisConnPacket:
		return cc.handleOutDisConnPacket(realPkt)
	case *packet.DisConnAckPacket:
		return cc.handleOutDisConnAckPacket(realPkt)
	case *packet.HeartbeatPacket:
		return cc.handleOutHeartbeatPacket(realPkt)
	default:
		return cc.handleOutDataPacket(pkt)
	}
}

// input packet
func (cc *ClientConn) handleInConnAckPacket(pkt *packet.ConnAckPacket) iodefine.IORet {
	cc.log.Debugf("read conn ack succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		pkt.ClientID, pkt.ID(), cc.netconn.RemoteAddr(), string(pkt.ConnData.Meta))

	err := cc.fsm.EmitEvent(ET_CONNACK)
	if err != nil {
		cc.log.Errorf("emit ET_CONNACK err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
			err, cc.clientID, pkt.ID(), cc.netconn.RemoteAddr(), string(cc.meta), cc.fsm.State())
		cc.shub.Error(pkt.PacketID, err)
		return iodefine.IOErr
	}
	cc.clientID = pkt.ClientID

	if pkt.ConnData.Error != "" {
		cc.shub.Error(pkt.PacketID, errors.New(pkt.ConnData.Error))
		// close the conn
		retPkt := cc.pf.NewDisConnPacket()
		cc.writeOutCh <- retPkt
		return iodefine.IOSuccess
	}
	cc.shub.Done(pkt.PacketID)
	cc.onlined = true
	return iodefine.IOSuccess
}

func (cc *ClientConn) handleInHeartbeatAckPacket(pkt *packet.HeartbeatAckPacket) iodefine.IORet {
	cc.log.Debugf("read heartbeat ack succeed, clientID: %d, PacketID: %d, remote: %s, meta: %s",
		cc.clientID, pkt.ID(), cc.netconn.RemoteAddr(), string(cc.meta))

	ok := cc.fsm.InStates(CONNED)
	if !ok {
		cc.log.Errorf("heartbeat at non-CONNED state, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
			cc.clientID, pkt.ID(), cc.netconn.RemoteAddr(), string(cc.meta), cc.fsm.State())
		return iodefine.IOErr
	}
	return iodefine.IOSuccess
}

func (cc *ClientConn) handleInDataPacket(pkt packet.Packet) iodefine.IORet {
	ok := cc.fsm.InStates(CONNED)
	if !ok {
		cc.log.Debugf("data at non CONNED, clientID: %d, packetID: %d, remote: %s, meta: %s",
			cc.clientID, pkt.ID(), cc.netconn.RemoteAddr(), string(cc.meta))
		return iodefine.IODiscard
	}
	// Data packets: block to ensure delivery, no packet loss
	// This ensures all packets are delivered to upper layer, even if it means slower processing
	// The blocking will wait for upper layer to read packets and make room
	cc.readOutCh <- pkt
	return iodefine.IOSuccess
}

// output packet
func (cc *ClientConn) handleOutConnPacket(pkt *packet.ConnPacket) iodefine.IORet {
	err := cc.fsm.EmitEvent(ET_CONNSENT)
	if err != nil {

		cc.log.Errorf("emit ET_CONNSENT err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
			err, cc.clientID, pkt.ID(), cc.netconn.RemoteAddr(), string(cc.meta), cc.fsm.State())

		cc.shub.Error(pkt.ID(), err)
		return iodefine.IOErr
	}
	err = cc.dowritePkt(pkt, true)
	if err != nil {
		cc.log.Errorf("send conn packet err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
			err, cc.clientID, pkt.ID(), cc.netconn.RemoteAddr(), string(cc.meta), cc.fsm.State())
		return iodefine.IOErr
	}
	cc.log.Debugf("send conn succeed, clientID: %d, PacketID: %d, packetType: %s",
		cc.clientID, pkt.ID(), pkt.Type().String())
	return iodefine.IOSuccess
}

func (cc *ClientConn) handleOutHeartbeatPacket(pkt *packet.HeartbeatPacket) iodefine.IORet {
	err := cc.dowritePkt(pkt, true)
	if err != nil {
		cc.log.Errorf("send heartbeat packet err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
			err, cc.clientID, pkt.ID(), cc.netconn.RemoteAddr(), string(cc.meta), cc.fsm.State())
		return iodefine.IOErr
	}
	return iodefine.IOSuccess
}

func (cc *ClientConn) sendHeartbeat(event *timer.Event) {
	cc.connMtx.RLock()
	if !cc.connOK {
		cc.connMtx.RUnlock()
		cc.log.Debugf("heartbeat skipped, connOK is false, clientID: %d", cc.clientID)
		return
	}
	// Create packet before releasing lock to avoid race condition
	pkt := cc.pf.NewHeartbeatPacket()
	cc.connMtx.RUnlock()

	cc.writeOutCh <- pkt
	cc.log.Tracef("heartbeat packet sent to writeOutCh, clientID: %d, packetID: %d", cc.clientID, pkt.ID())
}

func (cc *ClientConn) Close() {
	cc.closeOnce.Do(func() {
		cc.connMtx.RLock()
		if !cc.connOK {
			cc.connMtx.RUnlock()
			return
		}
		if cc.hbTick != nil {
			cc.hbTick.Cancel()
			cc.hbTick = nil
		}

		cc.log.Debugf("client is closing, clientID: %d, remote: %s, meta: %s",
			cc.clientID, cc.netconn.RemoteAddr(), string(cc.meta))

		pkt := cc.pf.NewDisConnPacket()
		// Directly call handleOutDisConnPacket to avoid deadlock and ensure packet ordering:
		// - DisConnPacket is a connection-layer packet, can be handled directly
		// - handleOutDisConnPacket sends to writeOutCh (not writeInCh), avoiding deadlock
		// - writeOutCh is read by writePkt() goroutine (not handlePkt()), so no deadlock risk
		// - handlePkt() is reading from writeInCh in the same goroutine,
		//   so sending to writeInCh would cause deadlock if channel is full
		// - By directly calling handleOutDisConnPacket, we bypass writeInCh entirely
		// - Sending to writeOutCh ensures DisConnPacket is queued after all existing packets,
		//   maintaining proper packet ordering (FIFO) so queued data packets are sent first
		// - Even if called from writePkt() goroutine, it's safe because writePkt() continues
		//   reading from writeOutCh, so blocking on writeOutCh won't cause deadlock
		cc.connMtx.RUnlock()
		ret := cc.handleOutDisConnPacket(pkt)
		if ret == iodefine.IOErr {
			cc.log.Errorf("handle out DisConnPacket err, clientID: %d, packetID: %d",
				cc.clientID, pkt.ID())
		}
	})
}

func (cc *ClientConn) fini() {
	if cc.dlgt != nil && cc.clientID != 0 {
		// not that delegate is different from server's delegate
		cc.dlgt.ConnOffline(cc)
	}
	remote := "unknown"
	if cc.netconn != nil {
		remote = cc.netconn.RemoteAddr().String()
	}
	cc.log.Debugf("client finishing, clientID: %d, remote: %s, meta: %s",
		cc.clientID, remote, string(cc.meta))
	if cc.hbTick != nil {
		cc.hbTick.Cancel()
		cc.hbTick = nil
	}
	// collect shub
	cc.shub.Close()
	cc.shub = nil
	// collect net.Conn
	cc.netconn.Close()
	cc.connMtx.Lock()
	cc.connOK = false
	cc.connMtx.Unlock()

	// the outside should care about channel status
	close(cc.readOutCh)

	close(cc.writeOutCh)
	for pkt := range cc.writeOutCh {
		if cc.failedCh != nil && !packet.ConnLayer(pkt) {
			cc.failedCh <- pkt
		}
	}
	cc.log.Debugf("client finished, clientID: %d, remote: %s, meta: %s",
		cc.clientID, remote, string(cc.meta))
}

// collectSharedResources is called when a goroutine (writePkt or handlePkt) finishes
// When called the second time (both goroutines finished), it collects shared resources
func (cc *ClientConn) collectSharedResources() {
	count := atomic.AddInt64(&cc.allDoneCount, 1)
	if count != 2 {
		// First call, just return
		return
	}
	// Second call means both writePkt and handlePkt have finished, safe to collect shared resources
	// collect timer
	if !cc.tmrOutside {
		cc.tmr.Close()
	}
	cc.tmr = nil
	// collect fsm (used by both writePkt and handlePkt)
	cc.fsm.EmitEvent(ET_FINI)
	cc.fsm.Close()

	cc.readInCh, cc.writeOutCh = nil, nil
}
