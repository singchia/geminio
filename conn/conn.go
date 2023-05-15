package conn

import (
	"io"
	"net"
	"sync"

	"github.com/singchia/geminio/delegate"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/log"
	"github.com/singchia/geminio/pkg/synchub"
	"github.com/singchia/go-timer"
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

type Conn interface {
	Start() error
	Close()
	LocalAddr() net.Addr
	RemoteAddr() net.Addr

	init() error
	fini()
	close()
	readPkt()
	writePkt()
}

type ConnOption func(*BaseConn) error

func OptionPacketFactory(pf *packet.PacketFactory) ConnOption {
	return func(bc *BaseConn) error {
		bc.pf = pf
		return nil
	}
}

func OptionTimer(tmr timer.Timer) ConnOption {
	return func(bc *BaseConn) error {
		bc.tmr = tmr
		bc.tmrOutsIDe = true
		return nil
	}
}

func OptionDelegate(dlgt delegate.Delegate) ConnOption {
	return func(bc *BaseConn) error {
		bc.dlgt = dlgt
		return nil
	}
}

func OptionLoggy(log log.Loggy) ConnOption {
	return func(bc *BaseConn) error {
		bc.log = log
		return nil
	}
}

type ConnOpts struct {
	ClientID    uint64
	Heartbeat   packet.Heartbeat
	Retain      bool
	Clear       bool
	WaitTimeout uint64
	Meta        []byte

	writeFromUpCh, readToUpCh chan packet.Packet
	wg                        sync.WaitGroup

	pf *packet.PacketFactory

	// delegate
	dlgt delegate.Delegate
}

type SIDe int

const (
	ClientSIDe SIDe = 0
	ServerSIDe SIDe = 1
)

type BaseConn struct {
	ConnOpts
	cn Conn

	fsm     *yafsm.FSM
	netconn net.Conn
	SIDe    SIDe
	shub    *synchub.SyncHub
	log     log.Loggy

	//delegate Delegate
	tmr        timer.Timer
	tmrOutsIDe bool
	hbTick     timer.Tick

	connOK  bool
	connMtx sync.RWMutex
}

func (bc *BaseConn) Read() (packet.Packet, error) {
	pkt, ok := <-bc.readToUpCh
	if !ok {
		return nil, io.EOF
	}
	return pkt, nil
}

func (bc *BaseConn) Write(pkt packet.Packet) error {
	bc.connMtx.RLock()
	defer bc.connMtx.RUnlock()

	if !bc.connOK {
		return io.EOF
	}
	bc.writeFromUpCh <- pkt
	return nil
}

func (bc *BaseConn) LocalAddr() net.Addr {
	return bc.netconn.LocalAddr()
}

func (bc *BaseConn) RemoteAddr() net.Addr {
	return bc.netconn.RemoteAddr()
}

func (bc *BaseConn) Start() error {
	bc.wg.Add(1)

	go bc.cn.readPkt()
	go bc.cn.writePkt()

	err := bc.cn.init()
	if err != nil {
		bc.cn.fini()
		return err
	}
	return nil
}

func (bc *BaseConn) init() error {
	return nil
}

func (bc *BaseConn) close() {}

func (bc *BaseConn) Close() {
	bc.cn.Close()
}

func (bc *BaseConn) readPkt() {}

func (bc *BaseConn) writePkt() {}
