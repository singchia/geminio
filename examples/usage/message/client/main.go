package main

import (
	"context"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/client"
)

func main() {
	log.SetLevel(log.LevelInfo)
	end, err := client.NewEnd("tcp", "127.0.0.1:8080")
	if err != nil {
		log.Errorf("client dial err: %s", err)
		return
	}
	msg := end.NewMessage([]byte("hello"))
	err = end.Publish(context.TODO(), msg)
	if err != nil {
		log.Errorf("end publish err: %s", err)
		return
	}
	end.Close()
}
