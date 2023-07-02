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

var (
	clients sync.Map
	sms     sync.Map
	sns     sync.Map
)

func main() {
	network := flag.String("network", "tcp", "network to listen")
	address := flag.String("address", "127.0.0.1:1202", "address to listen")
	flag.Parse()

	ln, err := net.Listen(*network, *address)
	if err != nil {
		log.Println(err)
		return
	}

	tmr := timer.NewTimer()

	clients = sync.Map{}
	sms = sync.Map{}
	sns = sync.Map{}

	// the cli protocol
	// 1. open clientID
	// 2. close clientID
	// 3. close clientID dialogueID
	// 4. sendto clientID dialogueID msg
	// 5. quit
	pf := packet.NewPacketFactory(id.NewIDCounter(id.Even))
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			text := scanner.Text()
			parts := strings.Split(text, " ")
			switch len(parts) {
			case 1:
				if parts[0] == "quit" {
					clients.Range(func(k, v interface{}) bool {
						v.(conn.Conn).Close()
						return true
					})
					ln.Close()
					return
				}
			case 2:
				// 1. close clientID
				// 2. open clientID
				client := parts[1]
				clientID, err := strconv.ParseUint(client, 10, 64)
				if err != nil {
					log.Println("illegal client id", err, client)
					continue
				}
				if parts[0] == "close" {
					// close client
					client, ok := clients.Load(clientID)
					if !ok {
						log.Printf("client id: %d not found\n", clientID)
						continue
					}
					// all dialogues from client must be closed
					client.(conn.Conn).Close()
					clients.Delete(clientID)
					continue

				} else if parts[0] == "open" {
					// open dialogue for a client
					sm, ok := sms.Load(clientID)
					if !ok {
						log.Printf("client id: %d not found\n", clientID)
						continue
					}
					sn, err := sm.(multiplexer.Multiplexer).OpenDialogue([]byte("austin zhai"))
					if err != nil {
						log.Printf("client id: %d open dialogue err: %s\n", clientID, err)
						continue
					}
					snID := strconv.FormatUint(sn.ClientID(), 10) + "-" + "1"
					sns.Store(snID, sn)
					continue
				}
			case 3:
				// 1. close clientID dialogueID
				client := parts[1]
				dialogue := parts[2]
				if parts[0] == "close" {
					snID := client + "-" + dialogue
					sn, ok := sns.Load(snID)
					if !ok {
						log.Printf("dialogue id: %s not found\n", dialogue)
						continue
					}
					sn.(multiplexer.Dialogue).Close()
					sns.Delete(snID)
					continue
				}
			case 4:
				// sendto clientID dialogueID xxx
				client := parts[1]
				dialogue := parts[2]
				msg := parts[3]
				if parts[0] == "sendto" {
					snID := client + "-" + dialogue
					sn, ok := sns.Load(snID)
					if !ok {
						log.Printf("dialogue id: %s not found\n", dialogue)
						continue
					}
					pkt := pf.NewMessagePacketWithSessionID(
						sn.(multiplexer.Dialogue).DialogueID(),
						[]byte{}, []byte(msg), []byte{})
					sn.(multiplexer.Dialogue).Write(pkt)
					continue
				}
			}
		}
	}()

	for {
		netconn, err := ln.Accept()
		if err != nil {
			log.Printf("accept err: %s\n", err)
			break
		}
		{
			// connection
			sc, err := conn.NewServerConn(netconn,
				conn.OptionServerConnPacketFactory(pf))
			if err != nil {
				log.Printf("new recvconn err: %s\n", err)
				continue
			}

			// multiplexer
			sm, err := multiplexer.NewMultiplexer(sc,
				multiplexer.OptionMultiplexerAcceptDialogue(),
				multiplexer.OptionMultiplexerClosedDialogue(),
				multiplexer.OptionTimer(tmr),
				multiplexer.OptionPacketFactory(pf))

			if err != nil {
				log.Println("new dialogue manager err:", err)
				return
			}

			clients.Store(sc.ClientID(), sc)
			// store the default dialogue
			sms.Store(sc.ClientID(), sm)
			sn := sm.ListDialogues()[0]
			snID := strconv.FormatUint(sn.ClientID(), 10) + "-" + "1"
			sns.Store(snID, sn)
			handleInput(sn)
			// handle multiplexer
			handleAcceptClosedDialogue(sm)
		}
	}
	time.Sleep(time.Second)
}

func handleAcceptClosedDialogue(sm multiplexer.Multiplexer) {
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
