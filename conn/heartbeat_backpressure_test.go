package conn

import (
	"context"
	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"net"
	"testing"
	"time"
)

// 控制包由测试注入接收入口，固定“双方输出满时同时收到心跳控制包”的时序。
func TestHeartbeatBackpressure(t *testing.T) {
	t.Run("without_control", func(t *testing.T) { heartbeatBackpressure(t, false) })
	t.Run("with_control", func(t *testing.T) { heartbeatBackpressure(t, true) })
}

func heartbeatBackpressure(t *testing.T, inject bool) {
	a, b := net.Pipe()
	ready := make(chan *ServerConn, 1)
	go func() {
		s, e := NewServerConn(a)
		if e != nil {
			ready <- nil
			return
		}
		ready <- s
	}()
	c, e := NewClientConn(b)
	if e != nil {
		t.Fatal(e)
	}
	s := <-ready
	if s == nil {
		t.Fatal("server handshake")
	}
	defer c.ForceClose()
	defer s.ForceClose()
	const count = 2048
	for _, bc := range []*baseConn{c.baseConn, s.baseConn} {
		go func(bc *baseConn) {
			for i := 0; i < count; i++ {
				if e := bc.Write(bc.pf.NewStreamPacketWithSessionID(1, []byte("data"))); e != nil {
					return
				}
			}
		}(bc)
	}
	until := time.Now().Add(3 * time.Second)
	for len(c.writeInCh) < cap(c.writeInCh) || len(s.writeInCh) < cap(s.writeInCh) {
		if time.Now().After(until) {
			t.Fatal("queues not full")
		}
		time.Sleep(time.Millisecond)
	}
	if inject {
		c.heartbeatCh <- c.pf.NewHeartbeatAckPacket(101)
		s.heartbeatCh <- s.pf.NewHeartbeatPacket()
	}
	// 让事件循环取走控制包，然后恢复两端上层消费者。
	time.Sleep(50 * time.Millisecond)
	results := make(chan int, 2)
	for _, bc := range []*baseConn{c.baseConn, s.baseConn} {
		go func(bc *baseConn) {
			n := 0
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			for n < count {
				select {
				case p, ok := <-bc.ChannelRead():
					if !ok {
						results <- n
						return
					}
					if p.Type() == packet.TypeStreamPacket {
						n++
					}
				case <-timer.C:
					results <- n
					return
				}
			}
			results <- n
		}(bc)
	}
	for i := 0; i < 2; i++ {
		if n := <-results; n != count {
			t.Errorf("received %d/%d despite both upper readers draining", n, count)
		}
	}
}

func TestHeartbeatQueueOverflowClosesTransport(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bc := &baseConn{netconn: a, ctx: ctx, cancel: cancel, heartbeatWriteCh: make(chan packet.Packet, 1)}
	bc.log = log.DefaultLog
	pf := packet.NewPacketFactory(id.NewIDCounter(id.Even))
	if !bc.queueHeartbeat(pf.NewHeartbeatPacket()) {
		t.Fatal("first heartbeat rejected")
	}
	if bc.queueHeartbeat(pf.NewHeartbeatPacket()) {
		t.Fatal("overflow was accepted")
	}
	if ctx.Err() == nil {
		t.Fatal("overflow did not cancel connection")
	}
	if _, err := b.Write([]byte{1}); err == nil {
		t.Fatal("transport remained open")
	}
}

func TestHeartbeatWriterPriority(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bc := &baseConn{netconn: a, ctx: ctx, cancel: cancel, heartbeatWriteCh: make(chan packet.Packet, 1), writeOutCh: make(chan packet.Packet, 1)}
	bc.log = log.DefaultLog
	pf := packet.NewPacketFactory(id.NewIDCounter(id.Even))
	bc.writeOutCh <- pf.NewStreamPacketWithSessionID(1, []byte("data"))
	bc.heartbeatWriteCh <- pf.NewHeartbeatPacket()
	done := make(chan struct{})
	go func() { defer close(done); bc.writePkt() }()
	defer func() { bc.ForceClose(); <-done }()
	b.SetReadDeadline(time.Now().Add(time.Second))
	for _, want := range []packet.Type{packet.TypeHeartbeatPacket, packet.TypeStreamPacket} {
		p, err := packet.DecodeFromReader(b)
		if err != nil {
			t.Fatal(err)
		}
		if p.Type() != want {
			t.Fatalf("got %v want %v", p.Type(), want)
		}
	}
}
