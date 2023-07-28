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
	sm, err := multiplexer.NewDialogueMgr(cc,
		multiplexer.OptionMultiplexerAcceptDialogue(),
		multiplexer.OptionMultiplexerClosedDialogue(),
		multiplexer.OptionTimer(tmr),
		multiplexer.OptionPacketFactory(pf))
	if err != nil {
		log.Println("new sm err:", err)
		return
	}

	sns := sync.Map{}
	dialogues := sm.ListDialogues()
	sns.Store("1", dialogues[0])
	// handle multiplexer
	handleAcceptClosedDialogue(sm, sns)

	// the cli protocol
	// 1. open
	// 2. close
	// 3. close sessionID
	// 4. send sessionID msg
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		text := scanner.Text()
		parts := strings.Split(text, " ")
		switch len(parts) {
		case 1:
			// 1. open
			// 2. close
			if parts[0] == "quit" || parts[0] == "close" {
				sm.Close()
				cc.Close()
				goto END
			}
			if parts[0] == "open" {
				sn, err := sm.OpenDialogue([]byte("auztin zhai"))
				if err != nil {
					log.Println("open session err:", err)
					continue
				}
				sns.Store(strconv.FormatUint(sn.DialogueID(), 10), sn)
				continue
			}
		case 2:
			// 1. close sessionID
			// 2. send msg, to default session
			if parts[0] == "close" {
				// close sessionID
				session := parts[1]
				sn, ok := sns.Load(session)
				if !ok {
					log.Printf("session id: %s not found \n", session)
					continue
				}
				sn.(multiplexer.Dialogue).Close()
				sns.Delete(session)
				continue
			}
			if parts[0] == "send" {
				msg := parts[1]
				sn, ok := sns.Load("1")
				if !ok {
					log.Printf("session id: %s not found\n", "1")
					continue
				}
				pkt := pf.NewMessagePacketWithSessionID(
					sn.(multiplexer.Dialogue).DialogueID(),
					[]byte{}, []byte(msg), []byte{})
				sn.(multiplexer.Dialogue).Write(pkt)
				continue
			}
		case 3:
			// send sessionID msg
			session := parts[1]
			msg := parts[2]
			if parts[0] == "send" {
				sn, ok := sns.Load(session)
				if !ok {
					log.Printf("session id: %s not found\n", session)
					continue
				}
				pkt := pf.NewMessagePacketWithSessionID(
					sn.(multiplexer.Dialogue).DialogueID(),
					[]byte{}, []byte(msg), []byte{})
				sn.(multiplexer.Dialogue).Write(pkt)
				continue
			}
		}
		log.Println("illegal operation")
	}
END:
	time.Sleep(time.Second)
}

func handleAcceptClosedDialogue(sm multiplexer.Multiplexer, sns sync.Map) {
	go func() {
		for {
			sn, err := sm.AcceptDialogue()
			if err != nil {
				break
			}
			snID := strconv.FormatUint(sn.ClientID(), 10) + "-" + "1"
			sns.Store(snID, sn)
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
			sns.Delete(sn.DialogueID())
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
