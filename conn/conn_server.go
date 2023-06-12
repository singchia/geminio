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

type ServerConn struct {
	*baseConn
	writeCh chan packet.Packet
	dlgt    ServerConnDelegate

	once        *sync.Once
	offlineOnce *sync.Once
	clientIDs   id.IDFactory // global IDs
}

type ServerConnOption func(*ServerConn) error

func OptionServerConnPacketFactory(pf *packet.PacketFactory) ServerConnOption {
	return func(rc *ServerConn) error {
		rc.pf = pf
		return nil
	}
}

func OptionServerConnTimer(tmr timer.Timer) ServerConnOption {
	return func(rc *ServerConn) error {
		rc.tmr = tmr
		rc.tmrOutside = true
		return nil
	}
}

func OptionServerConnDelegate(dlgt ServerConnDelegate) ServerConnOption {
	return func(rc *ServerConn) error {
		rc.dlgt = dlgt
		return nil
	}
}

func OptionServerConnLogger(log log.Logger) ServerConnOption {
	return func(rc *ServerConn) error {
		rc.log = log
		return nil
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
		writeCh:     make(chan packet.Packet, 1024),
		once:        new(sync.Once),
		offlineOnce: new(sync.Once),
		clientIDs:   id.NewIDCounter(id.Unique),
	}
	rc.cn = rc
	// options
	for _, opt := range opts {
		err = opt(rc)
		if err != nil {
			return nil, err
		}
	}
	rc.writeFromUpCh = make(chan packet.Packet)
	rc.readToUpCh = make(chan packet.Packet)
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
	rc.connMtx.RLock()
	defer rc.connMtx.RUnlock()
	return rc.netconn.RemoteAddr().String() + rc.netconn.LocalAddr().String()
}

func (rc *ServerConn) initFSM() {
	init := rc.fsm.AddState(INIT)
	connrecv := rc.fsm.AddState(CONN_RECV)
	conned := rc.fsm.AddState(CONNED)
	closesent := rc.fsm.AddState(CLOSE_SENT)
	closerecv := rc.fsm.AddState(CLOSE_RECV)
	closehalf := rc.fsm.AddState(CLOSE_HALF)
	closed := rc.fsm.AddState(CLOSED)
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
	rc.fsm.AddEvent(ET_CLOSESENT, closehalf, closehalf) // been closed and we acked, then start to close

	rc.fsm.AddEvent(ET_CLOSERECV, conned, closerecv)
	rc.fsm.AddEvent(ET_CLOSERECV, closesent, closerecv) // close and been closed at same time
	rc.fsm.AddEvent(ET_CLOSERECV, closehalf, closehalf)

	rc.fsm.AddEvent(ET_CLOSEACK, closesent, closehalf)
	rc.fsm.AddEvent(ET_CLOSEACK, closerecv, closehalf)
	rc.fsm.AddEvent(ET_CLOSEACK, closehalf, closed) // close and been closed at same time
	// fini
	rc.fsm.AddEvent(ET_FINI, init, fini)
	rc.fsm.AddEvent(ET_FINI, connrecv, fini)
	rc.fsm.AddEvent(ET_FINI, conned, fini)
	rc.fsm.AddEvent(ET_FINI, closesent, fini)
	rc.fsm.AddEvent(ET_FINI, closerecv, fini)
	rc.fsm.AddEvent(ET_FINI, closehalf, fini)
	rc.fsm.AddEvent(ET_FINI, closed, fini)
}

func (rc *ServerConn) writePkt() {
	err := error(nil)

	for {
		select {
		case pkt, ok := <-rc.writeCh:
			if !ok {
				return
			}
			ie := rc.handlePkt(pkt, iodefine.OUT)
			switch ie {
			case iodefine.IOSuccess:
				continue
			case iodefine.IOErr:
				rc.fsm.EmitEvent(ET_ERROR)
				goto CLOSED
			case iodefine.IOClosed:
				goto CLOSED
			}
		case pkt, ok := <-rc.writeFromUpCh:
			if !ok {
				rc.log.Infof("write from up EOF, clientID: %d", rc.clientID)
				continue
			}

			rc.log.Tracef("to write down, clientID: %d, packetID: %d, packetType: %s",
				rc.clientID, pkt.ID(), pkt.Type().String())
			err = packet.EncodeToWriter(pkt, rc.netconn)
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
			}
			continue
		}
	}
CLOSED:
	if rc.dlgt != nil && rc.clientID != 0 {
		rc.offlineOnce.Do(func() { rc.dlgt.Offline(rc.clientID, rc.meta, rc.RemoteAddr()) })
	}
	rc.fini()
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
			goto CLOSED
		}

		rc.log.Tracef("read %s , clientID: %d, packetID: %d, packetType: %s",
			pkt.Type().String(), rc.clientID, pkt.ID(), pkt.Type().String())
		ie := rc.handlePkt(pkt, iodefine.IN)
		switch ie {
		case iodefine.IONew:
			continue
		case iodefine.IOSuccess:
			continue
		case iodefine.IOData:
			// TODO using RCU
			rc.readToUpCh <- pkt
		case iodefine.IOExit:
			// no more reading, but the underlay conn is still using
			return

		case iodefine.IOErr:
			rc.fsm.EmitEvent(ET_ERROR)
			goto CLOSED
		case iodefine.IOClosed:
			goto CLOSED
		}
	}
CLOSED:
	if rc.dlgt != nil && rc.clientID != 0 {
		rc.offlineOnce.Do(func() { rc.dlgt.Offline(rc.clientID, rc.meta, rc.RemoteAddr()) })
	}
	rc.fini()
}

func (rc *ServerConn) handlePkt(pkt packet.Packet, iotype iodefine.IOType) iodefine.IORet {
	err := error(nil)

	switch iotype {
	case iodefine.OUT:
		switch pkt.Type() {
		case packet.TypeDisConnPacket:
			err = rc.fsm.EmitEvent(ET_CLOSESENT)
			if err != nil {
				rc.log.Errorf("emit ET_CLOSESENT err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
					err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta), rc.fsm.State())
				return iodefine.IOErr
			}
			err = packet.EncodeToWriter(pkt, rc.netconn)
			if err != nil {
				rc.log.Errorf("encode DISCONN to writer err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
					err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
				return iodefine.IOErr
			}
			rc.log.Debugf("send dis conn succeed, clientID: %d, packetID: %d",
				rc.clientID, pkt.ID())
			// TODO no more writing
			return iodefine.IOSuccess

		case packet.TypeDisConnAckPacket:
			err = packet.EncodeToWriter(pkt, rc.netconn)
			if err != nil {
				rc.log.Errorf("encode DISCONNACK to writer err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
					err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
				return iodefine.IOErr
			}
			rc.log.Debugf("send dis conn ack succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
				rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
			err = rc.fsm.EmitEvent(ET_CLOSEACK)
			if err != nil {
				rc.log.Errorf("emit out ET_CLOSEACK err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
					err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta), rc.fsm.State())
				return iodefine.IOErr
			}
			if rc.fsm.State() == CLOSE_HALF {
				return iodefine.IOSuccess
			}
			return iodefine.IOClosed

		default:
			rc.log.Error("unknown outgoing packet, clientID: %d, packetID: %d",
				rc.clientID, pkt.ID())
		}

	case iodefine.IN:
		switch realPkt := pkt.(type) {
		case *packet.ConnPacket:
			rc.log.Debugf("read conn succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
				rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(realPkt.ConnData.Meta))

			err = rc.fsm.EmitEvent(ET_CONNRECV)
			if err != nil {
				rc.log.Errorf("emit ET_CONNRECV err: %s, clientID: %d, packetID: %d, state: %s",
					err, rc.clientID, pkt.ID(), rc.fsm.State())
				rc.shub.Error(rc.getSyncID(), err)
				return iodefine.IOErr
			}

			rc.meta = realPkt.ConnData.Meta
			if realPkt.ClientID == 0 {
				if rc.dlgt != nil {
					rc.clientID, err = rc.dlgt.GetClientIDByMeta(rc.meta)
				} else {
					rc.clientID, err = rc.clientIDs.GetIDByMeta(rc.meta)
				}
				if err != nil {
					rc.log.Errorf("get ID err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
						err, rc.clientID, pkt.ID(), string(rc.meta))

					retPkt := rc.pf.NewConnAckPacket(realPkt.PacketID, rc.clientID, err)
					err = packet.EncodeToWriter(retPkt, rc.netconn)
					if err != nil {
						rc.log.Errorf("encode CONNERRACK to writer err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
							err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr())
					}
					rc.shub.Error(rc.getSyncID(), err)
					return iodefine.IOErr
				}
			}

			if rc.dlgt != nil {
				err = rc.dlgt.Online(rc.clientID, rc.meta, rc.RemoteAddr())
				if err != nil {
					rc.log.Errorf("online err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
						err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))

					retPkt := rc.pf.NewConnAckPacket(realPkt.PacketID, rc.clientID, err)
					err = packet.EncodeToWriter(retPkt, rc.netconn)
					if err != nil {
						rc.log.Errorf("encode CONNERRACK to writer err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
							err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
					}
					rc.shub.Error(rc.getSyncID(), err)
					return iodefine.IOSuccess
				}
			}
			rc.shub.Ack(rc.getSyncID(), nil)

			// 正常响应
			retPkt := rc.pf.NewConnAckPacket(realPkt.PacketID, rc.clientID, nil)
			err = packet.EncodeToWriter(retPkt, rc.netconn)
			if err != nil {
				rc.log.Errorf("encode CONNACK to writer err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
					err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
				return iodefine.IOErr
			}
			err = rc.fsm.EmitEvent(ET_CONNACK)
			if err != nil {
				rc.log.Errorf("emit ET_CONNACK err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
					err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta), rc.fsm.State())
				return iodefine.IOErr
			}

			// 心跳延长
			rc.heartbeat = realPkt.Heartbeat
			rc.hbTick = rc.tmr.Add(time.Duration(rc.heartbeat)*2*time.Second, timer.WithHandler(rc.waitHBTimeout))

			rc.log.Debugf("send conn ack succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
				rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
			return iodefine.IONew

		case *packet.DisConnPacket:
			rc.log.Debugf("recv dis conn succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
				rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
			// 收到断开请求
			err = rc.fsm.EmitEvent(ET_CLOSERECV)
			if err != nil {
				rc.log.Errorf("emit ET_CLOSERECV err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
					err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta), rc.fsm.State())
				return iodefine.IOErr
			}
			// no more read
			retPkt := rc.pf.NewDisConnAckPacket(realPkt.PacketID, nil)
			rc.connMtx.RLock()
			if !rc.connOK {
				rc.connMtx.RUnlock()
				return iodefine.IOErr
			}
			rc.writeCh <- retPkt
			rc.connMtx.RUnlock()
			rc.Close()
			return iodefine.IOSuccess

		case *packet.DisConnAckPacket:
			rc.log.Debugf("read dis conn ack packet, clientID: %d, packetID: %d, remote: %s, meta: %s",
				rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
			err = rc.fsm.EmitEvent(ET_CLOSEACK)
			if err != nil {
				rc.log.Errorf("emit in ET_CLOSEACK err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
					err, rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta), rc.fsm.State())
				return iodefine.IOErr
			}
			if rc.fsm.State() == CLOSE_HALF {
				return iodefine.IOSuccess
			}
			return iodefine.IOClosed

		case *packet.HeartbeatPacket:
			// 心跳
			ok := rc.fsm.InStates(CONNED)
			if !ok {
				rc.log.Errorf("heartbeat at non-CONNED state, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
					rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta), rc.fsm.State())
				return iodefine.IOErr
			}
			// 重置心跳
			rc.hbTick.Cancel()
			rc.hbTick = rc.tmr.Add(time.Duration(rc.heartbeat)*2*time.Second, timer.WithHandler(rc.waitHBTimeout), timer.WithCyclically())

			retPkt := rc.pf.NewHeartbeatAckPacket(realPkt.PacketID)
			err = packet.EncodeToWriter(retPkt, rc.netconn)
			if err != nil {
				rc.log.Errorf("encode HEARTBEAT to writer err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
					err, rc.clientID, pkt.ID(), rc.RemoteAddr().String(), string(rc.meta))
				return iodefine.IOErr
			}
			rc.log.Debugf("send heartbeat ack succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
				rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
			if rc.dlgt != nil {
				rc.dlgt.Heartbeat(rc.clientID, rc.meta, rc.netconn.RemoteAddr())
			}
			return iodefine.IOSuccess

		default:
			ok := rc.fsm.InStates(CONNED)
			if !ok {
				rc.log.Debugf("data at non CONNED, clientID: %d, packetID: %d, remote: %s, meta: %s",
					rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
				return iodefine.IOErr
			}
			return iodefine.IOData
		}
	}
	rc.log.Debugf("BUG! should not be here, clientID: %d, packetID: %d, remote: %s, meta: %s",
		rc.clientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.meta))
	return iodefine.IOErr
}

// TODO 优雅关闭
func (rc *ServerConn) Close() {
	rc.log.Debugf("client is closing, clientID: %d, remote: %s, meta: %s",
		rc.clientID, rc.netconn.RemoteAddr(), string(rc.meta))

	rc.connMtx.RLock()
	pkt := rc.pf.NewDisConnPacket()
	if !rc.connOK {
		rc.connMtx.RUnlock()
		return
	}
	rc.writeCh <- pkt
	rc.connMtx.RUnlock()
}

func (rc *ServerConn) close() {}

func (rc *ServerConn) closeWrapper(_ *yafsm.Event) {
	rc.Close()
}

// 回收资源
func (rc *ServerConn) fini() {
	rc.once.Do(func() {
		rc.log.Debugf("client finished, clientID: %d, remote: %s, meta: %s",
			rc.clientID, rc.netconn.RemoteAddr(), string(rc.meta))

		rc.connMtx.Lock()
		rc.connOK = false
		if rc.hbTick != nil {
			rc.hbTick.Cancel()
		}
		rc.netconn.Close()
		rc.shub.Close()
		close(rc.writeCh)
		close(rc.writeFromUpCh)
		close(rc.readToUpCh)
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
