package main

import (
	"flag"
	"log"
	"net"
	"strconv"

	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
)

func main() {
	network := flag.String("network", "tcp", "network to dial")
	address := flag.String("address", "127.0.0.1:1202", "address to dial")
	bench := flag.Int("bench", 1000, "bench client to dial")
	flag.Parse()

	dialer := func() (net.Conn, error) {
		return net.Dial(*network, *address)
	}

	pf := packet.NewPacketFactory(id.NewIDCounter(id.Odd))

	for i := 0; i < *bench; i++ {
		sc, err := conn.NewClientConnWithDialer(dialer, conn.OptionClientConnPacketFactory(pf))
		if err != nil {
			log.Println("new send conn err:", err)
			return
		}
		go func() {
			for {
				pkt, err := sc.Read()
				if err != nil {
					log.Println("read error", err)
					return
				}
				msg := pkt.(*packet.MessagePacket)
				log.Println(string(msg.MessageData.Key), string(msg.MessageData.Value))
			}
		}()

		pkt := pf.NewMessagePacket([]byte{}, []byte(strconv.Itoa(i)), []byte{})
		sc.Write(pkt)
		err = sc.Write(pkt)
		if err != nil {
			log.Println("write err:", err)
			break
		}
		sc.Close()
	}
}
