package conn

import (
	"errors"
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

type packetAndConnver struct {
	pkt        packet.Packet
	netconnVer uint32
}

type Dialer func() (net.Conn, error)

type SendConn struct {
	*BaseConn
	dialer  Dialer
	writeCh chan *packetAndConnver

	netconnVer uint32

	onceFini  *sync.Once
	onceClose *sync.Once
}

func NewSendConn(dialer Dialer, clientId uint64, meta []byte, opts ...ConnOption) (*SendConn, error) {
	err := error(nil)
	sc := &SendConn{
		dialer: dialer,
		BaseConn: &BaseConn{
			ConnOpts: ConnOpts{
				ClientId:  clientId,
				Heartbeat: packet.Heartbeat20,
				Meta:      meta,
			},
			fsm:  yafsm.NewFSM(),
			Side: ClientSide,
		},
		writeCh:   make(chan *packetAndConnver, 1024),
		onceFini:  new(sync.Once),
		onceClose: new(sync.Once),
	}
	sc.cn = sc
	// options
	for _, opt := range opts {
		err = opt(sc.BaseConn)
		if err != nil {
			return nil, err
		}
	}
	sc.writeFromUpCh = make(chan packet.Packet, 1024)
	sc.readToUpCh = make(chan packet.Packet, 1024)
	// timer
	if !sc.tmrOutside {
		sc.tmr = timer.NewTimer()
		sc.tmr.Start()
	}
	sc.shub = synchub.NewSyncHub(sc.tmr)

	if sc.pf == nil {
		sc.pf = packet.NewPacketFactory(id.NewIDCounter(id.Odd))
	}
	if sc.log == nil {
		sc.log = log.DefaultLog
	}
	// timer
	sc.hbTick, err = sc.tmr.Time(
		uint64(sc.Heartbeat), struct{}{}, nil, sc.heartbeat)
	if err != nil {
		return nil, err
	}
	// states
	sc.initFSM()
	return sc, nil
}

func (sc *SendConn) initFSM() {
	init := sc.fsm.AddState(INIT)
	connsent := sc.fsm.AddState(CONN_SENT)
	conned := sc.fsm.AddState(CONNED)
	abnormal := sc.fsm.AddState(ABNORMAL)
	closesent := sc.fsm.AddState(CLOSE_SENT)
	closerecv := sc.fsm.AddState(CLOSE_RECV)
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
	sc.fsm.AddEvent(ET_EOF, closesent, closed)
	sc.fsm.AddEvent(ET_EOF, closerecv, closed)

	sc.fsm.AddEvent(ET_CLOSESENT, conned, closesent)
	sc.fsm.AddEvent(ET_CLOSERECV, conned, closerecv)
	sc.fsm.AddEvent(ET_CLOSEACK, closesent, closed)
	sc.fsm.AddEvent(ET_CLOSEACK, closerecv, closed)
	// fini
	sc.fsm.AddEvent(ET_FINI, init, fini)
	sc.fsm.AddEvent(ET_FINI, connsent, fini)
	sc.fsm.AddEvent(ET_FINI, conned, fini)
	sc.fsm.AddEvent(ET_FINI, abnormal, fini)
	sc.fsm.AddEvent(ET_FINI, closesent, fini)
	sc.fsm.AddEvent(ET_FINI, closerecv, fini)
	sc.fsm.AddEvent(ET_FINI, closed, fini)
}

func (sc *SendConn) init() error {
	logrusLog, yes := sc.log.(logrus.FieldLogger)
	sc.connMtx.Lock()
	if sc.connOK {
		sc.connMtx.Unlock()
		sc.wg.Done()
		return nil
	}
	netconn, err := sc.dialer()
	if err != nil {
		if yes {
			logrusLog.Errorf("dial err: %s", err)
		} else {
			sc.log.Errorf("dial err: %s", err)
		}
		sc.connMtx.Unlock()
		sc.wg.Done()
		return err
	}
	sc.connOK = true
	sc.netconnVer++
	sc.netconn = netconn
	sc.connMtx.Unlock()
	sc.wg.Done()

	if yes {
		logrusLog.WithField("remote", netconn.RemoteAddr()).Debug("dial succeed")
	} else {
		sc.log.Debugf("dial succeed, remote: %s", netconn.RemoteAddr())
	}
	return sc.connect()
}

func (sc *SendConn) connect() error {
	pkt := sc.pf.NewConnPacket(sc.ClientId, sc.Heartbeat, sc.Meta)
	sc.connMtx.RLock()
	if !sc.connOK {
		sc.connMtx.RUnlock()
		return io.EOF
	}
	ver := sc.netconnVer
	sc.writeCh <- &packetAndConnver{pkt, ver}
	ch := sc.shub.Sync(pkt.PacketId, nil, 10)
	unit := <-ch
	sc.connMtx.RUnlock()

	logrusLog, yes := sc.log.(logrus.FieldLogger)
	if unit.Error != nil {
		if yes {
			logrusLog.WithField("clientId", sc.ClientId).WithField("remote", sc.netconn.RemoteAddr()).
				Error("connect err", unit.Error)
		} else {
			sc.log.Errorf("connect err: %s, clientId: %d, remote: %s",
				unit.Error, sc.ClientId, sc.netconn.RemoteAddr())
		}
		return unit.Error
	}
	if yes {
		logrusLog.WithField("clientId", sc.ClientId).WithField("remote", sc.netconn.RemoteAddr()).
			Debug("connect succeed")
	} else {
		sc.log.Debugf("connect succeed, clientId: %d, remote: %s",
			sc.ClientId, sc.netconn.RemoteAddr())
	}
	return nil
}

func (sc *SendConn) Close() {
	sc.onceClose.Do(func() {
		logrusLog, yes := sc.log.(logrus.FieldLogger)
		if yes {
			logrusLog.WithField("clientId", sc.ClientId).
				WithField("remote", sc.netconn.RemoteAddr()).WithField("meta", string(sc.Meta)).
				Debug("client is closing")
		} else {
			sc.log.Debugf("client is closing, clientId: %d, remote: %s, meta: %s",
				sc.ClientId, sc.netconn.RemoteAddr(), string(sc.Meta))
		}

		sc.connMtx.RLock()
		if !sc.connOK {
			sc.connMtx.RUnlock()
			return
		}
		pkt := sc.pf.NewDisConnPacket()
		ver := sc.netconnVer
		sc.writeCh <- &packetAndConnver{pkt, ver}
		sc.connMtx.RUnlock()
	})
}

func (sc *SendConn) fini() {
	sc.onceFini.Do(func() {
		remote := "unknown"
		if sc.netconn != nil {
			remote = sc.netconn.RemoteAddr().String()
		}
		logrusLog, yes := sc.log.(logrus.FieldLogger)
		if yes {
			logrusLog.WithField("clientId", sc.ClientId).
				WithField("remote", remote).WithField("meta", string(sc.Meta)).
				Debug("client finished")
		} else {
			sc.log.Debugf("client finished, clientId: %d, remote: %s, meta: %s",
				sc.ClientId, remote, string(sc.Meta))
		}

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
			sc.tmr.Stop()
		}

		sc.fsm.EmitEvent(ET_FINI)
		sc.fsm.Close()
	})
}

func (sc *SendConn) writePkt() {
	logrusLog, yes := sc.log.(logrus.FieldLogger)
	err := error(nil)

	for !sc.fsm.InStates(
		//CLOSE_SENT,
		//CLOSE_RECV,
		CLOSED,
		FINI) {

		sc.wg.Wait()

		if !sc.netOK() {
			goto CLOSED
		}
		for {
			select {
			case pkt, ok := <-sc.writeCh:
				if !ok {
					return
				}
				if !sc.validConnVer(pkt.netconnVer) {
					// drop the old packet
					if yes {
						logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.pkt.Id()).
							Error("invalid conn version: %s", pkt.netconnVer)
					} else {
						sc.log.Errorf("invalid conn version: %v",
							pkt.netconnVer)
					}
					continue
				}
				ie := sc.handlePkt(pkt.pkt, iodefine.OUT)
				switch ie {
				case iodefine.IOSuccess:
					continue
				case iodefine.IOReconnect:
					// TODO 重连
					goto CLOSED
				case iodefine.IOClosed:
					goto CLOSED
				case iodefine.IOErr:
					logrusLog, yes := sc.log.(logrus.FieldLogger)
					if yes {
						logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.pkt.Id()).
							Info("handle packet return err")
					} else {
						sc.log.Infof("handle packet return err, clientId: %d, packetId:%d",
							sc.ClientId, pkt.pkt.Id())
					}
					goto CLOSED
				}
				continue

			case pkt, ok := <-sc.writeFromUpCh:
				logrusLog, yes := sc.log.(logrus.FieldLogger)
				if !ok {
					if yes {
						logrusLog.WithField("clientId", sc.ClientId).
							Info("write from up EOF")
					} else {
						sc.log.Infof("write from up EOF, clientId: %d",
							sc.ClientId)
					}
					//return
					continue
				}
				if yes {
					logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
						WithField("packetType", pkt.Type().String()).
						Trace("to write down")
				} else {
					sc.log.Tracef("to write down, packetId: %d, clientId: %d, packetType: %s",
						pkt.Id(), sc.ClientId, pkt.Type().String())
				}
				err = packet.EncodeToWriter(pkt, sc.netconn)
				if err != nil {
					if err == io.EOF {
						// eof means no need for graceful close
						sc.fsm.EmitEvent(ET_EOF)
						if yes {
							logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
								WithField("packetType", pkt.Type().String()).
								Error("conn write down EOF")
						} else {
							sc.log.Errorf("conn write down EOF, clientId: %d, packetId: %d",
								sc.ClientId, pkt.Id())
						}
					} else {
						sc.fsm.EmitEvent(ET_ERROR)
						if yes {
							logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
								WithField("packetType", pkt.Type().String()).
								Error("conn write down err", err)
						} else {
							sc.log.Errorf("conn write down err: %s, clientId: %d, packetId: %d",
								err, sc.ClientId, pkt.Id())
						}
					}
					goto CLOSED
				}
				continue
			}
		}
	}

CLOSED:
	if sc.dlgt != nil && sc.ClientId != 0 {
		sc.dlgt.Offline(sc.ClientId, sc.Meta, sc.RemoteAddr())
	}
	sc.fini()
}

func (sc *SendConn) readPkt() {
	logrusLog, yes := sc.log.(logrus.FieldLogger)

	for !sc.fsm.InStates(
		CLOSE_SENT,
		CLOSE_RECV,
		CLOSED,
		FINI) {

		sc.wg.Wait()

		if !sc.netOK() {
			goto CLOSED
		}
		for {
			pkt, err := packet.DecodeFromReader(sc.netconn)
			if err != nil {
				if err == io.EOF {
					sc.fsm.EmitEvent(ET_EOF)
					if yes {
						logrusLog.WithField("clientId", sc.ClientId).
							Info("conn read down EOF")
					} else {
						sc.log.Infof("conn read down EOF, clientId: %d", sc.ClientId)
					}
				} else {
					if yes {
						logrusLog.WithField("clientId", sc.ClientId).
							Error("conn read down err", err)
					} else {
						sc.log.Errorf("conn read down err: %s, clientId: %d", err, sc.ClientId)
					}
				}
				goto CLOSED
			}
			logrusLog, yes := sc.log.(logrus.FieldLogger)
			if yes {
				logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
					WithField("packetType", pkt.Type().String()).
					Tracef("read %s", pkt.Type().String())
			} else {
				sc.log.Tracef("read %s , clientId: %d, packetId: %d, packetType: %s",
					pkt.Type().String(), sc.ClientId, pkt.Id(), pkt.Type().String())
			}
			ie := sc.handlePkt(pkt, iodefine.IN)
			switch ie {
			case iodefine.IOSuccess:
				continue
			case iodefine.IOReconnect:
				// TODO 在遇到IOErr之后，还有必要发送Close吗，需要区分情况
				sc.fsm.EmitEvent(ET_ERROR)
				// TODO 重连
				goto CLOSED
			case iodefine.IOClosed:
				// 无需优雅关闭，直接关闭连接
				goto CLOSED
			case iodefine.IOData:
				sc.connMtx.RLock()
				if !sc.connOK {
					sc.connMtx.RUnlock()
					goto CLOSED
				}
				sc.readToUpCh <- pkt
				sc.connMtx.RUnlock()

			case iodefine.IOErr:
				// TODO 在遇到IOErr之后，还有必要发送Close吗，需要区分情况
				sc.fsm.EmitEvent(ET_ERROR)
				logrusLog, yes := sc.log.(logrus.FieldLogger)
				if yes {
					logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
						Error("handle packet return err")
				} else {
					sc.log.Errorf("handle packet return err, clientId: %d, packetId:%d",
						sc.ClientId, pkt.Id())
				}
				goto CLOSED
			}
		}
	}

CLOSED:
	if sc.dlgt != nil && sc.ClientId != 0 {
		sc.dlgt.Offline(sc.ClientId, sc.Meta, sc.RemoteAddr())
	}
	sc.fini()
}

func (sc *SendConn) validConnVer(connver uint32) bool {
	sc.connMtx.RLock()
	defer sc.connMtx.RUnlock()

	if connver == sc.netconnVer {
		return true
	}
	return false
}

func (sc *SendConn) netOK() bool {
	sc.connMtx.RLock()
	defer sc.connMtx.RUnlock()
	return sc.connOK
}

func (sc *SendConn) netErr() {
	sc.connMtx.Lock()
	defer sc.connMtx.Unlock()
	sc.connOK = false
	if sc.netconn != nil {
		sc.netconn.Close()
	}
}

// TODO 做更细节的状态管理，思考chaos边缘场景（并发、延迟、宕机）
func (sc *SendConn) handlePkt(pkt packet.Packet, iotype iodefine.IOType) iodefine.IORet {
	logrusLog, yes := sc.log.(logrus.FieldLogger)
	err := error(nil)
	switch iotype {
	case iodefine.OUT:
		switch pkt.Type() {
		case packet.TypeConnPacket:
			// 发起连接
			err = sc.fsm.EmitEvent(ET_CONNSENT)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", sc.netconn.RemoteAddr()).WithField("meta", string(sc.Meta)).
						WithField("state", sc.fsm.State()).Error("emit ET_CONNSENT err", err)
				} else {
					sc.log.Errorf("emit ET_CONNSENT err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s, state: %s",
						err, sc.ClientId, pkt.Id(), sc.netconn.RemoteAddr(), string(sc.Meta), sc.fsm.State())
				}
				sc.shub.Error(pkt.Id(), err)
				return iodefine.IOReconnect
			}
			err = packet.EncodeToWriter(pkt, sc.netconn)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", sc.netconn.RemoteAddr()).WithField("meta", string(sc.Meta)).
						Error("encode CONN to writer err", err)
				} else {
					sc.log.Errorf("encode CONN to writer err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s",
						err, sc.ClientId, pkt.Id(), sc.netconn.RemoteAddr(), string(sc.Meta))
				}
				sc.shub.Error(pkt.Id(), err)
				return iodefine.IOReconnect
			}
			logrusLog, yes := sc.log.(logrus.FieldLogger)
			if yes {
				logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
					WithField("packetType", pkt.Type().String()).
					Debug("send conn succeed")
			} else {
				sc.log.Debugf("send connsucceed, clientId: %d, packetId: %d, packetType: %s",
					sc.ClientId, pkt.Id(), pkt.Type().String())
			}
			return iodefine.IOSuccess

		case packet.TypeDisConnPacket:
			// 客户端主动关闭连接
			err = sc.fsm.EmitEvent(ET_CLOSESENT)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", sc.netconn.RemoteAddr()).WithField("meta", string(sc.Meta)).
						WithField("state", sc.fsm.State()).Error("emit ET_CLOSESENT err", err)
				} else {
					sc.log.Errorf("emit ET_CLOSESENT err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s, state: %s",
						err, sc.ClientId, pkt.Id(), sc.netconn.RemoteAddr(), string(sc.Meta), sc.fsm.State())
				}
				return iodefine.IOErr
			}
			err = packet.EncodeToWriter(pkt, sc.netconn)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", sc.netconn.RemoteAddr()).WithField("meta", string(sc.Meta)).
						Error("encode DISCONN to writer err", err)
				} else {
					sc.log.Errorf("encode DISCONN to writer err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s",
						err, sc.ClientId, pkt.Id(), sc.netconn.RemoteAddr(), string(sc.Meta))
				}
				return iodefine.IOErr
			}
			if yes {
				logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
					WithField("packetType", pkt.Type().String()).
					Debug("send dis conn succeed")
			} else {
				sc.log.Debugf("send dis conn succeed, clientId: %d, packetId: %d, packetType: %s",
					sc.ClientId, pkt.Id(), pkt.Type().String())
			}
			//return iodefine.IOClosed
			return iodefine.IOSuccess

		case packet.TypeDisConnAckPacket:
			// 客户端回复关闭连接
			err = sc.fsm.EmitEvent(ET_CLOSEACK)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", sc.netconn.RemoteAddr()).WithField("meta", string(sc.Meta)).
						WithField("state", sc.fsm.State()).Error("emit ET_CLOSEACK err", err)
				} else {
					sc.log.Errorf("emit ET_CLOSEACK err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s, state: %s",
						err, sc.ClientId, pkt.Id(), sc.netconn.RemoteAddr(), string(sc.Meta), sc.fsm.State())
				}
				return iodefine.IOErr
			}

			err = packet.EncodeToWriter(pkt, sc.netconn)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", sc.netconn.RemoteAddr()).WithField("meta", string(sc.Meta)).
						Error("encode DISCONNACK to writer err", err)
				} else {
					sc.log.Errorf("encode DISCONNACK to writer err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s",
						err, sc.ClientId, pkt.Id(), sc.netconn.RemoteAddr(), string(sc.Meta))
				}
				return iodefine.IOErr
			}
			if yes {
				logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
					WithField("packetType", pkt.Type().String()).
					Debug("send dis conn ack succeed")
			} else {
				sc.log.Debugf("send dis conn ack succeed, clientId: %d, packetId: %d, packetType: %s",
					sc.ClientId, pkt.Id(), pkt.Type().String())
			}
			//return iodefine.IOClosed
			return iodefine.IOSuccess

		case packet.TypeHeartbeatPacket:
			err = packet.EncodeToWriter(pkt, sc.netconn)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", sc.netconn.RemoteAddr()).WithField("meta", string(sc.Meta)).
						Error("encode HEARTBEAT to writer err", err)
				} else {
					sc.log.Errorf("encode HEARTBEAT to writer err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s",
						err, sc.ClientId, pkt.Id(), sc.RemoteAddr().String(), string(sc.Meta))
				}
				return iodefine.IOReconnect
			}
			if yes {
				logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
					WithField("packetType", pkt.Type().String()).
					Debug("send heartbeat succeed")
			} else {
				sc.log.Debugf("send heartbeat succeed, clientId: %d, packetId: %d, packetType: %s",
					sc.ClientId, pkt.Id(), pkt.Type().String())
			}
			return iodefine.IOSuccess
		}

	case iodefine.IN:
		switch realPkt := pkt.(type) {
		case *packet.ConnAckPacket:
			// 服务端回复连接
			err = sc.fsm.EmitEvent(ET_CONNACK)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", sc.netconn.RemoteAddr()).WithField("meta", string(sc.Meta)).
						WithField("state", sc.fsm.State()).Error("emit ET_CONNACK err", err)
				} else {
					sc.log.Errorf("emit ET_CONNACK err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s, state: %s",
						err, sc.ClientId, pkt.Id(), sc.netconn.RemoteAddr(), string(sc.Meta), sc.fsm.State())
				}
				sc.shub.Error(realPkt.PacketId, err)
				return iodefine.IOReconnect
			}
			sc.ClientId = realPkt.ClientId

			if realPkt.ConnData.Error != "" {
				err := errors.New(realPkt.ConnData.Error)
				sc.shub.Error(realPkt.PacketId, err)
				return iodefine.IOClosed
			}
			sc.shub.Ack(realPkt.PacketId, nil)
			return iodefine.IOSuccess

		case *packet.DisConnPacket:
			// 服务端主动关闭连接
			err = sc.fsm.EmitEvent(ET_CLOSERECV)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", sc.netconn.RemoteAddr()).WithField("meta", string(sc.Meta)).
						WithField("state", sc.fsm.State()).Error("emit ET_DISCONN err", err)
				} else {
					sc.log.Errorf("emit ET_DISCONN err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s, state: %s",
						err, sc.ClientId, pkt.Id(), sc.netconn.RemoteAddr(), string(sc.Meta), sc.fsm.State())
				}
				return iodefine.IOErr
			}
			retPkt := sc.pf.NewDisConnAckPacket(pkt.Id(), nil)
			sc.connMtx.RLock()
			if !sc.connOK {
				sc.connMtx.RUnlock()
				return iodefine.IOErr
			}
			ver := sc.netconnVer
			sc.writeCh <- &packetAndConnver{retPkt, ver}
			sc.connMtx.RUnlock()
			//return iodefine.IOClosed
			return iodefine.IOSuccess

		case *packet.DisConnAckPacket:
			// 服务端确认关闭连接
			err = sc.fsm.EmitEvent(ET_CLOSEACK)
			if err != nil {
				if yes {
					logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", sc.netconn.RemoteAddr()).WithField("meta", string(sc.Meta)).
						WithField("state", sc.fsm.State()).Error("emit ET_CLOSEACK err", err)
				} else {
					sc.log.Errorf("emit ET_CLOSEACK err: %s, clientId: %d, packetId: %d, remote: %s, meta: %s, state: %s",
						err, sc.ClientId, pkt.Id(), sc.netconn.RemoteAddr(), string(sc.Meta), sc.fsm.State())
				}
				return iodefine.IOErr
			}
			if yes {
				logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
					WithField("remote", sc.netconn.RemoteAddr()).WithField("meta", string(sc.Meta)).
					Debug("recv dis conn ack succeed")
			} else {
				sc.log.Debugf("recv dis conn ack succeed, clientId: %d, packetId: %d, remote: %s, meta: %s",
					sc.ClientId, pkt.Id(), sc.netconn.RemoteAddr(), string(sc.Meta))
			}
			return iodefine.IOClosed

		case *packet.HeartbeatAckPacket:
			// 心跳确定
			ok := sc.fsm.InStates(CONNED)
			if !ok {
				if yes {
					logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
						WithField("remote", sc.netconn.RemoteAddr()).WithField("meta", string(sc.Meta)).
						WithField("state", sc.fsm.State()).Error("heartbeat at non-CONNED state")
				} else {
					sc.log.Errorf("heartbeat at non-CONNED state, clientId: %d, packetId: %d, remote: %s, meta: %s, state: %s",
						sc.ClientId, pkt.Id(), sc.netconn.RemoteAddr(), string(sc.Meta), sc.fsm.State())
				}
				return iodefine.IOErr
			}
			return iodefine.IOSuccess

		default:
			return iodefine.IOData
		}
	}
	if yes {
		logrusLog.WithField("clientId", sc.ClientId).WithField("packetId", pkt.Id()).
			WithField("remote", sc.netconn.RemoteAddr()).WithField("meta", string(sc.Meta)).
			Debugf("BUG! should not be here, iotype: %v", iotype)
	} else {
		sc.log.Debugf("BUG! should not be here, clientId: %d, packetId: %d, remote: %s, meta: %s",
			sc.ClientId, pkt.Id(), sc.netconn.RemoteAddr(), string(sc.Meta))
	}
	return iodefine.IOErr
}

func (sc *SendConn) heartbeat(data interface{}) error {
	logrusLog, yes := sc.log.(logrus.FieldLogger)
	err := error(nil)

	sc.hbTick, err = sc.tmr.Time(
		uint64(sc.Heartbeat), struct{}{}, nil, sc.heartbeat)
	if err != nil {
		// TODO
		if yes {
			logrusLog.WithField("clientId", sc.ClientId).
				WithField("remote", sc.netconn.RemoteAddr()).WithField("meta", string(sc.Meta)).
				Errorf("set heartbeat timer err", err)
		} else {
			sc.log.Errorf("set heartbeat timer err, clientId: %d, remote: %s, meta: %s",
				err, sc.ClientId, sc.netconn.RemoteAddr(), string(sc.Meta))
		}
		return err
	}
	sc.connMtx.RLock()
	if !sc.connOK {
		sc.connMtx.RUnlock()
		return io.EOF
	}
	pkt := sc.pf.NewHeartbeatPacket()
	ver := sc.netconnVer
	sc.writeCh <- &packetAndConnver{pkt, ver}
	sc.connMtx.RUnlock()
	return nil
}
