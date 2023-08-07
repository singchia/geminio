package main

import (
	"bufio"
	"flag"
	"net"
	"os"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
)

func main() {
	network := flag.String("network", "tcp", "network to dial")
	address := flag.String("address", "127.0.0.1:1202", "address to dial")
	loglevel := flag.Int("loglevel", 3, "1: trace, 2: debug, 3: info, 4: warn, 5: error")
	flag.Parse()

	log.SetLevel(log.Level(*loglevel))

	dialer := func() (net.Conn, error) {
		return net.Dial(*network, *address)
	}

	pf := packet.NewPacketFactory(id.NewIDCounter(id.Odd))
	sc, err := conn.NewClientConnWithDialer(dialer, conn.OptionClientConnPacketFactory(pf))
	if err != nil {
		log.Error("new send conn err:", err)
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	go func() {
		for {
			pkt, err := sc.Read()
			if err != nil {
				log.Info("read error", err)
				os.Stdin.Close()
				return
			}
			msg := pkt.(*packet.MessagePacket)
			log.Debug(string(msg.Data.Key), string(msg.Data.Value))
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
			log.Error("write err:", err)
			break
		}
	}
	time.Sleep(time.Second)
}
