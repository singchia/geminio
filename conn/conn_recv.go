package conn

import (
	"io"
	"net"
	"sync"

	"github.com/singchia/go-timer"
	"github.com/singchia/yafsm"
	"github.com/sirupsen/logrus"
	"gitlab.moresec.cn/moresec/ms_gw/alf/packet"
	"gitlab.moresec.cn/moresec/ms_gw/alf/pkg/id"
	"gitlab.moresec.cn/moresec/ms_gw/alf/pkg/iodefine"
	"gitlab.moresec.cn/moresec/ms_gw/alf/pkg/log"
	"gitlab.moresec.cn/moresec/ms_gw/alf/pkg/synchub"
)

type RecvConn struct {
	*BaseConn
	writeCh chan packet.Packet

	once        *sync.Once
	offlineOnce *sync.Once
	clientIDs   id.IDFactory // global ids
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
	if !rc.tmrOutside {
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
	logrusLog, yes := rc.log.(logrus.FieldLogger)
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
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).
						Info("write from up EOF")
				} else {
					rc.log.Infof("write from up EOF, clientId: %d", rc.ClientId)
				}
				// return
				continue
			}
			if yes {
				logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
					WithField("packetType", pkt.Type().String()).
					Trace("to write down")
			} else {
				rc.log.Tracef("to write down, clientId: %d, packetId: %d, packetType: %s",
					rc.ClientId, pkt.Id(), pkt.Type().String())
			}
			err = packet.EncodeToWriter(pkt, rc.netconn)
			if err != nil {
				if err == io.EOF {
					// eof means no need for graceful close
					rc.fsm.EmitEvent(ET_EOF)
					if yes {
						logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
							Info("conn write down EOF")
					} else {
						rc.log.Infof("conn write down EOF, clientId: %d, packetId: %d",
							rc.ClientId, pkt.Id())
					}
				} else {
					rc.fsm.EmitEvent(ET_ERROR)
					if yes {
						logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
							Error("conn write down err: %s", err)
					} else {
						rc.log.Errorf("conn write down err: %s, clientId: %d, packetId: %d",
							err, rc.ClientId, pkt.Id())
					}
				}
			}
			continue
		}
	}
CLOSED:
	if rc.dlgt != nil && rc.ClientId != 0 {
		rc.offlineOnce.Do(func() { rc.dlgt.Offline(rc.ClientId, rc.Meta, rc.RemoteAddr()) })
	}
	rc.fini()
}

func (rc *RecvConn) readPkt() {
	logrusLog, yes := rc.log.(logrus.FieldLogger)
	for {
		pkt, err := packet.DecodeFromReader(rc.netconn)
		if err != nil {
			if err == io.EOF {
				rc.fsm.EmitEvent(ET_EOF)
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).
						WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
						Debug("conn read down EOF")
				} else {
					rc.log.Debugf("conn read down EOF, clientId: %d, remote: %s, meta: %s",
						rc.ClientId, rc.netconn.RemoteAddr(), string(rc.Meta))
				}
			} else {
				rc.fsm.EmitEvent(ET_ERROR)
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).
						Error("conn read down err", err)
				} else {
					rc.log.Errorf("conn read down err, clientId: %d",
						rc.ClientId)
				}
			}
			goto CLOSED
		}
		if yes {
			logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
				Tracef("read %s", pkt.Type().String())
		} else {
			rc.log.Tracef("read %s , clientId: %d, packetId: %d, packetType: %s",
				pkt.Type().String(), rc.ClientId, pkt.Id(), pkt.Type().String())
		}
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
	if rc.dlgt != nil && rc.ClientId != 0 {
		rc.offlineOnce.Do(func() { rc.dlgt.Offline(rc.ClientId, rc.Meta, rc.RemoteAddr()) })
	}
	rc.fini()
}

func (rc *RecvConn) handlePkt(pkt packet.Packet, iotype iodefine.IOType) iodefine.IORet {
	logrusLog, yes := rc.log.(logrus.FieldLogger)
	err := error(nil)

	switch iotype {
	case iodefine.OUT:
		switch pkt.Type() {
		case packet.TypeDisConnPacket:
			err = rc.fsm.EmitEvent(ET_CLOSESENT)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
						WithField("state", rc.fsm.State()).Errorf("emit ET_CLOSESENT err: %s", err)
				} else {
					rc.log.Errorf("emit ET_CLOSESENT err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s, state: %s",
						err, rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta), rc.fsm.State())
				}
				return iodefine.IOErr
			}
			err = packet.EncodeToWriter(pkt, rc.netconn)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
						Error("encode DISCONN to writer err", err)
				} else {
					rc.log.Errorf("encode DISCONN to writer err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s",
						err, rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta))
				}
				return iodefine.IOErr
			}
			if yes {
				logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
					WithField("state", rc.fsm.State()).Debug("send dis conn succeed")
			} else {
				rc.log.Debugf("send dis conn succeed, clientId: %d, packetId: %d",
					rc.ClientId, pkt.Id())
			}
			return iodefine.IOSuccess

		case packet.TypeDisConnAckPacket:
			err = rc.fsm.EmitEvent(ET_CLOSEACK)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
						WithField("state", rc.fsm.State()).Error("emit ET_CLOSEACK err", err)
				} else {
					rc.log.Errorf("emit ET_CLOSEACK err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s, state: %s",
						err, rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta), rc.fsm.State())
				}
				return iodefine.IOErr
			}
			err = packet.EncodeToWriter(pkt, rc.netconn)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
						Error("encode DISCONNACK to writer err", err)
				} else {
					rc.log.Errorf("encode DISCONNACK to writer err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s",
						err, rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta))
				}
				return iodefine.IOErr
			}
			if yes {
				logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
					WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
					Debug("send dis conn ack succeed")
			} else {
				rc.log.Debugf("send dis conn ack succeed, clientId: %d, packetId: %d, remote: %s, meta: %s",
					rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta))
			}
			return iodefine.IOSuccess

		default:
			if yes {
				logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
					Error("unknown outgoing packet")
			} else {
				rc.log.Error("unknown outgoing packet, clientId: %d, packetId: %d",
					rc.ClientId, pkt.Id())
			}
		}

	case iodefine.IN:
		switch realPkt := pkt.(type) {
		case *packet.ConnPacket:
			if yes {
				logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
					WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(realPkt.ConnData.Meta)).
					Debug("read conn packet")
			} else {
				rc.log.Debugf("read conn succeed, clientId: %d, packetId: %d, remote: %s, meta: %s",
					rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(realPkt.ConnData.Meta))
			}

			err = rc.fsm.EmitEvent(ET_CONNRECV)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(realPkt.ConnData.Meta)).
						WithField("state", rc.fsm.State()).Errorf("emit ET_CONNRECV err: %s", err)
				} else {
					rc.log.Errorf("emit ET_CONNRECV err: %s, clientId: %d, packetId: %d, state: %s",
						err, rc.ClientId, pkt.Id(), rc.fsm.State())
				}
				rc.shub.Error(rc.getSyncID(), err)
				return iodefine.IOErr
			}

			rc.Meta = realPkt.ConnData.Meta
			//clientId := realPkt.ClientId
			if realPkt.ClientId == 0 {
				if rc.dlgt != nil {
					rc.ClientId, err = rc.dlgt.GetClientIdByMeta(rc.Meta)
				} else {
					rc.ClientId, err = rc.clientIDs.GetIDByMeta(rc.Meta)
				}
				if err != nil {
					if yes {
						logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
							WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
							Errorf("get id err: %s", err)
					} else {
						rc.log.Errorf("get id err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s",
							err, rc.ClientId, pkt.Id(), string(rc.Meta))
					}

					retPkt := rc.pf.NewConnAckPacket(realPkt.PacketId, rc.ClientId, err)
					err = packet.EncodeToWriter(retPkt, rc.netconn)
					if err != nil {
						if yes {
							logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
								WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
								Errorf("encode CONNERRACK to writer err: %s", err)
						} else {
							rc.log.Errorf("encode CONNERRACK to writer err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s",
								err, rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr())
						}
					}
					rc.shub.Error(rc.getSyncID(), err)
					return iodefine.IOErr
				}
			}

			if rc.dlgt != nil {
				err = rc.dlgt.Online(rc.ClientId, rc.Meta, rc.RemoteAddr())
				if err != nil {
					if yes {
						logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
							WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
							Errorf("online err: %s", err)
					} else {
						rc.log.Errorf("online err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s",
							err, rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta))
					}

					retPkt := rc.pf.NewConnAckPacket(realPkt.PacketId, rc.ClientId, err)
					err = packet.EncodeToWriter(retPkt, rc.netconn)
					if err != nil {
						if yes {
							logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
								WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
								Errorf("encode CONNERRACK to writer err: %s", err)
						} else {
							rc.log.Errorf("encode CONNERRACK to writer err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s",
								err, rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta))
						}
					}
					rc.shub.Error(rc.getSyncID(), err)
					// close the connection
					//return iodefine.IOErr
					return iodefine.IOSuccess
				}
			}
			rc.shub.Ack(rc.getSyncID(), nil)

			// 正常响应
			retPkt := rc.pf.NewConnAckPacket(realPkt.PacketId, rc.ClientId, nil)
			err = packet.EncodeToWriter(retPkt, rc.netconn)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
						Errorf("encode CONNACK to writer err: %s", err)
				} else {
					rc.log.Errorf("encode CONNACK to writer err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s",
						err, rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta))
				}
				return iodefine.IOErr
			}
			err = rc.fsm.EmitEvent(ET_CONNACK)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
						WithField("state", rc.fsm.State()).Errorf("emit ET_CONNACK err: %s", err)
				} else {
					rc.log.Errorf("emit ET_CONNACK err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s, state: %s",
						err, rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta), rc.fsm.State())
				}
				return iodefine.IOErr
			}

			// 心跳延长
			rc.Heartbeat = realPkt.Heartbeat
			rc.hbTick, err = rc.tmr.Time(
				uint64(rc.Heartbeat*2), struct{}{}, nil, rc.waitHBTimeout)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
						Errorf("set hb timer err: %s", err)
				} else {
					rc.log.Errorf("set hb timer err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s",
						err, rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta))
				}
				return iodefine.IOErr
			}

			if yes {
				logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
					WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
					Debug("send conn ack succeed")
			} else {
				rc.log.Debugf("send conn ack succeed, clientId: %d, packetId: %d, remote: %s, meta: %s",
					rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta))
			}
			return iodefine.IONew

		case *packet.DisConnPacket:
			if yes {
				logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
					WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
					Debug("read dis conn packet")
			} else {
				rc.log.Debugf("read dis conn succeed, clientId: %d, packetId: %d, remote: %s, meta: %s",
					rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta))
			}
			// 收到断开请求
			err = rc.fsm.EmitEvent(ET_CLOSERECV)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
						WithField("state", rc.fsm.State()).Errorf("emit ET_CLOSERECV err: %s", err)
				} else {
					rc.log.Errorf("emit ET_CLOSERECV err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s, state: %s",
						err, rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta), rc.fsm.State())
				}
				return iodefine.IOErr
			}
			// no more read
			retPkt := rc.pf.NewDisConnAckPacket(realPkt.PacketId, nil)
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
			if yes {
				logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
					WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
					Debug("read dis conn ack packet")
			} else {
				rc.log.Debugf("read dis conn ack packet, clientId: %d, packetId: %d, remote: %s, meta: %s",
					rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta))
			}
			// 确定断开请求，断开所有上下文
			err = rc.fsm.EmitEvent(ET_CLOSEACK)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
						WithField("state", rc.fsm.State()).Errorf("emit ET_CLOSERECV err: %s", err)
				} else {
					rc.log.Errorf("emit ET_CLOSERECV err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s, state: %s",
						err, rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta), rc.fsm.State())
				}
				return iodefine.IOErr
			}
			return iodefine.IOClosed

		case *packet.HeartbeatPacket:
			// 心跳
			ok := rc.fsm.InStates(CONNED)
			if !ok {
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
						WithField("state", rc.fsm.State()).Error("heartbeat at non-CONNED state")
				} else {
					rc.log.Errorf("heartbeat at non-CONNED state, clientId: %d, packetId: %d, remote: %s, meta: %s, state: %s",
						rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta), rc.fsm.State())
				}
				return iodefine.IOErr
			}
			// 重置心跳
			rc.hbTick.Cancel()
			rc.hbTick, err = rc.tmr.Time(
				uint64(rc.Heartbeat*2), struct{}{}, nil, rc.waitHBTimeout)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
						Error("set heartbeat timer err ", err)
				} else {
					rc.log.Errorf("set heartbeat timer err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s, state: %s",
						err, rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta), rc.fsm.State())
				}
			}

			retPkt := rc.pf.NewHeartbeatAckPacket(realPkt.PacketId)
			err = packet.EncodeToWriter(retPkt, rc.netconn)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
						Error("encode HEARTBEAT to writer err ", err)
				} else {
					rc.log.Errorf("encode HEARTBEAT to writer err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s",
						err, rc.ClientId, pkt.Id(), rc.RemoteAddr().String(), string(rc.Meta))
				}
				return iodefine.IOErr
			}
			if yes {
				logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
					WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
					Debug("send heartbeat ack succeed")
			} else {
				rc.log.Debugf("send heartbeat ack succeed, clientId: %d, packetId: %d, remote: %s, meta: %s",
					rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta))
			}
			if rc.dlgt != nil {
				rc.dlgt.Heartbeat(rc.ClientId, rc.Meta, rc.netconn.RemoteAddr())
			}
			return iodefine.IOSuccess

		default:
			ok := rc.fsm.InStates(CONNED)
			if !ok {
				if yes {
					logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
						Error("data at non CONNED")
				} else {
					rc.log.Debugf("data at non CONNED, clientId: %d, packetId: %d, remote: %s, meta: %s",
						rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta))
				}
				return iodefine.IOErr
			}
			return iodefine.IOData
		}
	}
	if yes {
		logrusLog.WithField("clientId", rc.ClientId).WithField("packetId", pkt.Id()).
			WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
			Debugf("BUG! should not be here, iotype: %v", iotype)
	} else {
		rc.log.Debugf("BUG! should not be here, clientId: %d, packetId: %d, remote: %s, meta: %s",
			rc.ClientId, pkt.Id(), rc.netconn.RemoteAddr(), string(rc.Meta))
	}
	return iodefine.IOErr
}

func (rc *RecvConn) init() error {
	rc.wg.Done() // 兼容抽象，在发送端需要先连接再进入loop，所以使用wg控制 TODO
	logrusLog, yes := rc.log.(logrus.FieldLogger)
	ch := rc.shub.Sync(rc.getSyncID(), nil, 10)
	unit := <-ch
	if unit.Error != nil {
		if yes {
			logrusLog.WithField("clientId", rc.ClientId).
				WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
				Error("wait conn timeout")
		} else {
			rc.log.Errorf("wait conn timeout, clientId: %d, remote: %s, meta: %s",
				rc.ClientId, rc.netconn.RemoteAddr(), string(rc.Meta))
		}
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
	logrusLog, yes := rc.log.(logrus.FieldLogger)
	if yes {
		logrusLog.WithField("clientId", rc.ClientId).
			WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
			Debug("client is closing")
	} else {
		rc.log.Debugf("client is closing, clientId: %d, remote: %s, meta: %s",
			rc.ClientId, rc.netconn.RemoteAddr(), string(rc.Meta))
	}

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
		logrusLog, yes := rc.log.(logrus.FieldLogger)
		if yes {
			logrusLog.WithField("clientId", rc.ClientId).
				WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
				Debug("client finished")
		} else {
			rc.log.Debugf("client finished, clientId: %d, remote: %s, meta: %s",
				rc.ClientId, rc.netconn.RemoteAddr(), string(rc.Meta))
		}

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
			rc.tmr.Stop()
		}

		rc.fsm.EmitEvent(ET_FINI)
		rc.fsm.Close()
	})
}

func (rc *RecvConn) waitHBTimeout(data interface{}) error {
	logrusLog, yes := rc.log.(logrus.FieldLogger)
	if yes {
		logrusLog.WithField("clientId", rc.ClientId).
			WithField("remote", rc.netconn.RemoteAddr()).WithField("meta", string(rc.Meta)).
			Errorf("wait HEARTBEAT timeout")
	} else {
		rc.log.Errorf("wait HEARTBEAT timeout, clientId: %d, remote: %s, meta: %s",
			rc.ClientId, rc.netconn.RemoteAddr(), string(rc.Meta))
	}
	rc.fini()
	return nil
}
