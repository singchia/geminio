package main

import (
	"net"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/client"
)

func main() {
	end, err := client.NewEnd("tcp", "127.0.0.1:8080")
	if err != nil {
		log.Errorf("client dial err: %s", err)
		return
	}
	// the end is also a net.Listener
	ln := net.Listener(end)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Errorf("end accept err: %s", err)
			break
		}
		go func(conn net.Conn) {
			buf := make([]byte, 128)
			_, err := conn.Read(buf)
			if err != nil {
				return
			}
			log.Info("read:", string(buf))
		}(conn)
	}
	end.Close()
}
