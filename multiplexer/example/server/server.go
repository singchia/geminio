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
	network := flag.String("network", "tcp", "network to listen")
	address := flag.String("address", "127.0.0.1:1202", "address to listen")
	flag.Parse()

	ln, err := net.Listen(*network, *address)
	if err != nil {
		log.Println(err)
		return
	}

	tmr := timer.NewTimer()

	clients := sync.Map{}
	sms := sync.Map{}
	sns := sync.Map{}

	// open clientID
	// close clientID
	// close clientID dialogueID
	// sendto dialogueID msg
	// quit
	pf := packet.NewPacketFactory(id.NewIDCounter(id.Even))
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			text := scanner.Text()
			index := strings.Index(text, " ")
			if index != -1 && index < len(text) {
				command := text[:index]
				text = text[index+1:]

				switch command {
				case "open":
					clientId, err := strconv.ParseUint(text, 10, 64)
					if err != nil {
						log.Println("illegal id", err, text)
						continue
					}
					sm, ok := sms.Load(clientId)
					if !ok {
						log.Printf("clientId not found '%d'\n", clientId)
						continue
					}
					sn, err := sm.(multiplexer.Multiplexer).OpenDialogue([]byte("austin zhai"))
					if err != nil {
						log.Printf("clientId: %d open session err: %s\n", clientId, err)
						continue
					}
					sns.Store(sn.DialogueID(), sn)
					continue

				case "close":
					index = strings.Index(text, " ")
					if index == -1 {
						// close client
						clientId, err := strconv.ParseUint(text, 10, 64)
						if err != nil {
							log.Println("illegal id", err, text)
							continue
						}
						client, ok := clients.Load(clientId)
						if !ok {
							log.Printf("clientId not found '%d'\n", clientId)
							continue
						}
						client.(conn.Conn).Close()
						clients.Delete(clientId)
						continue
					}
					text = text[index+1:]
					// close session
					dialogueID, err := strconv.ParseUint(text, 10, 64)
					if err != nil {
						log.Println("illegal id", err, text)
						continue
					}
					sn, ok := sns.Load(dialogueID)
					if !ok {
						log.Printf("dialogueID not found '%d'\n", dialogueID)
						continue
					}
					sn.(multiplexer.Dialogue).Close()
					continue

				case "sendto":
					index = strings.Index(text, " ")
					if index == -1 {
						log.Println("msg not found")
						continue
					}
					//text = text[index+1:]
					// session
					dialogueID, err := strconv.ParseUint(text[:index], 10, 64)
					if err != nil {
						log.Println("illegal id", err, text)
						continue
					}
					sn, ok := sns.Load(dialogueID)
					if !ok {
						log.Printf("dialogueID not found '%d'\n", dialogueID)
						continue
					}
					pkt := pf.NewMessagePacket([]byte{}, []byte(text[index+1:]), []byte{})
					pkt.SessionID = sn.(multiplexer.Dialogue).DialogueID()
					sn.(multiplexer.Dialogue).Write(pkt)
					continue

				case "quit":
					clients.Range(func(k, v interface{}) bool {
						v.(conn.Conn).Close()
						return true
					})
					ln.Close()
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
				log.Println("new session manager err:", err)
				return
			}

			clients.Store(sc.ClientID(), sc)
			sms.Store(sc.ClientID(), sm)

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
							log.Println("> ", sc.ClientID(), msg.SessionID, string(msg.MessageData.Value))
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
		}
	}
	time.Sleep(time.Second)
}
