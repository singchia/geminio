package conn

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/singchia/go-timer/v2"
)

type countingHeartbeatTick struct {
	timer.Tick
	cancels atomic.Int32
}

func (tick *countingHeartbeatTick) Cancel() error {
	tick.cancels.Add(1)
	return nil
}

func TestConcurrentHeartbeatTeardown(t *testing.T) {
	tick := &countingHeartbeatTick{}
	cc := &ClientConn{baseConn: &baseConn{hbTick: tick}}
	var workers sync.WaitGroup
	for i := 0; i < 64; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			cc.stopHeartbeat()
		}()
	}
	workers.Wait()
	if got := tick.cancels.Load(); got != 1 {
		t.Fatalf("heartbeat canceled %d times, want once", got)
	}
}
