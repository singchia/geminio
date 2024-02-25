package regression

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/server"
	"github.com/singchia/geminio/test"
)

func TestCall(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatal(err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	echoServer := func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData(req.Data())
	}
	sEnd.Register(context.TODO(), "hello", echoServer)

	resp, err := cEnd.Call(context.TODO(), "hello", cEnd.NewRequest([]byte("world")))
	if err != nil {
		t.Error(err)
		return
	}
	if string(resp.Data()) != "world" {
		t.Error(errors.New("unequal request"))
		return
	}
}

func TestMessage(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatal(err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)

		msg, err := sEnd.Receive(context.TODO())
		if err != nil {
			return
		}
		msg.Done()
		t.Logf("recv1 data=%s", string(msg.Data()))

		msg, err = sEnd.Receive(context.TODO())
		if err != nil {
			t.Error(err)
			return
		}
		msg.Done()
		t.Logf("recv2 data=%s", string(msg.Data()))
	}()

	cEnd.Publish(context.TODO(), cEnd.NewMessage([]byte("hello")))
	cEnd.Publish(context.TODO(), cEnd.NewMessage([]byte("hello2")))

	<-done
}

func TestServer(t *testing.T) {
	network := "tcp"
	address := "127.0.0.1:12345"
	srv, err := server.Listen(network, address)
	if err != nil {
		t.Error(err)
		return
	}
	wg := sync.WaitGroup{}
	wg.Add(1)

	index, count := uint64(0), uint64(1000)
	accepted := make([]uint64, count)
	go func() {
		for {
			end, err := srv.AcceptEnd()
			if err != nil {
				t.Error(err)
				return
			}
			meta := end.Meta()
			id := binary.BigEndian.Uint64(meta)
			accepted[id-1] = 1
			new := atomic.AddUint64(&index, 1)
			if new == count {
				wg.Done()
			}
		}
	}()

	for i := uint64(0); i < count; i++ {
		opt := client.NewEndOptions()
		buf := make([]byte, 8)
		binary.BigEndian.PutUint64(buf, i+1)
		opt.SetMeta(buf)
		_, err := client.NewEnd(network, address, opt)
		if err != nil {
			t.Error(err)
		}
	}

	wg.Wait()
	for _, elem := range accepted {
		if elem != 1 {
			t.Error("failed end exist")
			return
		}
	}
}
