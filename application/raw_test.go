package application

import (
	"container/list"
	"math"
	"os"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio/application/mock"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/go-timer/v2"
)

func getStream(_ *gomock.Controller, readWait, writeWait time.Duration) *stream {
	tmr := timer.NewTimer()
	sm := &stream{
		opts: &opts{
			log:      log.DefaultLog,
			tmr:      tmr,
			tmrOwner: nil,
			pf:       packet.NewPacketFactory(id.NewIDCounter(id.Even)),
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
			time.Sleep(writeWait)
			<-sm.writeInCh
		}
	}()
	return sm
}

func Test_stream_SetDeadline(t *testing.T) {
	ctl := gomock.NewController(t)
	defer ctl.Finish()

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
			args:    args{t: time.Now().Add(3 * time.Second)},
			wantErr: true,
			err:     os.ErrDeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := getStream(ctl, 5*time.Second, 5*time.Second)
			dialogue := mock.NewMockDialogue(ctl)
			dialogue.EXPECT().DialogueID().Return(uint64(1))
			sm.dg = dialogue

			err := sm.SetDeadline(tt.args.t)
			if err != nil {
				t.Error(err)
			}
			buf := make([]byte, 32)
			_, err = sm.Read(buf)
			if (err != nil) != tt.wantErr {
				t.Errorf("stream.SetReadDeadline() error = %v, wantErr %v", err, tt.wantErr)
			}
			_, err = sm.Write([]byte("this is a test"))
			if (err != nil) != tt.wantErr {
				t.Errorf("stream.SetReadDeadline() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_stream_SetReadDeadline(t *testing.T) {
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	type args struct {
		t time.Time
	}
	tests := []struct {
		name      string
		args      args
		wantErr   bool
		err       error
		afterRead bool
	}{
		{
			args:      args{t: time.Now().Add(2 * time.Second)},
			wantErr:   true,
			err:       os.ErrDeadlineExceeded,
			afterRead: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := getStream(ctl, 5*time.Second, 5*time.Second)

			if tt.afterRead {
				go func() {
					time.Sleep(time.Second)
					err := sm.SetReadDeadline(tt.args.t)
					if err != nil {
						t.Error(err)
					}
				}()
			} else {
				err := sm.SetReadDeadline(tt.args.t)
				if err != nil {
					t.Error(err)
				}
			}
			buf := make([]byte, 32)
			_, err := sm.Read(buf)
			if (err != nil) != tt.wantErr {
				t.Errorf("stream.SetReadDeadline() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_stream_MultiSetReadDeadline(t *testing.T) {
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	type args struct {
		t time.Time
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
		err     error
	}{
		{
			args:    args{t: time.Now().Add(2 * time.Second)},
			wantErr: true,
			err:     os.ErrDeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := getStream(ctl, 5*time.Second, 5*time.Second)
			err := sm.SetReadDeadline(tt.args.t)
			if err != nil {
				t.Error(err)
			}
			err = sm.SetReadDeadline(tt.args.t.Add(2 * time.Second))
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

func Test_stream_Read(t *testing.T) {
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	type args struct {
		t time.Time
	}
	tests := []struct {
		name       string
		waitSecond int
		wantErr    bool
	}{
		{
			waitSecond: 5,
			wantErr:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := getStream(ctl, time.Duration(tt.waitSecond)*time.Second,
				time.Duration(tt.waitSecond)*time.Second)
			start := time.Now()
			buf := make([]byte, 32)
			_, err := sm.Read(buf)
			if (err != nil) != tt.wantErr {
				t.Errorf("stream.Read() error = %v, wantErr %v", err, tt.wantErr)
			}
			elapse := time.Now().Sub(start).Seconds()
			if int(math.Round(elapse)) != tt.waitSecond {
				t.Errorf("stream.Read() error, elapse not matched")
			}
		})
	}
}

func Test_stream_SetWriteDeadline(t *testing.T) {
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	type args struct {
		t time.Time
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
		err     error
	}{
		{
			args:    args{t: time.Now().Add(2 * time.Second)},
			wantErr: true,
			err:     os.ErrDeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := getStream(ctl, 5*time.Second, 5*time.Second)
			dialogue := mock.NewMockDialogue(ctl)
			dialogue.EXPECT().DialogueID().Return(uint64(1))
			sm.dg = dialogue

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

func Test_stream_Write(t *testing.T) {
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	type args struct {
		t time.Time
	}
	tests := []struct {
		name       string
		waitSecond int
		wantErr    bool
	}{
		{
			waitSecond: 5,
			wantErr:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := getStream(ctl, time.Duration(tt.waitSecond)*time.Second,
				time.Duration(tt.waitSecond)*time.Second)
			dialogue := mock.NewMockDialogue(ctl)
			dialogue.EXPECT().DialogueID().Return(uint64(1))
			sm.dg = dialogue
			start := time.Now()
			_, err := sm.Write([]byte("this is a test"))
			if (err != nil) != tt.wantErr {
				t.Errorf("stream.Write() error = %v, wantErr %v", err, tt.wantErr)
			}
			elapse := time.Now().Sub(start).Seconds()
			if int(math.Round(elapse)) != tt.waitSecond {
				t.Errorf("stream.Write() error, elapse not matched")
			}
		})
	}
}

func Test_stream_MultiSetWriteDeadline(t *testing.T) {
	ctl := gomock.NewController(t)
	defer ctl.Finish()

	type args struct {
		t time.Time
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
		err     error
	}{
		{
			args:    args{t: time.Now().Add(2 * time.Second)},
			wantErr: true,
			err:     os.ErrDeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := getStream(ctl, 5*time.Second, 5*time.Second)
			dialogue := mock.NewMockDialogue(ctl)
			dialogue.EXPECT().DialogueID().Return(uint64(1))
			sm.dg = dialogue

			err := sm.SetWriteDeadline(tt.args.t)
			if err != nil {
				t.Error(err)
			}
			err = sm.SetWriteDeadline(tt.args.t.Add(2 * time.Second))
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
