package main

import (
	"context"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/client"
)

func main() {
	opt := client.NewEndOptions()
	opt.SetWaitRemoteRPCs("echo")
	end, err := client.NewEnd("tcp", "127.0.0.1:8080", opt)
	if err != nil {
		log.Errorf("client dial err: %s", err)
		return
	}
	rsp, err := end.Call(context.TODO(), "echo", end.NewRequest([]byte("hello")))
	if err != nil {
		log.Errorf("end call err: %s", err)
		return
	}
	if string(rsp.Data()) != "hello" {
		log.Fatal("wrong echo", string(rsp.Data()))
	}
	log.Info("echo:", string(rsp.Data()))
	end.Close()
}
