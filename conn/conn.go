package conn

import (
	"io"
	"net"
	"sync"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio/packet"
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
	Read() (packet.Packet, error)
	Write(pkt packet.Packet) error

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

	writeFromUpCh, readToUpCh chan packet.Packet

	pf *packet.PacketFactory
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

	//delegate Delegate
	tmr        timer.Timer
	tmrOutside bool
	hbTick     timer.Tick

	connOK  bool
	connMtx sync.RWMutex
}

func (bc *baseConn) Read() (packet.Packet, error) {
	pkt, ok := <-bc.readToUpCh
	if !ok {
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
	bc.writeFromUpCh <- pkt
	return nil
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
