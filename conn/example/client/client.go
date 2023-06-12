package main

import (
	"bufio"
	"flag"
	"log"
	"net"
	"os"
	"time"

	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
)

func main() {
	network := flag.String("network", "tcp", "network to dial")
	address := flag.String("address", "127.0.0.1:1202", "address to dial")
	flag.Parse()

	dialer := func() (net.Conn, error) {
		return net.Dial(*network, *address)
	}

	pf := packet.NewPacketFactory(id.NewIDCounter(id.Odd))
	sc, err := conn.NewClientConnWithDialer(dialer, conn.OptionClientConnPacketFactory(pf))
	if err != nil {
		log.Println("new send conn err:", err)
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	go func() {
		for {
			pkt, err := sc.Read()
			if err != nil {
				log.Println("read error", err)
				os.Stdin.Close()
				return
			}
			msg := pkt.(*packet.MessagePacket)
			log.Println(string(msg.MessageData.Key), string(msg.MessageData.Value))
		}
	}()

	for scanner.Scan() {
		text := scanner.Text()
		if text == "quit" {
			sc.Close()
			break
		}
		pkt := pf.NewMessagePacket([]byte{}, []byte(text), []byte{})
		err := sc.Write(pkt)
		if err != nil {
			log.Println("write err:", err)
			break
		}
	}
	time.Sleep(500 * time.Millisecond)
}
