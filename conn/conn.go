package conn

import (
	"io"
	"net"
	"sync"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/synchub"
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
	ABNORMAL   = "abnormal"
	CLOSE_SENT = "close_sent"
	CLOSE_RECV = "close_recv"
	CLOSE_HALF = "close_half"
	CLOSED     = "closed"
	FINI       = "fini"

	ET_CONNSENT  = "connsent"
	ET_CONNRECV  = "connrecv"
	ET_CONNACK   = "connack"
	ET_ERROR     = "error"
	ET_EOF       = "eof"
	ET_CLOSESENT = "closesent"
	ET_CLOSERECV = "closerecv"
	ET_CLOSEACK  = "closeack"
	ET_FINI      = "fini"
)

type Reader interface {
	Read() (packet.Packet, error)
}

type Writer interface {
	Write(pkt packet.Packet) error
}

type Closer interface {
	Close()
}

type Conn interface {
	// row functions
	Reader
	Writer
	Closer

	// meta
	ClientID() uint64
	Meta() []byte
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Side() Side
}

type connOpts struct {
	clientID    uint64
	heartbeat   packet.Heartbeat
	retain      bool
	clear       bool
	waitTimeout uint64
	meta        []byte
	pf          *packet.PacketFactory
}

type Side int

const (
	ClientSide Side = 0
	ServerSide Side = 1
)

type baseConn struct {
	connOpts
	cn Conn

	fsm     *yafsm.FSM
	netconn net.Conn
	side    Side
	shub    *synchub.SyncHub
	log     log.Logger

	readInCh, writeOutCh chan packet.Packet // io neighbor channel
	readOutCh, writeInCh chan packet.Packet // to outside
	failedCh             chan packet.Packet

	//delegate Delegate
	tmr        timer.Timer
	tmrOutside bool
	hbTick     timer.Tick

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

func (bc *baseConn) Write(pkt packet.Packet) error {
	bc.connMtx.RLock()
	defer bc.connMtx.RUnlock()
	if !bc.connOK {
		return io.EOF
	}
	bc.writeInCh <- pkt
	return nil
}

// common read/write/handle
func (bc *baseConn) writePkt() {
	writeOutCh := bc.writeOutCh
	err := error(nil)

	for {
		select {
		case pkt, ok := <-writeOutCh:
			if !ok {
				bc.log.Infof("conn write done, clientID: %d", bc.clientID)
				return
			}
			bc.log.Tracef("conn write down, clientID: %d, packetID: %d, packetType: %s",
				bc.clientID, pkt.ID(), pkt.Type().String())
			err = bc.dowritePkt(pkt, true)
			if err != nil {
				return
			}
		}
	}
}

func (bc *baseConn) dowritePkt(pkt packet.Packet, record bool) error {
	err := packet.EncodeToWriter(pkt, bc.netconn)
	if err != nil {
		bc.log.Errorf("conn write down err: %s, clientID: %d, packetID: %d",
			err, bc.clientID, pkt.ID())
		if record && bc.failedCh != nil {
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
				bc.log.Infof("conn read down closed, clientID: %d", bc.clientID)
			} else {
				bc.log.Infof("conn read down err: %s, clientID: %d",
					err, bc.clientID)
			}
			goto FINI
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
	bc.writeInCh <- retPkt
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
	bc.writeOutCh <- pkt
	bc.log.Debugf("send dis conn succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
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
	bc.writeOutCh <- pkt
	bc.log.Tracef("send data succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
		bc.clientID, pkt.ID(), bc.netconn.RemoteAddr(), string(bc.meta))
	return iodefine.IOSuccess
}

func (bc *baseConn) LocalAddr() net.Addr {
	return bc.netconn.LocalAddr()
}

func (bc *baseConn) RemoteAddr() net.Addr {
	return bc.netconn.RemoteAddr()
}

func (bc *baseConn) Side() Side {
	return bc.side
}

func (bc *baseConn) Close() {
	bc.cn.Close()
}

func (bc *baseConn) Meta() []byte {
	return bc.meta
}

func (bc *baseConn) ClientID() uint64 {
	return bc.clientID
}
