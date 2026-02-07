package conn

import (
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
	readOutCh, writeInCh     chan packet.Packet // to outside
	readInSize, writeOutSize int
	readOutSize, writeInSize int
	failedCh                 chan packet.Packet
	heartbeatCh              chan packet.Packet // priority channel for heartbeat packets (receive)
	heartbeatWriteCh         chan packet.Packet // priority channel for heartbeat packets (send)

	// heartbeat
	hbTick timer.Tick

	connOK  bool
	connMtx sync.RWMutex
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
	writeInCh := bc.writeInCh
	writeInSize := bc.writeInSize
	clientID := bc.clientID
	bc.connMtx.RUnlock()

	// Channel operations don't need lock (len() and <- are thread-safe)
	writeInLen := len(writeInCh)
	// Log warning if channel is already > 80% full
	if writeInLen > 0 && writeInLen*100/writeInSize > 80 {
		bc.log.Warnf("writeInCh is >80%% full (%d/%d), clientID: %d, packetID: %d, packetType: %s",
			writeInLen, writeInSize, clientID, pkt.ID(), pkt.Type().String())
	}
	writeInCh <- pkt
	return nil
}

// common read/write/handle
func (bc *baseConn) writePkt() {
	writeOutCh := bc.writeOutCh
	heartbeatWriteCh := bc.heartbeatWriteCh
	err := error(nil)
	lastLogTime := time.Now()
	packetCount := 0

	for {
		select {
		case pkt := <-heartbeatWriteCh:
			// Priority processing for heartbeat packets to avoid being blocked by application layer messages
			bc.log.Tracef("conn write heartbeat down, clientID: %d, packetID: %d, packetType: %s, writeOutCh remaining: %d/%d",
				bc.clientID, pkt.ID(), pkt.Type().String(), len(writeOutCh), bc.writeOutSize)
			record := !packet.ConnLayer(pkt)
			writeStart := time.Now()
			err = bc.dowritePkt(pkt, record)
			writeDuration := time.Since(writeStart)
			if err != nil {
				// write to net Conn error, we should close the layer
				bc.log.Errorf("conn write heartbeat error, closing connection: %s, clientID: %d, packetID: %d, packetType: %s, writeDuration: %v",
					err, bc.clientID, pkt.ID(), pkt.Type().String(), writeDuration)
				bc.Close()
				//return
			}
			if writeDuration > 5*time.Second {
				bc.log.Warnf("heartbeat write took too long, clientID: %d, packetID: %d, writeDuration: %v (network may be blocking)",
					bc.clientID, pkt.ID(), writeDuration)
			}
		case pkt, ok := <-writeOutCh:
			if !ok {
				bc.log.Debugf("conn write done, clientID: %d", bc.clientID)
				return
			}
			packetCount++
			// Log periodically to ensure writePkt() is still running
			now := time.Now()
			if now.Sub(lastLogTime) > 10*time.Second {
				bc.log.Infof("writePkt() is running, clientID: %d, packets processed: %d, writeOutCh remaining: %d/%d",
					bc.clientID, packetCount, len(writeOutCh), bc.writeOutSize)
				lastLogTime = now
			}
			bc.log.Tracef("conn write down, clientID: %d, packetID: %d, packetType: %s, writeOutCh remaining: %d/%d",
				bc.clientID, pkt.ID(), pkt.Type().String(), len(writeOutCh), bc.writeOutSize)
			record := !packet.ConnLayer(pkt)
			writeStart := time.Now()
			err = bc.dowritePkt(pkt, record)
			writeDuration := time.Since(writeStart)
			if err != nil {
				// write to net Conn error, we should close the layer
				bc.log.Errorf("conn write error, closing connection: %s, clientID: %d, packetID: %d, packetType: %s, writeDuration: %v",
					err, bc.clientID, pkt.ID(), pkt.Type().String(), writeDuration)
				bc.Close()
				//return
			}
			if writeDuration > 5*time.Second {
				bc.log.Warnf("data packet write took too long, clientID: %d, packetID: %d, writeDuration: %v (network may be blocking)",
					bc.clientID, pkt.ID(), writeDuration)
			}
		}
	}
}

func (bc *baseConn) dowritePkt(pkt packet.Packet, record bool) error {
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
		// Route heartbeat packets to priority channel for fast processing
		if pkt.Type() == packet.TypeHeartbeatPacket || pkt.Type() == packet.TypeHeartbeatAckPacket {
			select {
			case bc.heartbeatCh <- pkt:
			default:
				// If heartbeat channel is full, fallback to normal channel
				// This should rarely happen as heartbeat packets are small and infrequent
				readInCh <- pkt
			}
		} else {
			readInCh <- pkt
		}
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
	// Use non-blocking send to avoid deadlock:
	// handlePkt() is reading from writeInCh in the same goroutine,
	// so if writeInCh is full, blocking here would cause deadlock
	// Use a goroutine to send asynchronously to break the deadlock
	go func() {
		bc.writeInCh <- retPkt
	}()
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
	// DisConnPacket is critical (connection-related), must be sent successfully, so we block here
	// This ensures the disconnection packet is always sent, even if writeOutCh is full
	bc.writeOutCh <- pkt
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
	// make sure this packet is flushed before writeOutCh closed
	err = bc.dowritePkt(pkt, false)
	if err != nil {
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
	// Check if connection is still OK before attempting to send
	// This prevents sending to a closed writeOutCh after dis conn is received
	bc.connMtx.RLock()
	if !bc.connOK {
		bc.connMtx.RUnlock()
		// Connection is closed (e.g., after receiving dis conn), discard the packet
		bc.log.Debugf("connection closed, data packet discarded: clientID: %d, packetID: %d, packetType: %s",
			bc.clientID, pkt.ID(), pkt.Type().String())
		return iodefine.IODiscard
	}
	writeOutCh := bc.writeOutCh
	writeOutSize := bc.writeOutSize
	writeOutLen := len(writeOutCh)
	bc.connMtx.RUnlock()

	// Log warning if channel is already > 80% full
	if writeOutLen*100/writeOutSize > 80 {
		bc.log.Warnf("writeOutCh is >80%% full (%d/%d), clientID: %d, packetID: %d, packetType: %s",
			writeOutLen, writeOutSize, bc.clientID, pkt.ID(), pkt.Type().String())
	}

	// Data packets: block to ensure delivery, no packet loss
	// This ensures all packets are sent, even if it means slower processing
	// The blocking will wait for writePkt() to process packets and make room
	writeOutCh <- pkt
	bc.log.Tracef("send data succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		bc.clientID, pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta))
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
