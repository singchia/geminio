package application

import (
	"container/list"
	"os"
	"testing"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/go-timer/v2"
)

func getStream(readWait, writeWait time.Duration) *stream {
	tmr := timer.NewTimer()
	sm := &stream{
		streamOpts: streamOpts{
			log:        log.DefaultLog,
			tmr:        tmr,
			tmrOutside: true,
		},
		shub:          synchub.NewSyncHub(synchub.OptionTimer(tmr)),
		streamOK:      true,
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

func Test_stream_SetReadDeadline(t *testing.T) {
	type args struct {
		t time.Time
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
		err     error
	}{
		// TODO: Add test cases.
		{
			args:    args{t: time.Now().Add(5 * time.Second)},
			wantErr: true,
			err:     os.ErrDeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := getStream(10*time.Second, 10*time.Second)
			err := sm.SetReadDeadline(tt.args.t)
			if err != nil {
				t.Error(err)
			}
			buf := make([]byte, 32)
			_, err = sm.Read(buf)
			if (err != nil) != tt.wantErr {
				t.Errorf("stream.SetReadDeadline() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_stream_SetWriteDeadline(t *testing.T) {
	type args struct {
		t time.Time
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
		err     error
	}{
		// TODO: Add test cases.
		{
			args:    args{t: time.Now().Add(5 * time.Second)},
			wantErr: true,
			err:     os.ErrDeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := getStream(10*time.Second, 10*time.Second)
			err := sm.SetWriteDeadline(tt.args.t)
			if err != nil {
				t.Error(err)
			}
			_, err = sm.Write([]byte("this is a test"))
			if (err != nil) != tt.wantErr {
				t.Errorf("stream.SetReadDeadline() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
