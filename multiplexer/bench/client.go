package main

import (
	"flag"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/multiplexer"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/go-timer/v2"
)

func main() {
	network := flag.String("network", "tcp", "network to dial")
	address := flag.String("address", "127.0.0.1:1202", "address to dial")
	bench := flag.Int("bench", 1000, "bench client to dial")
	flag.Parse()

	dialer := func() (net.Conn, error) {
		return net.Dial(*network, *address)
	}
	tmr := timer.NewTimer()

	pf := packet.NewPacketFactory(id.NewIDCounter(id.Odd))

	for i := 0; i < *bench; i++ {
		// connection
		cc, err := conn.NewClientConnWithDialer(dialer,
			conn.OptionClientConnMeta([]byte(strconv.Itoa(i))),
			conn.OptionClientConnTimer(tmr),
			conn.OptionClientConnPacketFactory(pf))
		if err != nil {
			log.Println("new send conn err:", err)
			continue
		}
		// multiplexer
		sm, err := multiplexer.NewDialogueMgr(cc,
			multiplexer.OptionMultiplexerAcceptDialogue(),
			multiplexer.OptionMultiplexerClosedDialogue(),
			multiplexer.OptionTimer(tmr),
			multiplexer.OptionPacketFactory(pf))
		if err != nil {
			log.Println("new sm err:", err)
			continue
		}
		// handle multiplexer
		handleAcceptClosedDialogue(sm)

		sn, err := sm.OpenDialogue([]byte(strconv.Itoa(i)))
		if err != nil {
			log.Println("open dialogue err:", err)
			continue
		}
		pkt := pf.NewMessagePacketWithSessionID(sn.DialogueID(), []byte{}, []byte(strconv.Itoa(i)), []byte{})
		err = sn.Write(pkt)
		if err != nil {
			log.Println("write err:", err)
			continue
		}
		sm.Close()
		cc.Close()
	}
	time.Sleep(500 * time.Millisecond)
}

func handleAcceptClosedDialogue(sm multiplexer.Multiplexer) {
	go func() {
		for {
			sn, err := sm.AcceptDialogue()
			if err != nil {
				break
			}
			log.Printf("accepted dialogue: %d\n", sn.DialogueID())
			handleInput(sn)
		}
	}()

	go func() {
		for {
			sn, err := sm.ClosedDialogue()
			if err != nil {
				break
			}
			log.Printf("closed dialogue: %d\n", sn.DialogueID())
		}
	}()
}

func handleInput(sn multiplexer.Dialogue) {
	go func() {
		for {
			pkt, err := sn.Read()
			if err != nil {
				log.Println("read session err", err)
				return
			}
			msg := pkt.(*packet.MessagePacket)
			log.Println(">", sn.ClientID(), msg.SessionID(), string(msg.MessageData.Value))
		}
	}()
}
