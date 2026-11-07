package conn

import (
	"context"
	"errors"
	"github.com/singchia/geminio/packet"
	"testing"
	"time"
)

func TestResetWaitsForSocketReceipt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bc := &baseConn{ctx: ctx, heartbeatWriteCh: make(chan packet.Packet, 1)}
	deadline, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	// Queue space exists, but no socket writer consumes it: must not report success.
	if err := bc.WriteResetContext(deadline, packet.NewStreamResetPacket(3)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	<-bc.heartbeatWriteCh
	done := make(chan error, 1)
	go func() { done <- bc.WriteResetContext(ctx, packet.NewStreamResetPacket(5)) }()
	p := <-bc.heartbeatWriteCh
	p.(*resetReceipt).written <- nil
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("receipt not observed")
	}
}
