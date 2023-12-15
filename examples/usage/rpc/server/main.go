package main

import (
	"context"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/server"
)

func main() {
	ln, err := server.Listen("tcp", "127.0.0.1:8080")
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
			err := end.Register(context.TODO(), "echo", echo)
			if err != nil {
				return
			}
		}()
	}
}

func echo(_ context.Context, req geminio.Request, rsp geminio.Response) {
	rsp.SetData(req.Data())
	log.Info("echo:", string(req.Data()))
}
