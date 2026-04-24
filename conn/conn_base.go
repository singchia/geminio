package conn

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
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

	connOK      bool
	connMtx     sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	monitorStop chan struct{} // closed when conn shuts down to stop the channel-monitor goroutine
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
	clientID := bc.ClientID()
	bc.connMtx.RUnlock()

	// Channel operations don't need lock (len() and <- are thread-safe)
	writeInLen := len(writeInCh)
	// Log warning if channel is already > 80% full
	if writeInLen > 0 && writeInLen*100/writeInSize > 80 {
		bc.log.Warnf("writeInCh is >80%% full (%d/%d), clientID: %d, packetID: %d, packetType: %s",
			writeInLen, writeInSize, clientID, pkt.ID(), pkt.Type().String())
	}
	select {
	case writeInCh <- pkt:
		return nil
	case <-bc.ctx.Done():
		return io.EOF
	}
}

// sendToWriteIn is the single entry point for all writes to writeInCh.
// It selects on ctx.Done() so it never sends after fini() cancels the context.
func (bc *baseConn) sendToWriteIn(pkt packet.Packet) error {
	select {
	case <-bc.ctx.Done():
		return io.EOF
	case bc.writeInCh <- pkt:
		return nil
	}
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
		case pkt, ok := <-heartbeatWriteCh:
			if !ok {
				return
			}
			// Priority processing for heartbeat packets to avoid being blocked by application layer messages
			bc.log.Tracef("conn write heartbeat down, clientID: %d, packetID: %d, packetType: %s, writeOutCh remaining: %d/%d",
				bc.ClientID(), pkt.ID(), pkt.Type().String(), len(writeOutCh), bc.writeOutSize)
			record := !packet.ConnLayer(pkt)
			writeStart := time.Now()
			err = bc.dowritePkt(pkt, record)
			writeDuration := time.Since(writeStart)
			if err != nil {
				// write to net Conn error, we should close the layer
				if bc.ctx.Err() != nil {
					bc.log.Debugf("conn write heartbeat error, closing connection: %s, clientID: %d, packetID: %d, packetType: %s, writeDuration: %v",
						err, bc.ClientID(), pkt.ID(), pkt.Type().String(), writeDuration)
				} else {
					bc.log.Errorf("conn write heartbeat error, closing connection: %s, clientID: %d, packetID: %d, packetType: %s, writeDuration: %v",
						err, bc.ClientID(), pkt.ID(), pkt.Type().String(), writeDuration)
				}
				bc.netconn.Close()
				bc.cancel()
				return
			}
			if writeDuration > 5*time.Second {
				bc.log.Warnf("heartbeat write took too long, clientID: %d, packetID: %d, writeDuration: %v (network may be blocking)",
					bc.ClientID(), pkt.ID(), writeDuration)
			}
		case pkt, ok := <-writeOutCh:
			if !ok {
				bc.log.Debugf("conn write done, clientID: %d", bc.ClientID())
				return
			}
			packetCount++
			// Log periodically to ensure writePkt() is still running
			now := time.Now()
			if now.Sub(lastLogTime) > 10*time.Second {
				bc.log.Infof("writePkt() is running, clientID: %d, packets processed: %d, writeOutCh remaining: %d/%d",
					bc.ClientID(), packetCount, len(writeOutCh), bc.writeOutSize)
				lastLogTime = now
			}
			bc.log.Tracef("conn write down, clientID: %d, packetID: %d, packetType: %s, writeOutCh remaining: %d/%d",
				bc.ClientID(), pkt.ID(), pkt.Type().String(), len(writeOutCh), bc.writeOutSize)
			record := !packet.ConnLayer(pkt)
			writeStart := time.Now()
			err = bc.dowritePkt(pkt, record)
			writeDuration := time.Since(writeStart)
			if err != nil {
				// write to net Conn error, we should close the layer.
				// Teardown-path write failures (ctx canceled or the net.Conn
				// already gone) are expected — log at Debug. Anything else
				// is a genuine IO problem and stays at Error.
				if bc.ctx.Err() != nil || iodefine.IsConnGone(err) {
					bc.log.Debugf("conn write error, closing connection: %s, clientID: %d, packetID: %d, packetType: %s, writeDuration: %v",
						err, bc.ClientID(), pkt.ID(), pkt.Type().String(), writeDuration)
				} else {
					bc.log.Errorf("conn write error, closing connection: %s, clientID: %d, packetID: %d, packetType: %s, writeDuration: %v",
						err, bc.ClientID(), pkt.ID(), pkt.Type().String(), writeDuration)
				}
				bc.netconn.Close()
				bc.cancel()
				return
			}
			if writeDuration > 5*time.Second {
				bc.log.Warnf("data packet write took too long, clientID: %d, packetID: %d, writeDuration: %v (network may be blocking)",
					bc.ClientID(), pkt.ID(), writeDuration)
			}
		}
	}
}

func (bc *baseConn) dowritePkt(pkt packet.Packet, record bool) error {
	err := packet.EncodeToWriter(pkt, bc.netconn)
	if err != nil {
		// Expected during teardown: our own ctx canceled, or the underlying
		// net.Conn was closed by the peer while we were flushing a trailing
		// packet (typically a DismissAck). Log at Debug so ordinary close
		// paths do not spam ERROR.
		if bc.ctx.Err() != nil || iodefine.IsConnGone(err) {
			bc.log.Debugf("conn write down err: %s, clientID: %d, packetID: %d, packetType: %s",
				err, bc.ClientID(), pkt.ID(), pkt.Type().String())
		} else {
			bc.log.Errorf("conn write down err: %s, clientID: %d, packetID: %d, packetType: %s",
				err, bc.ClientID(), pkt.ID(), pkt.Type().String())
		}
		if record && bc.failedCh != nil {
			// only upper layer packet need to be notified
			bc.failedCh <- pkt
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
				bc.log.Debugf("conn read down closed, clientID: %d", bc.ClientID())
			} else {
				bc.log.Debugf("conn read down err: %s, clientID: %d",
					err, bc.ClientID())
			}
			goto FINI
		}
		// Check packet size to prevent OOM attacks from oversized packets
		if pkt.Length() > DefaultMaxPacketSize {
			bc.log.Debugf("packet too large, discarding: clientID: %d, packetID: %d, packetType: %s, size: %d, max: %d",
				bc.ClientID(), pkt.ID(), pkt.Type().String(), pkt.Length(), DefaultMaxPacketSize)
			// Discard the oversized packet and continue reading
			continue
		}
		bc.log.Tracef("read %s , clientID: %d, packetID: %d, packetType: %s",
			pkt.Type().String(), bc.ClientID(), pkt.ID(), pkt.Type().String())
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
		bc.ClientID(), pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta))

	err := bc.fsm.EmitEvent(ET_CLOSERECV)
	if err != nil {
		bc.log.Errorf("emit ET_CLOSERECV err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, bc.ClientID(), pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta), bc.fsm.State())
		return iodefine.IOErr
	}
	retPkt := bc.pf.NewDisConnAckPacket(pkt.PacketID, nil)
	// Send from a goroutine because handlePkt() is the only consumer of writeInCh;
	// blocking here would deadlock. sendToWriteIn selects on ctx.Done() for safety.
	go func() { bc.sendToWriteIn(retPkt) }() //nolint:errcheck
	// send our side close while receiving close packet
	bc.Close()
	return iodefine.IOSuccess
}

func (bc *baseConn) handleInDisConnAckPacket(pkt *packet.DisConnAckPacket) iodefine.IORet {
	bc.log.Debugf("read dis conn ack packet, clientID: %d, packetID: %d, remote: %s, meta: %s",
		bc.ClientID(), pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta))

	err := bc.fsm.EmitEvent(ET_CLOSEACK)
	if err != nil {
		bc.log.Errorf("emit in ET_CLOSEACK err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
			err, bc.ClientID(), pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta), bc.fsm.State())
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
			err, bc.ClientID(), pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta), bc.fsm.State())
		return iodefine.IOErr
	}
	// DisConnPacket is critical (connection-related), must be sent successfully, so we block here
	// This ensures the disconnection packet is always sent, even if writeOutCh is full
	select {
	case bc.writeOutCh <- pkt:
	case <-bc.ctx.Done():
		return iodefine.IOClosed
	}
	bc.log.Debugf("send dis conn down succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		bc.ClientID(), pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta))
	return iodefine.IOSuccess
}

func (bc *baseConn) handleOutDisConnAckPacket(pkt *packet.DisConnAckPacket) iodefine.IORet {
	err := bc.fsm.EmitEvent(ET_CLOSEACK)
	if err != nil {
		bc.log.Errorf("emit out ET_CLOSEACK err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
			err, bc.ClientID(), pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta), bc.fsm.State())
		return iodefine.IOErr
	}
	// make sure this packet is flushed before writeOutCh closed
	err = bc.dowritePkt(pkt, false)
	if err != nil {
		return iodefine.IOErr
	}
	bc.log.Debugf("send dis conn ack succeed, clientID: %d, PacketID: %d, packetType: %s",
		bc.ClientID(), pkt.ID(), pkt.Type().String())
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
			bc.ClientID(), pkt.ID(), pkt.Type().String())
		return iodefine.IODiscard
	}
	writeOutCh := bc.writeOutCh
	writeOutSize := bc.writeOutSize
	writeOutLen := len(writeOutCh)
	bc.connMtx.RUnlock()

	// Log warning if channel is already > 80% full
	if writeOutLen*100/writeOutSize > 80 {
		bc.log.Warnf("writeOutCh is >80%% full (%d/%d), clientID: %d, packetID: %d, packetType: %s",
			writeOutLen, writeOutSize, bc.ClientID(), pkt.ID(), pkt.Type().String())
	}

	// Data packets: block to ensure delivery, no packet loss
	// This ensures all packets are sent, even if it means slower processing
	// The blocking will wait for writePkt() to process packets and make room
	select {
	case writeOutCh <- pkt:
	case <-bc.ctx.Done():
		return iodefine.IOClosed
	}
	bc.log.Tracef("send data succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		bc.ClientID(), pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta))
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
	return atomic.LoadUint64(&bc.clientID)
}

func (bc *baseConn) Close() {
	bc.cn.Close()
}
