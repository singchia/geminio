package conn

import (
	"net"
	"testing"
	"time"

	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
)

// A manual legacy peer knows only Conn/ConnAck and omits the additive field.
func TestFlowNegotiationLegacyServer(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	a.SetDeadline(time.Now().Add(3 * time.Second))
	b.SetDeadline(time.Now().Add(3 * time.Second))
	done := make(chan error, 1)
	go func() {
		p, e := packet.DecodeFromReader(a)
		if e != nil {
			done <- e
			return
		}
		req := p.(*packet.ConnPacket)
		ack := packet.NewPacketFactory(nil).NewConnAckPacket(req.ID(), 123, nil)
		data, e := ack.Encode()
		if e == nil {
			_, e = a.Write(data)
		}
		done <- e
	}()
	c, err := NewClientConn(b)
	if err != nil {
		t.Fatal(err)
	}
	defer c.ForceClose()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if c.FlowControl() != 0 || c.StreamReset() != 0 {
		t.Fatal("enabled flow control with legacy server")
	}
}
func TestFlowNegotiationLegacyClient(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	a.SetDeadline(time.Now().Add(3 * time.Second))
	b.SetDeadline(time.Now().Add(3 * time.Second))
	ready := make(chan *ServerConn, 1)
	failure := make(chan error, 1)
	go func() {
		s, e := NewServerConn(a)
		if e != nil {
			failure <- e
			return
		}
		ready <- s
	}()
	p := packet.NewPacketFactory(id.NewIDCounter(id.Odd)).NewConnPacket(123, true, packet.Heartbeat20, nil)
	data, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Write(data); err != nil {
		t.Fatal(err)
	}
	ack, err := packet.DecodeFromReader(b)
	if err != nil {
		t.Fatal(err)
	}
	if ack.(*packet.ConnAckPacket).ConnData.FlowControl != 0 || ack.(*packet.ConnAckPacket).ConnData.StreamReset != 0 {
		t.Fatal("advertised flow control to legacy client")
	}
	select {
	case s := <-ready:
		defer s.ForceClose()
		if s.FlowControl() != 0 || s.StreamReset() != 0 {
			t.Fatal("enabled flow control with legacy client")
		}
	case err := <-failure:
		t.Fatal(err)
	}
}
