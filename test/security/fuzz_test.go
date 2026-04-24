package security

import (
	"context"
	"testing"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/gemino"
	"github.com/singchia/gemino/test"
)

// FuzzRPCData fuzzes RPC data with random bytes
func FuzzRPCData(f *testing.F) {
	// Seed corpus
	f.Add([]byte("normal data"))
	f.Add([]byte(""))
	f.Add([]byte{0x00, 0x01, 0xFF})

	f.Fuzz(func(t *testing.T, data []byte) {
		log.SetLevel(log.LevelError)
		sEnd, cEnd, err := test.GetEndPair()
		if err != nil {
			t.Skipf("get end pair failed: %v", err)
			return
		}
		defer sEnd.Close()
		defer cEnd.Close()

		sEnd.Register(context.TODO(), "fuzz", func(ctx context.Context, req gemino.Request, resp gemino.Response) {
			resp.SetData(req.Data())
		})

		resp, err := cEnd.Call(context.TODO(), "fuzz", cEnd.NewRequest(data))
		if err != nil {
			// Call might fail for various reasons, that's OK
			return
		}

		// Response should match request
		if string(resp.Data()) != string(data) {
			t.Errorf("data mismatch: got %v, want %v", resp.Data(), data)
		}
	})
}

// FuzzMessageData fuzzes message publishing
func FuzzMessageData(f *testing.F) {
	f.Add([]byte("normal message"))
	f.Add([]byte(""))
	f.Add([]byte{0xFF, 0xFE, 0x00})

	f.Fuzz(func(t *testing.T, data []byte) {
		log.SetLevel(log.LevelError)
		sEnd, cEnd, err := test.GetEndPair()
		if err != nil {
			t.Skipf("get end pair failed: %v", err)
			return
		}
		defer sEnd.Close()
		defer cEnd.Close()

		received := make(chan []byte, 1)
		go func() {
			msg, err := sEnd.Receive(context.TODO())
			if err != nil {
				return
			}
			received <- msg.Data()
			msg.Done()
		}()

		msg := cEnd.NewMessage(data)
		err = cEnd.Publish(context.TODO(), msg)
		if err != nil {
			return
		}

		select {
		case recvData := <-received:
			if string(recvData) != string(data) {
				t.Errorf("data mismatch: got %v, want %v", recvData, data)
			}
		case <-context.TODO().Done():
			t.Error("timeout waiting for message")
		}
	})
}

// FuzzStreamData fuzzes stream Read/Write operations
func FuzzStreamData(f *testing.F) {
	f.Add([]byte("stream data"))
	f.Add([]byte(""))
	f.Add([]byte{0x00, 0x01, 0x02, 0x03})

	f.Fuzz(func(t *testing.T, data []byte) {
		log.SetLevel(log.LevelError)
		sEnd, cEnd, ss, cs, err := test.GetEndStream()
		if err != nil {
			t.Skipf("get stream failed: %v", err)
			return
		}
		defer sEnd.Close()
		defer cEnd.Close()
		defer ss.Close()
		defer cs.Close()

		go func() {
			cs.Write(data)
			cs.Close()
		}()

		ss.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, len(data)+1024)
		n, err := ss.Read(buf)
		if err != nil && err != context.Canceled {
			// Read might fail due to deadline or stream close, that's OK
			return
		}

		if n > 0 && string(buf[:n]) != string(data) {
			t.Errorf("data mismatch: got %v, want %v", buf[:n], data)
		}
	})
}
