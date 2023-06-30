package main

import (
	"bufio"
	"flag"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
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
	flag.Parse()
	dialer := func() (net.Conn, error) {
		return net.Dial(*network, *address)
	}

	tmr := timer.NewTimer()
	pf := packet.NewPacketFactory(id.NewIDCounter(id.Odd))

	// connection
	cc, err := conn.NewClientConnWithDialer(dialer,
		conn.OptionClientConnMeta([]byte("Austin Zhai")),
		conn.OptionClientConnTimer(tmr),
		conn.OptionClientConnPacketFactory(pf))
	if err != nil {
		log.Println("new send conn err:", err)
		return
	}

	// multiplexer
	sm, err := multiplexer.NewMultiplexer(cc,
		multiplexer.OptionMultiplexerAcceptDialogue(),
		multiplexer.OptionMultiplexerClosedDialogue(),
		multiplexer.OptionTimer(tmr),
		multiplexer.OptionPacketFactory(pf))
	if err != nil {
		log.Println("new sm err:", err)
		return
	}

	sns := sync.Map{}

	go func() {
		for {
			sn, err := sm.AcceptDialogue()
			if err != nil {
				break
			}
			sns.Store(sn.DialogueID(), sn)
			log.Printf("accepted session: %d\n", sn.DialogueID())
			go func() {
				for {
					pkt, err := sn.Read()
					if err != nil {
						log.Println("read session err", err)
						return
					}
					msg := pkt.(*packet.MessagePacket)
					log.Println("> ", cc.ClientID(), msg.SessionID, string(msg.MessageData.Value))
				}
			}()
		}
	}()

	go func() {
		for {
			sn, err := sm.ClosedDialogue()
			if err != nil {
				break
			}
			log.Printf("closed session: %d\n", sn.DialogueID())
			sns.Delete(sn.DialogueID())
		}
	}()

	// open
	// close
	// close sessionId
	// sendto sessionId msg
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		text := scanner.Text()
		index := strings.Index(text, " ")
		if index == -1 {
			switch text {
			case "open":
				sn, err := sm.OpenDialogue([]byte("austin zhai 1"))
				if err != nil {
					log.Println("open session err:", err)
					continue
				}
				sns.Store(sn.DialogueID(), sn)
			case "close":
				cc.Close()
				goto END
			}
			continue
		}
		command := text[:index]
		text = text[index+1:]
		switch command {
		case "close":
			sessionId, err := strconv.ParseUint(text, 10, 64)
			if err != nil {
				log.Println("illegal id", err, text)
				continue
			}
			sn, ok := sns.Load(sessionId)
			if !ok {
				log.Printf("sessionId not found '%d'\n", sessionId)
				continue
			}
			sn.(multiplexer.Dialogue).Close()
			sns.Delete(sessionId)
			continue

		case "sendto":
			index = strings.Index(text, " ")
			if index == -1 {
				log.Println("msg not found")
				continue
			}
			//text = text[index+1:]
			// session
			sessionId, err := strconv.ParseUint(text[:index], 10, 64)
			if err != nil {
				log.Println("illegal id", err, text)
				continue
			}
			sn, ok := sns.Load(sessionId)
			if !ok {
				log.Printf("sessionId not found '%d'\n", sessionId)
				continue
			}
			pkt := pf.NewMessagePacket([]byte{}, []byte(text[index+1:]), []byte{})
			pkt.SessionID = sn.(multiplexer.Dialogue).DialogueID()
			sn.(multiplexer.Dialogue).Write(pkt)
			continue
		}
	}
END:
	time.Sleep(time.Second)
}
