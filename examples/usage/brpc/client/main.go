package main

import (
	"context"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/client"
)

func main() {
	opt := client.NewEndOptions()
	// the option means all End from server will wait for the rpc registration
	opt.SetWaitRemoteRPCs("server-echo")
	// pre-register client side method
	opt.SetRegisterLocalRPCs(&geminio.MethodRPC{"client-echo", echo})

	end, err := client.NewEnd("tcp", "127.0.0.1:8080", opt)
	if err != nil {
		log.Errorf("client dial err: %s", err)
		return
	}
	// call server side method
	rsp, err := end.Call(context.TODO(), "server-echo", end.NewRequest([]byte("bar")))
	if err != nil {
		log.Errorf("end call err: %s", err)
		return
	}
	if string(rsp.Data()) != "bar" {
		log.Fatal("wrong echo", string(rsp.Data()))
	}
	log.Info("server echo:", string(rsp.Data()))
	end.Close()
}

func echo(_ context.Context, req geminio.Request, rsp geminio.Response) {
	rsp.SetData(req.Data())
	log.Info("client echo:", string(req.Data()))
}
