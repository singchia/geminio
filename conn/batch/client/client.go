package main

import (
	"flag"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/go-timer/v2"
)

func main() {
	network := flag.String("network", "tcp", "network to dial")
	address := flag.String("address", "127.0.0.1:1202", "address to dial")
	bench := flag.Int("batch", 1000, "bench clients to dial")
	mps := flag.Int("mps", 1, "message per second per client")
	flag.Parse()

	log.SetLevel(log.LevelInfo)

	dialer := func() (net.Conn, error) {
		return net.Dial(*network, *address)
	}
	tmr := timer.NewTimer()
	pf := packet.NewPacketFactory(id.NewIDCounter(id.Odd))
	clients := []*conn.ClientConn{}

	for i := 0; i < *bench; i++ {
		go func(i int) {
			sc, err := conn.NewClientConnWithDialer(dialer, conn.OptionClientConnPacketFactory(pf))
			if err != nil {
				log.Error("new send conn err:", err)
				return
			}
			clients = append(clients, sc)

			go func() {
				for {
					pkt, err := sc.Read()
					if err != nil {
						log.Error("read error", err)
						return
					}
					msg := pkt.(*packet.MessagePacket)
					log.Debug(string(msg.MessageData.Key), string(msg.MessageData.Value))
				}
			}()
			go func() {
				tick := tmr.Add(time.Second, timer.WithCyclically())
				for {
					<-tick.C()
					for j := 0; j < *mps; j++ {
						pkt := pf.NewMessagePacket([]byte{}, []byte(strconv.Itoa(i)), []byte{})
						sc.Write(pkt)
						err = sc.Write(pkt)
						if err != nil {
							log.Error("write err:", err)
							return
						}
					}
				}
			}()
		}(i)
		if i%50 == 0 {
			time.Sleep(time.Second)
		}
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	for _, client := range clients {
		client.Close()
	}
	time.Sleep(5 * time.Second)
}
