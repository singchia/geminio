package main

import (
	"github.com/jumboframes/armorigo/log"
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
		// stream #1, and it's also a net.Conn
		sm1, err := end.OpenStream()
		if err != nil {
			log.Errorf("end open stream err: %s", err)
			break
		}
		sm1.Write([]byte("hello#1"))
		sm1.Close()

		// stream #2 and it's also a net.Conn
		sm2, err := end.OpenStream()
		if err != nil {
			log.Errorf("end open stream err: %s", err)
			break
		}
		sm2.Write([]byte("hello#2"))
		sm2.Close()
	}
}
