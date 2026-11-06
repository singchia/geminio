package conn

import (
	"context"
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

	closeOnce *sync.Once
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
			cc.writeInSize = write
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
			monitorStop:  make(chan struct{}),
			readInSize:   256,
			writeOutSize: 256,
			readOutSize:  256,
			writeInSize:  256,
		},
		//finiOnce:  new(sync.Once),
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
	cc.writeInCh = make(chan packet.Packet, cc.writeInSize)
	cc.heartbeatCh = make(chan packet.Packet, 10) // priority channel for heartbeat packets (receive)
	cc.ackWritten = make(chan struct{}, 1)
	cc.heartbeatWriteCh = make(chan packet.Packet, 10) // priority channel for heartbeat packets (send)
	cc.ctx, cc.cancel = context.WithCancel(context.Background())
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
	cc.startChannelMonitor(false, 30*time.Second)
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
	pkt.ConnData.StreamReset = packet.StreamResetV1
	pkt.ConnData.FlowControl = packet.FlowControlV1
	sync := cc.shub.New(pkt.PacketID, synchub.WithTimeout(10*time.Second))
	cc.sendToWriteIn(pkt) //nolint:errcheck
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

func (cc *ClientConn) handlePkt() {
	readInCh := cc.readInCh
	writeInCh := cc.writeInCh
	heartbeatCh := cc.heartbeatCh
	var deferredIn packet.Packet
	var deferredOut packet.Packet

	finishing := false

	for {
		// Only the event loop delivers data. A full output disables its
		// input direction, without parking the opposite direction.
		in, out, hb := readInCh, writeInCh, heartbeatCh
		var up, down chan packet.Packet
		if cc.pendingUp != nil {
			in = nil
			up = cc.readOutCh
		}
		if cc.pendingDown != nil {
			out = nil
			down = cc.writeOutCh
		}
		if deferredIn != nil {
			in = nil
			hb = nil
		}
		if deferredOut != nil {
			out = nil
		}
		// Protocol handlers may emit control output. Run them only once
		// older data output has been handed to the writer, preserving order.
		if !finishing && cc.pendingDown == nil {
			if deferredIn != nil {
				pkt := deferredIn
				deferredIn = nil
				ret := cc.handleIn(pkt)
				if ret == iodefine.IOErr {
					goto FINI
				}
				if ret == iodefine.IOClosed {
					finishing = true
				}
				continue
			}
			if deferredOut != nil {
				pkt := deferredOut
				deferredOut = nil
				ret := cc.handleOut(pkt)
				if ret == iodefine.IOErr {
					goto FINI
				}
				if ret == iodefine.IOClosed {
					finishing = true
				}
				continue
			}
		}

		var flushed <-chan struct{}
		if finishing {
			in, out, hb = nil, nil, nil
			if cc.pendingUp == nil && cc.pendingDown == nil {
				if !cc.ackQueued {
					goto FINI
				}
				flushed = cc.ackWritten
			}
		}

		select {
		case <-flushed:
			goto FINI
		case <-cc.ctx.Done():
			goto FINI
		case up <- cc.pendingUp:
			cc.pendingUp = nil
		case down <- cc.pendingDown:
			cc.pendingDown = nil

		case pkt := <-hb:
			// Priority processing for heartbeat packets
			cc.log.Tracef("conn read in heartbeat packet, clientID: %d, packetID: %d, packetType: %s",
				cc.clientID, pkt.ID(), pkt.Type().String())
			if packet.ConnLayer(pkt) && pkt.Type() != packet.TypeHeartbeatAckPacket && cc.pendingDown != nil {
				deferredIn = pkt
				continue
			}
			ret := cc.handleIn(pkt)
			if ret == iodefine.IOErr {
				cc.log.Errorf("handle in heartbeat packet err, clientID: %d", cc.clientID)
				goto FINI
			}
			if ret == iodefine.IOClosed {
				cc.log.Debugf("handle in heartbeat packet done, clientID: %d", cc.clientID)
				finishing = true
			}
		case pkt, ok := <-in:
			if !ok {
				goto FINI
			}
			if packet.ConnLayer(pkt) && pkt.Type() != packet.TypeHeartbeatAckPacket && cc.pendingDown != nil {
				deferredIn = pkt
				continue
			}
			ret := cc.handleIn(pkt)
			if ret == iodefine.IOErr {
				cc.log.Errorf("handle in packet err, clientID: %d", cc.clientID)
				goto FINI
			}
			if ret == iodefine.IOClosed {
				cc.log.Debugf("handle in packet done, clientID: %d", cc.clientID)
				finishing = true
			}
		case pkt := <-out:
			// Log when processing packet from writeInCh to track if heartbeat packets are being processed
			cc.log.Tracef("handlePkt processing writeInCh packet, clientID: %d, packetID: %d, packetType: %s",
				cc.clientID, pkt.ID(), pkt.Type().String())
			if packet.ConnLayer(pkt) {
				deferredOut = pkt
				continue
			}
			ret := cc.handleOut(pkt)
			if ret == iodefine.IOErr {
				cc.log.Errorf("handle out packet err, clientID: %d", cc.clientID)
				goto FINI
			}
			if ret == iodefine.IOClosed {
				cc.log.Debugf("handle out packet done, clientID: %d", cc.clientID)
				finishing = true
			}
			if ret == iodefine.IODiscard {
				// Log when packet is discarded to track if writeOutCh is full
				cc.log.Debugf("handle out packet discarded, clientID: %d, packetID: %d, packetType: %s",
					cc.clientID, pkt.ID(), pkt.Type().String())
			}
		}
	}
FINI:
	cc.log.Debugf("handle pkt done, clientID: %d", cc.clientID)
	if cc.dlgt != nil && cc.clientID != 0 {
		// not that delegate is different from server's delegate
		cc.dlgt.ConnOffline(cc)
	}
	// only handlePkt leads to close other channels
	cc.fini()
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
	atomic.StoreUint64(&cc.clientID, pkt.ClientID)

	if pkt.ConnData.Error != "" {
		cc.shub.Error(pkt.PacketID, errors.New(pkt.ConnData.Error))
		// close the conn
		retPkt := cc.pf.NewDisConnPacket()
		// Send from a goroutine because handlePkt() is the only consumer of writeInCh;
		// blocking here would deadlock. sendToWriteIn selects on ctx.Done() for safety.
		go func() { cc.sendToWriteIn(retPkt) }() //nolint:errcheck
		return iodefine.IOSuccess
	}
	if pkt.ConnData.StreamReset == packet.StreamResetV1 && pkt.ConnData.FlowControl == packet.FlowControlV1 {
		atomic.StoreUint32(&cc.streamReset, packet.StreamResetV1)
	}
	if pkt.ConnData.FlowControl == packet.FlowControlV1 {
		atomic.StoreUint32(&cc.flowControl, packet.FlowControlV1)
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
	// Retain this packet without blocking the opposite direction.
	cc.pendingUp = pkt
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
	// The handshake uses the same ordered, non-blocking output path.
	cc.pendingDown = pkt
	cc.log.Debugf("send conn succeed, clientID: %d, PacketID: %d, packetType: %s",
		cc.clientID, pkt.ID(), pkt.Type().String())
	return iodefine.IOSuccess
}

func (cc *ClientConn) handleOutHeartbeatPacket(pkt *packet.HeartbeatPacket) iodefine.IORet {
	if !cc.queueHeartbeat(pkt) {
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

	cc.queueHeartbeat(pkt)

}

func (cc *ClientConn) Close() {
	cc.closeOnce.Do(func() {
		cc.connMtx.RLock()
		defer cc.connMtx.RUnlock()
		if !cc.connOK {
			return
		}
		if cc.hbTick != nil {
			cc.hbTick.Cancel()
			cc.hbTick = nil
		}

		cc.log.Debugf("client is closing, clientID: %d, remote: %s, meta: %s",
			cc.clientID, cc.netconn.RemoteAddr(), string(cc.meta))

		pkt := cc.pf.NewDisConnPacket()
		// Use non-blocking send to avoid deadlock:
		// Close() may be called from handlePkt() goroutine (e.g., via handleInDisConnPacket -> Close()),
		// Send from a goroutine because Close() may be called from handlePkt() goroutine;
		// blocking here would deadlock. sendToWriteIn selects on ctx.Done() for safety.
		go func() { cc.sendToWriteIn(pkt) }() //nolint:errcheck
	})
}

func (cc *ClientConn) fini() {
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
	// Do NOT set cc.shub = nil: concurrent goroutines may still call shub methods.
	// collect net.Conn
	cc.netconn.Close()
	cc.connMtx.Lock()
	cc.connOK = false
	cc.cancel()
	close(cc.monitorStop)
	// Do NOT close writeInCh: external senders guard on ctx.Done() and would panic
	// on a send-to-closed-channel race. After cancel(), no new sends will succeed.
	cc.connMtx.Unlock()
	// Drain buffered packets that were never written.
DRAINED_WRITE_CC:
	for {
		select {
		case pkt, ok := <-cc.writeInCh:
			if !ok {
				break DRAINED_WRITE_CC
			}
			if cc.failedCh != nil && !packet.ConnLayer(pkt) {
				select {
				case cc.failedCh <- pkt:
				case <-cc.ctx.Done():
				}
			}
		default:
			break DRAINED_WRITE_CC
		}
	}
	// the outside should care about channel status
	close(cc.readOutCh)

	close(cc.writeOutCh)
	for pkt := range cc.writeOutCh {
		if cc.failedCh != nil && !packet.ConnLayer(pkt) {
			select {
			case cc.failedCh <- pkt:
			case <-cc.ctx.Done():
			}
		}
	}
	// collect timer
	if !cc.tmrOutside {
		cc.tmr.Close()
	}
	cc.tmr = nil
	// collect fsm
	cc.fsm.EmitEvent(ET_FINI)
	cc.fsm.Close()
	cc.fsm = nil
	// collect channels
	// Heartbeat producers can race teardown; cancellation owns their lifetime.
	// Do not close these queues while the socket reader or timer may send.
	// Do NOT nil these fields: baseConn.writePkt and other goroutines
	// read them at startup and those reads can overlap with fini under
	// rapid open/close. Cancellation stops their users; GC reclaims the
	// queues when the connection is unreferenced.

	cc.log.Debugf("client finished, clientID: %d, remote: %s, meta: %s",
		cc.clientID, remote, string(cc.meta))
}
