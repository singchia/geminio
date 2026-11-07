package conn

import (
	"context"
	"errors"
	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/packet"
	"testing"
	"time"
)

func TestWriteContextCancelsOnlyWaitingWriter(t *testing.T) {
	transportCtx, stop := context.WithCancel(context.Background())
	defer stop()
	bc := &baseConn{ctx: transportCtx, connOK: true, writeInCh: make(chan packet.Packet, 1)}
	bc.log = log.DefaultLog
	bc.writeInSize = 1
	pkt := &packet.HeartbeatPacket{PacketHeader: &packet.PacketHeader{Typ: packet.TypeHeartbeatPacket}}
	bc.writeInCh <- pkt // Occupy the shared queue without running a consumer.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := bc.WriteContext(ctx, pkt); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("write cancellation: %v", err)
	}
	if transportCtx.Err() != nil {
		t.Fatal("per-stream cancellation stopped transport")
	}
	<-bc.writeInCh
	if err := bc.WriteContext(context.Background(), pkt); err != nil {
		t.Fatal(err)
	}
}
