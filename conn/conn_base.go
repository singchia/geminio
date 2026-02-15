package conn

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/iodefine"
	"github.com/singchia/go-timer/v2"
	"github.com/singchia/yafsm"
)

const (
	INIT       = "init"
	CONN_SENT  = "conn_sent"
	CONN_RECV  = "conn_recv"
	CONNED     = "conned"
	CLOSE_SENT = "close_sent"
	CLOSE_RECV = "close_recv"
	CLOSE_HALF = "close_half"
	CLOSED     = "closed"
	FINI       = "fini"

	ET_CONNSENT  = "connsent"
	ET_CONNRECV  = "connrecv"
	ET_CONNACK   = "connack"
	ET_ERROR     = "error"
	ET_CLOSESENT = "closesent"
	ET_CLOSERECV = "closerecv"
	ET_CLOSEACK  = "closeack"
	ET_FINI      = "fini"

	// DefaultMaxPacketSize is the default maximum packet payload size (10MB)
	// This prevents OOM attacks and memory exhaustion from oversized packets
	DefaultMaxPacketSize = 10 * 1024 * 1024 // 10MB
)

type connOpts struct {
	clientID uint64
	// timer
	tmr        timer.Timer
	tmrOutside bool
	heartbeat  packet.Heartbeat

	waitTimeout uint64
	meta        []byte
	pf          packet.PacketFactory
	log         log.Logger
	// options for future usage
	retain bool
	clear  bool
}

type baseConn struct {
	connOpts
	cn Conn

	fsm     *yafsm.FSM
	netconn net.Conn
	side    geminio.Side
	onlined bool
	// sync hub
	shub *synchub.SyncHub

	// read write failed channel
	readInCh, writeOutCh     chan packet.Packet // io neighbor channel
	readOutCh                chan packet.Packet // to outside
	readInSize, writeOutSize int
	readOutSize              int
	failedCh                 chan packet.Packet

	// heartbeat
	hbTick timer.Tick

	connOK  bool
	connMtx sync.RWMutex

	allDoneCount int64
}

func (bc *baseConn) Read() (packet.Packet, error) {
	pkt, ok := <-bc.readOutCh
	if !ok {
		bc.readOutCh = nil
		return nil, io.EOF
	}
	return pkt, nil
}

func (bc *baseConn) ChannelRead() <-chan packet.Packet {
	return bc.readOutCh
}

func (bc *baseConn) Write(pkt packet.Packet) error {
	// Minimize lock holding time: only check connOK and get channel reference
	bc.connMtx.RLock()
	if !bc.connOK {
		bc.connMtx.RUnlock()
		return io.EOF
	}
	clientID := bc.clientID
	bc.connMtx.RUnlock()

	// Channel operations don't need lock (len() and <- are thread-safe)
	writeOutLen := len(bc.writeOutCh)
	// Log warning if channel is already > 80% full
	if writeOutLen > 0 && writeOutLen*100/bc.writeOutSize > 80 {
		bc.log.Warnf("writeOutCh is >80%% full (%d/%d), clientID: %d, packetID: %d, packetType: %s",
			writeOutLen, bc.writeOutSize, clientID, pkt.ID(), pkt.Type().String())
	}
	bc.writeOutCh <- pkt
	return nil
}

func (bc *baseConn) dowritePkt(pkt packet.Packet, record bool) error {
	// Check if netconn is nil (may happen if connection is being closed)
	if bc.netconn == nil {
		return errors.New("netconn is nil, connection is closed")
	}

	// Log before encoding to track if encoding is slow
	startTime := time.Now()
	pktSize := pkt.Length()

	// Set write deadline to prevent indefinite blocking (30 seconds timeout)
	// This ensures that if network write blocks, we can detect and handle it
	writeDeadline := time.Now().Add(30 * time.Second)
	err := bc.netconn.SetWriteDeadline(writeDeadline)
	if err != nil {
		bc.log.Warnf("failed to set write deadline: %s, clientID: %d, packetID: %d",
			err, bc.clientID, pkt.ID())
		// Continue anyway, but log the warning
	}

	// Encode packet (may be slow for large packets)
	encodeStart := time.Now()
	err = packet.EncodeToWriter(pkt, bc.netconn)
	encodeDuration := time.Since(encodeStart)
	totalDuration := time.Since(startTime)

	// Clear write deadline after write
	bc.netconn.SetWriteDeadline(time.Time{})

	if err != nil {
		bc.log.Errorf("conn write down err: %s, clientID: %d, packetID: %d, packetType: %s, size: %d, encodeDuration: %v, totalDuration: %v",
			err, bc.clientID, pkt.ID(), pkt.Type().String(), pktSize, encodeDuration, totalDuration)
		if record && bc.failedCh != nil {
			// only upper layer packet need to be notified
			bc.failedCh <- pkt
		}
	} else {
		// Log slow writes to help diagnose blocking issues
		// If encodeDuration > 100ms, it means network write is blocking
		if encodeDuration > 100*time.Millisecond {
			bc.log.Warnf("slow packet write (network may be blocking), clientID: %d, packetID: %d, packetType: %s, size: %d, encodeDuration: %v, totalDuration: %v",
				bc.clientID, pkt.ID(), pkt.Type().String(), pktSize, encodeDuration, totalDuration)
		}
	}
	return err
}

func (bc *baseConn) readPkt() {
	readInCh := bc.readInCh

	for {
		pkt, err := packet.DecodeFromReader(bc.netconn)
		if err != nil {
			if iodefine.ErrUseOfClosedNetwork(err) {
				bc.log.Debugf("conn read down closed, clientID: %d", bc.clientID)
			} else {
				bc.log.Debugf("conn read down err: %s, clientID: %d",
					err, bc.clientID)
			}
			goto FINI
		}
		// Check packet size to prevent OOM attacks from oversized packets
		if pkt.Length() > DefaultMaxPacketSize {
			bc.log.Debugf("packet too large, discarding: clientID: %d, packetID: %d, packetType: %s, size: %d, max: %d",
				bc.clientID, pkt.ID(), pkt.Type().String(), pkt.Length(), DefaultMaxPacketSize)
			// Discard the oversized packet and continue reading
			continue
		}
		bc.log.Tracef("read %s , clientID: %d, packetID: %d, packetType: %s",
			pkt.Type().String(), bc.clientID, pkt.ID(), pkt.Type().String())
		readInCh <- pkt
	}
FINI:
	close(readInCh)
}

// common in packet
func (bc *baseConn) handleInDisConnPacket(pkt *packet.DisConnPacket) iodefine.IORet {
	bc.log.Debugf("recv dis conn succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		bc.clientID, pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta))

	err := bc.fsm.EmitEvent(ET_CLOSERECV)
	if err != nil {
		bc.log.Errorf("emit ET_CLOSERECV err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, bc.clientID, pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta), bc.fsm.State())
		return iodefine.IOErr
	}
	retPkt := bc.pf.NewDisConnAckPacket(pkt.PacketID, nil)
	// Directly call handleOutDisConnAckPacket to avoid deadlock:
	// - DisConnAckPacket is a connection-layer packet, can be handled directly
	// - handleOutDisConnAckPacket sends to writeOutCh (not writeInCh), avoiding deadlock
	// - writeOutCh is read by writePkt() goroutine (not handlePkt()), so no deadlock risk
	// - handlePkt() is reading from writeInCh in the same goroutine,
	//   so sending to writeInCh would cause deadlock if channel is full
	// - By directly calling handleOutDisConnAckPacket, we bypass writeInCh entirely
	//
	// NOTE: Packet ordering risk:
	// - If writeInCh has queued data packets, they will be processed by handlePkt() and sent to writeOutCh
	// - DisConnAckPacket is sent directly to writeOutCh, so it may be sent before writeInCh packets
	// - This is acceptable because:
	//   1. writeOutCh is FIFO, so if writeOutCh already has packets, DisConnAckPacket will be queued after them
	//   2. writeInCh packets that are already being processed will reach writeOutCh before DisConnAckPacket
	//   3. Only writeInCh packets that haven't started processing yet may be sent after DisConnAckPacket
	// - The peer should handle this gracefully by continuing to receive packets until DisConnPacket is received
	writeOutChLen := len(bc.writeOutCh)
	if writeOutChLen > 0 {
		bc.log.Debugf("dis conn ack may be sent before some data packets, writeOutCh: %d, clientID: %d",
			writeOutChLen, bc.clientID)
	}
	bc.writeOutCh <- retPkt
	// IOClosed is expected when connection is fully closed (not half-closed)
	// This is normal behavior, we continue with closing the connection
	// send our side close while receiving close packet
	bc.Close()
	return iodefine.IOSuccess
}

func (bc *baseConn) handleInDisConnAckPacket(pkt *packet.DisConnAckPacket) iodefine.IORet {
	bc.log.Debugf("read dis conn ack packet, clientID: %d, packetID: %d, remote: %s, meta: %s",
		bc.clientID, pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta))

	err := bc.fsm.EmitEvent(ET_CLOSEACK)
	if err != nil {
		bc.log.Errorf("emit in ET_CLOSEACK err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, bc.clientID, pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta), bc.fsm.State())
		return iodefine.IOErr
	}
	if bc.fsm.State() == CLOSE_HALF {
		return iodefine.IOSuccess
	}
	return iodefine.IOClosed
}

// common out packet
func (bc *baseConn) handleOutDisConnPacket(pkt *packet.DisConnPacket) iodefine.IORet {
	err := bc.fsm.EmitEvent(ET_CLOSESENT)
	if err != nil {
		bc.log.Errorf("emit out ET_CLOSESENT err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, bc.clientID, pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta), bc.fsm.State())
		return iodefine.IOErr
	}

	err = bc.dowritePkt(pkt, true)
	if err != nil {
		bc.log.Errorf("send dis conn packet err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
			err, bc.clientID, pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta), bc.fsm.State())
		return iodefine.IOErr
	}
	bc.log.Debugf("send dis conn down succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		bc.clientID, pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta))
	return iodefine.IOSuccess
}

func (bc *baseConn) handleOutDisConnAckPacket(pkt *packet.DisConnAckPacket) iodefine.IORet {
	err := bc.fsm.EmitEvent(ET_CLOSEACK)
	if err != nil {
		bc.log.Errorf("emit out ET_CLOSEACK err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
			err, bc.clientID, pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta), bc.fsm.State())
		return iodefine.IOErr
	}
	err = bc.dowritePkt(pkt, true)
	if err != nil {
		bc.log.Errorf("send dis conn ack packet err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
			err, bc.clientID, pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta), bc.fsm.State())
		return iodefine.IOErr
	}
	bc.log.Debugf("send dis conn ack succeed, clientID: %d, PacketID: %d, packetType: %s",
		bc.clientID, pkt.ID(), pkt.Type().String())
	if bc.fsm.State() == CLOSE_HALF {
		return iodefine.IOSuccess
	}
	return iodefine.IOClosed
}

func (bc *baseConn) handleOutDataPacket(pkt packet.Packet) iodefine.IORet {
	err := bc.dowritePkt(pkt, false)
	if err != nil {
		bc.log.Errorf("send data packet err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
			err, bc.clientID, pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta), bc.fsm.State())
		return iodefine.IOErr
	}
	bc.log.Debugf("send data succeed, clientID: %d, PacketID: %d, packetType: %s",
		bc.clientID, pkt.ID(), pkt.Type().String())
	return iodefine.IOSuccess
}

func (bc *baseConn) handleOutHeartbeatAckPacket(pkt *packet.HeartbeatAckPacket) iodefine.IORet {
	err := bc.dowritePkt(pkt, true)
	if err != nil {
		bc.log.Errorf("send heartbeat ack packet err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
			err, bc.clientID, pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta), bc.fsm.State())
		return iodefine.IOErr
	}
	bc.log.Debugf("send heartbeat ack succeed, clientID: %d, PacketID: %d, packetType: %s",
		bc.clientID, pkt.ID(), pkt.Type().String())
	return iodefine.IOSuccess
}

// meta related functions
func (bc *baseConn) LocalAddr() net.Addr {
	return bc.netconn.LocalAddr()
}

func (bc *baseConn) RemoteAddr() net.Addr {
	return bc.netconn.RemoteAddr()
}

func (bc *baseConn) Side() geminio.Side {
	return bc.side
}

func (bc *baseConn) Meta() []byte {
	return bc.meta
}

func (bc *baseConn) ClientID() uint64 {
	return bc.clientID
}

func (bc *baseConn) Close() {
	bc.cn.Close()
}
