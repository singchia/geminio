package application

import (
	"container/list"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/singchia/geminio/application/mock"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
)

func TestBlockedIODeadlineWakeup(t *testing.T) {
	for _, write := range []bool{false, true} {
		for _, past := range []bool{false, true} {
			t.Run(map[bool]string{false: "read", true: "write"}[write]+map[bool]string{false: "_future", true: "_past"}[past], func(t *testing.T) {
				ctl := gomock.NewController(t)
				dg := mock.NewMockDialogue(ctl)
				dg.EXPECT().DialogueID().Return(uint64(1)).AnyTimes()
				sm := &stream{dg: dg, streamOK: true,
					opts:     &opts{pf: packet.NewPacketFactory(id.NewIDCounter(id.Even))},
					streamCh: make(chan *packet.StreamPacket), writeInCh: make(chan packet.Packet),
					dlReadChList: list.New(), dlWriteChList: list.New()}
				defer sm.SetDeadline(time.Time{})
				done := make(chan error, 1)
				go func() {
					var err error
					if write {
						_, err = sm.Write([]byte("blocked"))
					} else {
						_, err = sm.Read(make([]byte, 1))
					}
					done <- err
				}()
				until := time.Now().Add(time.Second)
				for {
					sm.dlMtx.RLock()
					n := sm.dlReadChList.Len() + sm.dlWriteChList.Len()
					sm.dlMtx.RUnlock()
					if n > 0 {
						break
					}
					if time.Now().After(until) {
						t.Fatal("I/O did not register")
					}
					time.Sleep(time.Millisecond)
				}
				deadline := time.Now().Add(15 * time.Millisecond)
				if past {
					deadline = time.Now().Add(-time.Second)
				}
				sm.SetDeadline(deadline)
				select {
				case err := <-done:
					if !errors.Is(err, os.ErrDeadlineExceeded) {
						t.Fatalf("got %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("deadline failed to wake blocked I/O")
				}
			})
		}
	}
}
