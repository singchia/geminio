package conn

import (
	"errors"
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

type Dialer func() (net.Conn, error)

type SendConn struct {
	*baseConn
	dlgt    SendConnDelegate
	dialer  Dialer
	writeCh chan packet.Packet

	onceFini  *sync.Once
	onceClose *sync.Once
}

type SendConnOption func(*SendConn) error

func OptionSendConnPacketFactory(pf *packet.PacketFactory) SendConnOption {
	return func(sc *SendConn) error {
		sc.pf = pf
		return nil
	}
}

func OptionSendConnTimer(tmr timer.Timer) SendConnOption {
	return func(sc *SendConn) error {
		sc.tmr = tmr
		sc.tmrOutside = true
		return nil
	}
}

func OptionSendConnDelegate(dlgt SendConnDelegate) SendConnOption {
	return func(sc *SendConn) error {
		sc.dlgt = dlgt
		return nil
	}
}

func OptionSendConnLogger(log log.Logger) SendConnOption {
	return func(sc *SendConn) error {
		sc.log = log
		return nil
	}
}

func OptionSendConnMeta(meta []byte) SendConnOption {
	return func(sc *SendConn) error {
		sc.meta = meta
		return nil
	}
}

func OptionSendConnClientID(clientID uint64) SendConnOption {
	return func(sc *SendConn) error {
		sc.clientID = clientID
		return nil
	}
}

func NewSendConn(netconn net.Conn, opts ...SendConnOption) (*SendConn, error) {
	return newSendConn(netconn, opts...)
}

func NewSendConnWithDialer(dialer Dialer, opts ...SendConnOption) (*SendConn, error) {
	netconn, err := dialer()
	if err != nil {
		return nil, err
	}
	return newSendConn(netconn, opts...)
}

func newSendConn(netconn net.Conn, opts ...SendConnOption) (*SendConn, error) {
	err := error(nil)
	sc := &SendConn{
		baseConn: &baseConn{
			connOpts: connOpts{
				clientID:  packet.ClientIDNull,
				heartbeat: packet.Heartbeat20,
				meta:      []byte{},
			},
			netconn: netconn,
			fsm:     yafsm.NewFSM(),
			side:    ClientSide,
			connOK:  true,
		},
		writeCh:   make(chan packet.Packet, 1024),
		onceFini:  new(sync.Once),
		onceClose: new(sync.Once),
	}
	sc.cn = sc
	// options
	for _, opt := range opts {
		err = opt(sc)
		if err != nil {
			return nil, err
		}
	}
	sc.writeFromUpCh = make(chan packet.Packet, 1024)
	sc.readToUpCh = make(chan packet.Packet, 1024)
	// timer
	if !sc.tmrOutside {
		sc.tmr = timer.NewTimer()
	}
	sc.shub = synchub.NewSyncHub(synchub.OptionTimer(sc.tmr))
	// packet factory
	if sc.pf == nil {
		sc.pf = packet.NewPacketFactory(id.NewIDCounter(id.Odd))
	}
	// log
	if sc.log == nil {
		sc.log = log.DefaultLog
	}
	// states
	sc.initFSM()
	// timer
	sc.hbTick = sc.tmr.Add(time.Duration(sc.heartbeat)*time.Second,
		timer.WithHandler(sc.sendHeartbeat), timer.WithCyclically())
	// start
	go sc.readPkt()
	go sc.writePkt()
	err = sc.connect()
	if err != nil {
		goto ERR
	}
	return sc, nil
ERR:
	sc.fini()
	return nil, err
}

func (sc *SendConn) initFSM() {
	init := sc.fsm.AddState(INIT)
	connsent := sc.fsm.AddState(CONN_SENT)
	conned := sc.fsm.AddState(CONNED)
	abnormal := sc.fsm.AddState(ABNORMAL)
	closesent := sc.fsm.AddState(CLOSE_SENT)
	closerecv := sc.fsm.AddState(CLOSE_RECV)
	closehalf := sc.fsm.AddState(CLOSE_HALF)
	closed := sc.fsm.AddState(CLOSED)
	fini := sc.fsm.AddState(FINI)
	sc.fsm.SetState(INIT)

	// events
	sc.fsm.AddEvent(ET_CONNSENT, init, connsent)
	sc.fsm.AddEvent(ET_CONNSENT, abnormal, connsent)
	sc.fsm.AddEvent(ET_CONNACK, connsent, conned)

	sc.fsm.AddEvent(ET_ERROR, conned, abnormal)
	sc.fsm.AddEvent(ET_ERROR, connsent, abnormal)
	sc.fsm.AddEvent(ET_ERROR, closesent, abnormal)
	sc.fsm.AddEvent(ET_ERROR, closerecv, abnormal)

	sc.fsm.AddEvent(ET_EOF, connsent, closed)
	sc.fsm.AddEvent(ET_EOF, conned, closed)

	sc.fsm.AddEvent(ET_CLOSESENT, conned, closesent)
	sc.fsm.AddEvent(ET_CLOSESENT, closerecv, closesent) // close and been closed at same time
	sc.fsm.AddEvent(ET_CLOSESENT, closehalf, closehalf) // been closed and we acked, then start to close

	sc.fsm.AddEvent(ET_CLOSERECV, conned, closerecv)
	sc.fsm.AddEvent(ET_CLOSERECV, closesent, closerecv) // close and been closed at same time
	sc.fsm.AddEvent(ET_CLOSERECV, closehalf, closehalf)

	sc.fsm.AddEvent(ET_CLOSEACK, closesent, closehalf)
	sc.fsm.AddEvent(ET_CLOSEACK, closerecv, closehalf)
	sc.fsm.AddEvent(ET_CLOSEACK, closehalf, closed) // close and been closed at same time
	// fini
	sc.fsm.AddEvent(ET_FINI, init, fini)
	sc.fsm.AddEvent(ET_FINI, connsent, fini)
	sc.fsm.AddEvent(ET_FINI, conned, fini)
	sc.fsm.AddEvent(ET_FINI, abnormal, fini)
	sc.fsm.AddEvent(ET_FINI, closesent, fini)
	sc.fsm.AddEvent(ET_FINI, closerecv, fini)
	sc.fsm.AddEvent(ET_FINI, closehalf, fini)
	sc.fsm.AddEvent(ET_FINI, closed, fini)
}

func (sc *SendConn) connect() error {
	pkt := sc.pf.NewConnPacket(sc.clientID, sc.heartbeat, sc.meta)
	sc.writeCh <- pkt
	sync := sc.shub.New(pkt.PacketID, synchub.WithTimeout(10*time.Second))
	event := <-sync.C()

	if event.Error != nil {
		sc.log.Errorf("connect err: %s, clientID: %d, remote: %s",
			event.Error, sc.clientID, sc.netconn.RemoteAddr())
		return event.Error
	}
	sc.log.Debugf("connect succeed, clientID: %d, remote: %s",
		sc.clientID, sc.netconn.RemoteAddr())
	return nil
}

func (sc *SendConn) writePkt() {
	err := error(nil)

	for !sc.fsm.InStates(
		CLOSED,
		FINI) {

		for {
			select {
			case pkt, ok := <-sc.writeCh:
				if !ok {
					return
				}
				ie := sc.handlePkt(pkt, iodefine.OUT)
				switch ie {
				case iodefine.IOSuccess:
					continue
				case iodefine.IOClosed:
					sc.log.Infof("handle packet return closed, clientID: %d, PacketID:%d",
						sc.clientID, pkt.ID())
					goto CLOSED
				case iodefine.IOErr:
					sc.log.Infof("handle packet return err, clientID: %d, PacketID:%d",
						sc.clientID, pkt.ID())
					goto CLOSED
				}
				continue

			case pkt, ok := <-sc.writeFromUpCh:
				if !ok {
					sc.log.Infof("write from up EOF, clientID: %d",
						sc.clientID)
					continue
				}
				sc.log.Tracef("to write down, PacketID: %d, clientID: %d, packetType: %s",
					pkt.ID(), sc.clientID, pkt.Type().String())
				err = packet.EncodeToWriter(pkt, sc.netconn)
				if err != nil {
					if err == io.EOF {
						// eof means no need for graceful close
						sc.fsm.EmitEvent(ET_EOF)
						sc.log.Errorf("conn write down EOF, clientID: %d, PacketID: %d",
							sc.clientID, pkt.ID())
					} else {
						sc.fsm.EmitEvent(ET_ERROR)
						sc.log.Errorf("conn write down err: %s, clientID: %d, PacketID: %d",
							err, sc.clientID, pkt.ID())
					}
					goto CLOSED
				}
				continue
			}
		}
	}

CLOSED:
	if sc.dlgt != nil && sc.clientID != 0 {
		sc.dlgt.Offline(sc.clientID, sc.meta, sc.RemoteAddr())
	}
	sc.fini()
}

func (sc *SendConn) readPkt() {

	for !sc.fsm.InStates(
		CLOSE_SENT,
		CLOSE_RECV,
		CLOSED,
		FINI) {

		for {
			pkt, err := packet.DecodeFromReader(sc.netconn)
			if err != nil {
				if err == io.EOF {
					sc.fsm.EmitEvent(ET_EOF)
					sc.log.Infof("conn read down EOF, clientID: %d", sc.clientID)
				} else if iodefine.ErrUseOfClosedNetwork(err) {
					sc.log.Infof("conn read down closed, clientID: %d", sc.clientID)
				} else {
					sc.log.Errorf("conn read down err: %s, clientID: %d", err, sc.clientID)
				}
				goto CLOSED
			}
			sc.log.Tracef("read %s, clientID: %d, PacketID: %d, packetType: %s",
				pkt.Type().String(), sc.clientID, pkt.ID(), pkt.Type().String())
			ie := sc.handlePkt(pkt, iodefine.IN)
			switch ie {
			case iodefine.IOSuccess:
				continue
			case iodefine.IOClosed:
				sc.log.Debugf("handle packet return closed, clientID: %d, PacketID:%d",
					sc.clientID, pkt.ID())
				goto CLOSED
			case iodefine.IOData:
				sc.connMtx.RLock()
				if !sc.connOK {
					sc.connMtx.RUnlock()
					goto CLOSED
				}
				sc.readToUpCh <- pkt
				sc.connMtx.RUnlock()
			case iodefine.IOExit:
				// no more reading, but the underlay conn is still using
				return
			case iodefine.IOErr:
				sc.fsm.EmitEvent(ET_ERROR)
				sc.log.Errorf("handle packet return err, clientID: %d, PacketID:%d",
					sc.clientID, pkt.ID())
				goto CLOSED
			}
		}
	}

CLOSED:
	if sc.dlgt != nil && sc.clientID != 0 {
		sc.dlgt.Offline(sc.clientID, sc.meta, sc.RemoteAddr())
	}
	sc.fini()
}

func (sc *SendConn) handlePkt(pkt packet.Packet, iotype iodefine.IOType) iodefine.IORet {
	err := error(nil)
	switch iotype {
	case iodefine.OUT:
		switch pkt.Type() {
		case packet.TypeConnPacket:
			// 发起连接
			err = sc.fsm.EmitEvent(ET_CONNSENT)
			if err != nil {

				sc.log.Errorf("emit ET_CONNSENT err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
					err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta), sc.fsm.State())

				sc.shub.Error(pkt.ID(), err)
				return iodefine.IOErr
			}
			err = packet.EncodeToWriter(pkt, sc.netconn)
			if err != nil {

				sc.log.Errorf("encode CONN to writer err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s",
					err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))

				sc.shub.Error(pkt.ID(), err)
				return iodefine.IOErr
			}
			sc.log.Debugf("send connsucceed, clientID: %d, PacketID: %d, packetType: %s",
				sc.clientID, pkt.ID(), pkt.Type().String())

			return iodefine.IOSuccess

		case packet.TypeDisConnPacket:
			// the close request from client
			err = sc.fsm.EmitEvent(ET_CLOSESENT)
			if err != nil {
				sc.log.Errorf("emit ET_CLOSESENT err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
					err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta), sc.fsm.State())
				return iodefine.IOErr
			}

			err = packet.EncodeToWriter(pkt, sc.netconn)
			if err != nil {
				sc.log.Errorf("encode DISCONN to writer err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s",
					err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))

				return iodefine.IOErr
			}
			sc.log.Debugf("send dis conn succeed, clientID: %d, PacketID: %d, packetType: %s",
				sc.clientID, pkt.ID(), pkt.Type().String())
			return iodefine.IOSuccess

		case packet.TypeDisConnAckPacket:
			// the close ack from client
			err = packet.EncodeToWriter(pkt, sc.netconn)
			if err != nil {
				sc.log.Errorf("encode DISCONNACK to writer err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s",
					err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
				return iodefine.IOErr
			}
			err = sc.fsm.EmitEvent(ET_CLOSEACK)
			if err != nil {
				sc.log.Errorf("emit out ET_CLOSEACK err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
					err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta), sc.fsm.State())
				return iodefine.IOErr
			}
			sc.log.Debugf("send dis conn ack succeed, clientID: %d, PacketID: %d, packetType: %s",
				sc.clientID, pkt.ID(), pkt.Type().String())
			if sc.fsm.State() == CLOSE_HALF {
				return iodefine.IOSuccess
			}
			return iodefine.IOClosed

		case packet.TypeHeartbeatPacket:
			err = packet.EncodeToWriter(pkt, sc.netconn)
			if err != nil {
				sc.log.Errorf("encode HEARTBEAT to writer err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s",
					err, sc.clientID, pkt.ID(), sc.RemoteAddr().String(), string(sc.meta))
				return iodefine.IOErr
			}
			sc.log.Debugf("send heartbeat succeed, clientID: %d, PacketID: %d, packetType: %s",
				sc.clientID, pkt.ID(), pkt.Type().String())
			return iodefine.IOSuccess
		}

	case iodefine.IN:
		switch realPkt := pkt.(type) {
		case *packet.ConnAckPacket:
			// the connection ack from server
			err = sc.fsm.EmitEvent(ET_CONNACK)
			if err != nil {
				sc.log.Errorf("emit ET_CONNACK err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
					err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta), sc.fsm.State())
				sc.shub.Error(realPkt.PacketID, err)
				return iodefine.IOErr
			}
			sc.clientID = realPkt.ClientID

			if realPkt.ConnData.Error != "" {
				err := errors.New(realPkt.ConnData.Error)
				sc.shub.Error(realPkt.PacketID, err)
				return iodefine.IOClosed
			}
			sc.shub.Ack(realPkt.PacketID, nil)
			sc.log.Debugf("recv conn ack succeed, clientID: %d, PacketID: %d, packetType: %s",
				sc.clientID, pkt.ID(), pkt.Type().String())
			return iodefine.IOSuccess

		case *packet.DisConnPacket:
			// the close request from server
			err = sc.fsm.EmitEvent(ET_CLOSERECV)
			if err != nil {
				sc.log.Errorf("emit ET_CLOSERECV err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
					err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta), sc.fsm.State())
				return iodefine.IOErr
			}
			retPkt := sc.pf.NewDisConnAckPacket(pkt.ID(), nil)
			sc.connMtx.RLock()
			if !sc.connOK {
				sc.connMtx.RUnlock()
				return iodefine.IOErr
			}
			sc.writeCh <- retPkt
			sc.connMtx.RUnlock()
			sc.log.Debugf("recv dis conn succeed, clientID: %d, PacketID: %d, packetType: %s",
				sc.clientID, pkt.ID(), pkt.Type().String())
			sc.Close()
			return iodefine.IOSuccess

		case *packet.DisConnAckPacket:
			// 服务端确认关闭连接
			err = sc.fsm.EmitEvent(ET_CLOSEACK)
			if err != nil {
				sc.log.Errorf("emit in ET_CLOSEACK err: %s, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
					err, sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta), sc.fsm.State())
				return iodefine.IOErr
			}
			sc.log.Debugf("recv dis conn ack succeed, clientID: %d, PacketID: %d, remote: %s, meta: %s",
				sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
			if sc.fsm.State() == CLOSE_HALF {
				return iodefine.IOSuccess
			}
			return iodefine.IOClosed

		case *packet.HeartbeatAckPacket:
			// heart
			ok := sc.fsm.InStates(CONNED)
			if !ok {
				sc.log.Errorf("heartbeat at non-CONNED state, clientID: %d, PacketID: %d, remote: %s, meta: %s, state: %s",
					sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta), sc.fsm.State())
				return iodefine.IOErr
			}
			return iodefine.IOSuccess

		default:
			return iodefine.IOData
		}
	}
	sc.log.Debugf("BUG! should not be here, clientID: %d, PacketID: %d, remote: %s, meta: %s",
		sc.clientID, pkt.ID(), sc.netconn.RemoteAddr(), string(sc.meta))
	return iodefine.IOErr
}

func (sc *SendConn) sendHeartbeat(event *timer.Event) {
	sc.connMtx.RLock()
	if !sc.connOK {
		sc.connMtx.RUnlock()
		return
	}
	pkt := sc.pf.NewHeartbeatPacket()
	sc.writeCh <- pkt
	sc.connMtx.RUnlock()
}

func (sc *SendConn) Close() {
	sc.onceClose.Do(func() {
		sc.log.Debugf("client is closing, clientID: %d, remote: %s, meta: %s",
			sc.clientID, sc.netconn.RemoteAddr(), string(sc.meta))

		sc.connMtx.RLock()
		if !sc.connOK {
			sc.connMtx.RUnlock()
			return
		}
		pkt := sc.pf.NewDisConnPacket()
		sc.writeCh <- pkt
		sc.connMtx.RUnlock()
	})
}

func (sc *SendConn) fini() {
	sc.onceFini.Do(func() {
		remote := "unknown"
		if sc.netconn != nil {
			remote = sc.netconn.RemoteAddr().String()
		}
		sc.log.Debugf("client finished, clientID: %d, remote: %s, meta: %s",
			sc.clientID, remote, string(sc.meta))

		sc.connMtx.Lock()
		sc.connOK = false
		sc.hbTick.Cancel()
		if sc.netconn != nil {
			sc.netconn.Close()
		}
		sc.shub.Close()
		close(sc.writeCh)
		close(sc.writeFromUpCh)
		close(sc.readToUpCh)
		sc.connMtx.Unlock()
		if !sc.tmrOutside {
			sc.tmr.Close()
		}

		sc.fsm.EmitEvent(ET_FINI)
		sc.fsm.Close()
	})
}
