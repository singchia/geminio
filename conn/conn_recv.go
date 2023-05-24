package conn

import (
	"io"
	"net"
	"sync"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/geminio/pkg/iodefine"
	"github.com/singchia/geminio/pkg/synchub"
	"github.com/singchia/go-timer"
	"github.com/singchia/yafsm"
)

type RecvConn struct {
	*BaseConn
	writeCh chan packet.Packet

	once        *sync.Once
	offlineOnce *sync.Once
	clientIDs   id.IDFactory // global IDs
}

func NewRecvConn(netconn net.Conn, opts ...ConnOption) (*RecvConn, error) {
	err := error(nil)
	rc := &RecvConn{
		BaseConn: &BaseConn{
			ConnOpts: ConnOpts{
				WaitTimeout: 10,
			},
			fsm:     yafsm.NewFSM(),
			netconn: netconn,
			Side:    ServerSide,
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
		err = opt(rc.BaseConn)
		if err != nil {
			return nil, err
		}
	}
	rc.writeFromUpCh = make(chan packet.Packet)
	rc.readToUpCh = make(chan packet.Packet)
	// timer
	if !rc.tmrOutsIDe {
		rc.tmr = timer.NewTimer()
		rc.tmr.Start()
	}
	rc.shub = synchub.NewSyncHub(rc.tmr)

	if rc.pf == nil {
		rc.pf = packet.NewPacketFactory(id.NewIDCounter(id.Even))
	}
	if rc.log == nil {
		rc.log = log.DefaultLog
	}
	// states
	rc.initFSM()
	return rc, nil
}

func (rc *RecvConn) initFSM() {
	init := rc.fsm.AddState(INIT)
	connrecv := rc.fsm.AddState(CONN_RECV)
	conned := rc.fsm.AddState(CONNED)
	closesent := rc.fsm.AddState(CLOSE_SENT)
	closerecv := rc.fsm.AddState(CLOSE_RECV)
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
	rc.fsm.AddEvent(ET_EOF, closesent, closed)
	rc.fsm.AddEvent(ET_EOF, closerecv, closed)

	rc.fsm.AddEvent(ET_CLOSESENT, conned, closesent)
	rc.fsm.AddEvent(ET_CLOSERECV, conned, closerecv)
	rc.fsm.AddEvent(ET_CLOSEACK, closesent, closed)
	rc.fsm.AddEvent(ET_CLOSEACK, closerecv, closed)
	// fini
	rc.fsm.AddEvent(ET_FINI, init, fini)
	rc.fsm.AddEvent(ET_FINI, conned, fini)
	rc.fsm.AddEvent(ET_FINI, closesent, fini)
	rc.fsm.AddEvent(ET_FINI, closerecv, fini)
	rc.fsm.AddEvent(ET_FINI, closed, fini)
}

func (rc *RecvConn) writePkt() {
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
				rc.log.Infof("write from up EOF, clientID: %d", rc.ClientID)
				// return
				continue
			}

			rc.log.Tracef("to write down, clientID: %d, packetID: %d, packetType: %s",
				rc.ClientID, pkt.ID(), pkt.Type().String())
			err = packet.EncodeToWriter(pkt, rc.netconn)
			if err != nil {
				if err == io.EOF {
					// eof means no need for graceful close
					rc.fsm.EmitEvent(ET_EOF)
					rc.log.Infof("conn write down EOF, clientID: %d, packetID: %d",
						rc.ClientID, pkt.ID())
				}
			} else {
				rc.fsm.EmitEvent(ET_ERROR)
				rc.log.Errorf("conn write down err: %s, clientID: %d, packetID: %d",
					err, rc.ClientID, pkt.ID())
			}
			continue
		}
	}
CLOSED:
	if rc.dlgt != nil && rc.ClientID != 0 {
		rc.offlineOnce.Do(func() { rc.dlgt.Offline(rc.ClientID, rc.Meta, rc.RemoteAddr()) })
	}
	rc.fini()
}

func (rc *RecvConn) readPkt() {
	for {
		pkt, err := packet.DecodeFromReader(rc.netconn)
		if err != nil {
			if err == io.EOF {
				rc.fsm.EmitEvent(ET_EOF)
				rc.log.Debugf("conn read down EOF, clientID: %d, remote: %s, meta: %s",
					rc.ClientID, rc.netconn.RemoteAddr(), string(rc.Meta))
			} else {
				rc.fsm.EmitEvent(ET_ERROR)
				rc.log.Errorf("conn read down err, clientID: %d",
					rc.ClientID)
			}
			goto CLOSED
		}

		rc.log.Tracef("read %s , clientID: %d, packetID: %d, packetType: %s",
			pkt.Type().String(), rc.ClientID, pkt.ID(), pkt.Type().String())
		ie := rc.handlePkt(pkt, iodefine.IN)
		switch ie {
		case iodefine.IONew:
			continue

		case iodefine.IOSuccess:
			continue

		case iodefine.IOData:
			// TODO 性能待优化，使用RCU优化该场景
			rc.connMtx.RLock()
			if !rc.connOK {
				rc.connMtx.RUnlock()
				goto CLOSED
			}
			rc.readToUpCh <- pkt
			rc.connMtx.RUnlock()

		case iodefine.IOErr:
			// TODO 在遇到IOErr之后，还有必要发送Close吗，需要区分情况
			rc.fsm.EmitEvent(ET_ERROR)
			goto CLOSED
		case iodefine.IOClosed:
			// 无需优雅关闭，直接关闭连接
			goto CLOSED
		}
	}
CLOSED:
	if rc.dlgt != nil && rc.ClientID != 0 {
		rc.offlineOnce.Do(func() { rc.dlgt.Offline(rc.ClientID, rc.Meta, rc.RemoteAddr()) })
	}
	rc.fini()
}

func (rc *RecvConn) handlePkt(pkt packet.Packet, iotype iodefine.IOType) iodefine.IORet {
	err := error(nil)

	switch iotype {
	case iodefine.OUT:
		switch pkt.Type() {
		case packet.TypeDisConnPacket:
			err = rc.fsm.EmitEvent(ET_CLOSESENT)
			if err != nil {
				rc.log.Errorf("emit ET_CLOSESENT err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
					err, rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta), rc.fsm.State())
				return iodefine.IOErr
			}
			err = packet.EncodeToWriter(pkt, rc.netconn)
			if err != nil {
				rc.log.Errorf("encode DISCONN to writer err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
					err, rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta))
				return iodefine.IOErr
			}
			rc.log.Debugf("send dis conn succeed, clientID: %d, packetID: %d",
				rc.ClientID, pkt.ID())
			return iodefine.IOSuccess

		case packet.TypeDisConnAckPacket:
			err = rc.fsm.EmitEvent(ET_CLOSEACK)
			if err != nil {
				rc.log.Errorf("emit ET_CLOSEACK err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
					err, rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta), rc.fsm.State())
				return iodefine.IOErr
			}
			err = packet.EncodeToWriter(pkt, rc.netconn)
			if err != nil {
				rc.log.Errorf("encode DISCONNACK to writer err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
					err, rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta))
				return iodefine.IOErr
			}
			rc.log.Debugf("send dis conn ack succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
				rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta))
			return iodefine.IOSuccess

		default:
			rc.log.Error("unknown outgoing packet, clientID: %d, packetID: %d",
				rc.ClientID, pkt.ID())
		}

	case iodefine.IN:
		switch realPkt := pkt.(type) {
		case *packet.ConnPacket:
			rc.log.Debugf("read conn succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
				rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(realPkt.ConnData.Meta))

			err = rc.fsm.EmitEvent(ET_CONNRECV)
			if err != nil {
				rc.log.Errorf("emit ET_CONNRECV err: %s, clientID: %d, packetID: %d, state: %s",
					err, rc.ClientID, pkt.ID(), rc.fsm.State())
				rc.shub.Error(rc.getSyncID(), err)
				return iodefine.IOErr
			}

			rc.Meta = realPkt.ConnData.Meta
			//clientID := realPkt.ClientID
			if realPkt.ClientID == 0 {
				if rc.dlgt != nil {
					rc.ClientID, err = rc.dlgt.GetClientIDByMeta(rc.Meta)
				} else {
					rc.ClientID, err = rc.clientIDs.GetIDByMeta(rc.Meta)
				}
				if err != nil {
					rc.log.Errorf("get ID err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
						err, rc.ClientID, pkt.ID(), string(rc.Meta))

					retPkt := rc.pf.NewConnAckPacket(realPkt.PacketID, rc.ClientID, err)
					err = packet.EncodeToWriter(retPkt, rc.netconn)
					if err != nil {
						rc.log.Errorf("encode CONNERRACK to writer err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
							err, rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr())
					}
					rc.shub.Error(rc.getSyncID(), err)
					return iodefine.IOErr
				}
			}

			if rc.dlgt != nil {
				err = rc.dlgt.Online(rc.ClientID, rc.Meta, rc.RemoteAddr())
				if err != nil {
					rc.log.Errorf("online err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
						err, rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta))

					retPkt := rc.pf.NewConnAckPacket(realPkt.PacketID, rc.ClientID, err)
					err = packet.EncodeToWriter(retPkt, rc.netconn)
					if err != nil {
						rc.log.Errorf("encode CONNERRACK to writer err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
							err, rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta))
					}
					rc.shub.Error(rc.getSyncID(), err)
					// close the connection
					//return iodefine.IOErr
					return iodefine.IOSuccess
				}
			}
			rc.shub.Ack(rc.getSyncID(), nil)

			// 正常响应
			retPkt := rc.pf.NewConnAckPacket(realPkt.PacketID, rc.ClientID, nil)
			err = packet.EncodeToWriter(retPkt, rc.netconn)
			if err != nil {
				rc.log.Errorf("encode CONNACK to writer err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
					err, rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta))
				return iodefine.IOErr
			}
			err = rc.fsm.EmitEvent(ET_CONNACK)
			if err != nil {
				rc.log.Errorf("emit ET_CONNACK err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
					err, rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta), rc.fsm.State())
				return iodefine.IOErr
			}

			// 心跳延长
			rc.Heartbeat = realPkt.Heartbeat
			rc.hbTick, err = rc.tmr.Time(
				uint64(rc.Heartbeat*2), struct{}{}, nil, rc.waitHBTimeout)
			if err != nil {
				rc.log.Errorf("set hb timer err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
					err, rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta))
				return iodefine.IOErr
			}

			rc.log.Debugf("send conn ack succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
				rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta))
			return iodefine.IONew

		case *packet.DisConnPacket:
			rc.log.Debugf("read dis conn succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
				rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta))
			// 收到断开请求
			err = rc.fsm.EmitEvent(ET_CLOSERECV)
			if err != nil {
				rc.log.Errorf("emit ET_CLOSERECV err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
					err, rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta), rc.fsm.State())
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
			//return iodefine.IOClosed TODO 当前收到diconn后立即断开连接会让对端收不到dis conn ack的包
			return iodefine.IOSuccess

		case *packet.DisConnAckPacket:
			rc.log.Debugf("read dis conn ack packet, clientID: %d, packetID: %d, remote: %s, meta: %s",
				rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta))
			// 确定断开请求，断开所有上下文
			err = rc.fsm.EmitEvent(ET_CLOSEACK)
			if err != nil {
				rc.log.Errorf("emit ET_CLOSERECV err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
					err, rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta), rc.fsm.State())
				return iodefine.IOErr
			}
			return iodefine.IOClosed

		case *packet.HeartbeatPacket:
			// 心跳
			ok := rc.fsm.InStates(CONNED)
			if !ok {
				rc.log.Errorf("heartbeat at non-CONNED state, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
					rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta), rc.fsm.State())
				return iodefine.IOErr
			}
			// 重置心跳
			rc.hbTick.Cancel()
			rc.hbTick, err = rc.tmr.Time(
				uint64(rc.Heartbeat*2), struct{}{}, nil, rc.waitHBTimeout)
			if err != nil {
				rc.log.Errorf("set heartbeat timer err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s, state: %s",
					err, rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta), rc.fsm.State())
			}

			retPkt := rc.pf.NewHeartbeatAckPacket(realPkt.PacketID)
			err = packet.EncodeToWriter(retPkt, rc.netconn)
			if err != nil {
				rc.log.Errorf("encode HEARTBEAT to writer err: %s, clientID: %d, packetID: %d, remote: %s, meta: %s",
					err, rc.ClientID, pkt.ID(), rc.RemoteAddr().String(), string(rc.Meta))
				return iodefine.IOErr
			}
			rc.log.Debugf("send heartbeat ack succeed, clientID: %d, packetID: %d, remote: %s, meta: %s",
				rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta))
			if rc.dlgt != nil {
				rc.dlgt.Heartbeat(rc.ClientID, rc.Meta, rc.netconn.RemoteAddr())
			}
			return iodefine.IOSuccess

		default:
			ok := rc.fsm.InStates(CONNED)
			if !ok {
				rc.log.Debugf("data at non CONNED, clientID: %d, packetID: %d, remote: %s, meta: %s",
					rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta))
				return iodefine.IOErr
			}
			return iodefine.IOData
		}
	}
	rc.log.Debugf("BUG! should not be here, clientID: %d, packetID: %d, remote: %s, meta: %s",
		rc.ClientID, pkt.ID(), rc.netconn.RemoteAddr(), string(rc.Meta))
	return iodefine.IOErr
}

func (rc *RecvConn) init() error {
	rc.wg.Done() // 兼容抽象，在发送端需要先连接再进入loop，所以使用wg控制 TODO
	ch := rc.shub.Sync(rc.getSyncID(), nil, 10)
	unit := <-ch
	if unit.Error != nil {
		rc.log.Errorf("wait conn timeout, clientID: %d, remote: %s, meta: %s",
			rc.ClientID, rc.netconn.RemoteAddr(), string(rc.Meta))
		rc.fini()
		return unit.Error
	}
	return nil
}

func (rc *RecvConn) getSyncID() string {
	rc.connMtx.RLock()
	defer rc.connMtx.RUnlock()
	return rc.netconn.RemoteAddr().String() + rc.netconn.LocalAddr().String()
}

// TODO 优雅关闭
func (rc *RecvConn) Close() {
	rc.log.Debugf("client is closing, clientID: %d, remote: %s, meta: %s",
		rc.ClientID, rc.netconn.RemoteAddr(), string(rc.Meta))

	rc.connMtx.RLock()
	pkt := rc.pf.NewDisConnPacket()
	if !rc.connOK {
		rc.connMtx.RUnlock()
		return
	}
	rc.writeCh <- pkt
	rc.connMtx.RUnlock()
}

func (rc *RecvConn) close() {}

func (rc *RecvConn) closeWrapper(_ *yafsm.Event) {
	rc.Close()
}

// 回收资源
func (rc *RecvConn) fini() {
	rc.once.Do(func() {
		rc.log.Debugf("client finished, clientID: %d, remote: %s, meta: %s",
			rc.ClientID, rc.netconn.RemoteAddr(), string(rc.Meta))

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
		if !rc.tmrOutsIDe {
			rc.tmr.Stop()
		}

		rc.fsm.EmitEvent(ET_FINI)
		rc.fsm.Close()
	})
}

func (rc *RecvConn) waitHBTimeout(data interface{}) error {
	rc.log.Errorf("wait HEARTBEAT timeout, clientID: %d, remote: %s, meta: %s",
		rc.ClientID, rc.netconn.RemoteAddr(), string(rc.Meta))
	rc.fini()
	return nil
}
