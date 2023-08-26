package application

import (
	"container/list"
	"testing"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/go-timer/v2"
)

func getStream(readWait, writeWait time.Duration) *stream {
	sm := &stream{
		streamOpts: streamOpts{
			log:        log.DefaultLog,
			tmr:        timer.NewTimer(),
			tmrOutside: true,
		},
		streamCh:      make(chan *packet.StreamPacket),
		writeInCh:     make(chan packet.Packet),
		dlReadChList:  list.New(),
		dlWriteChList: list.New(),
	}
	go func() {
		for {
			time.Sleep(readWait)
			sm.streamCh <- &packet.StreamPacket{}
		}
	}()
	go func() {
		for {
			time.Sleep(readWait)
			<-sm.writeInCh
		}
	}()
	return sm
}

func TestSetDeadline(t *testing.T) {
	sm := getStream(10*time.Second, 10*time.Second)
	sm.setReadDeadline(time.Now().Add(5 * time.Second))
}
