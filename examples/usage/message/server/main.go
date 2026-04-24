package main

import (
	"context"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/server"
)

func main() {
	log.SetLevel(log.LevelInfo)
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
			msg, err := end.Receive(context.TODO())
			if err != nil {
				return
			}
			log.Infof("end receive: %s", string(msg.Data()))
			msg.Done()
		}()
	}

}
