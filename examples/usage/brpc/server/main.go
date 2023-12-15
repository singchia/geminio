package main

import (
	"context"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/server"
)

func main() {
	opt := server.NewEndOptions()
	// the option means all End from server will wait for the rpc registration
	opt.SetWaitRemoteRPCs("client-echo")
	// pre-register server side method
	opt.SetRegisterLocalRPCs(&geminio.MethodRPC{"server-echo", echo})

	ln, err := server.Listen("tcp", "127.0.0.1:8080", opt)
	if err != nil {
		log.Errorf("server listen err: %s", err)
		return
	}

	for {
		end, err := ln.AcceptEnd()
		if err != nil {
			log.Errorf("accept err: %s", err)
			break
		}
		go func() {
			// call client side method
			rsp, err := end.Call(context.TODO(), "client-echo", end.NewRequest([]byte("foo")))
			if err != nil {
				log.Errorf("end call err: %s", err)
				return
			}
			if string(rsp.Data()) != "foo" {
				log.Fatal("wrong echo", string(rsp.Data()))
			}
			log.Info("client echo:", string(rsp.Data()))
		}()
	}
}

func echo(_ context.Context, req geminio.Request, rsp geminio.Response) {
	rsp.SetData(req.Data())
	log.Info("server echo:", string(req.Data()))
}
