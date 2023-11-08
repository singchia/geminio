package regression

import (
	"context"
	"errors"
	"testing"

	"github.com/singchia/geminio"
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
