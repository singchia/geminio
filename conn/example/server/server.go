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

	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
	"github.com/sirupsen/logrus"
)

type server struct {
	idFactory id.IDFactory
}

func newServer() *server {
	idFactory := id.NewIDCounter(id.Unique)
	return &server{
		idFactory: idFactory,
	}
}

func (s *server) Online(clientID uint64, meta []byte, addr net.Addr) error            { return nil }
func (s *server) Offline(clientID uint64, meta []byte, addr net.Addr) error           { return nil }
func (s *server) RemoteRegistration(method string, clientID uint64, sessionID uint64) {}
func (s *server) Heartbeat(clientID uint64, meta []byte, addr net.Addr) error         { return nil }
func (s *server) GetClientIDByMeta(meta []byte) (uint64, error) {
	return s.idFactory.GetID(), nil
}

func main() {
	network := flag.String("network", "tcp", "network to listen")
	address := flag.String("address", "127.0.0.1:1202", "address to listen")
	flag.Parse()

	ln, err := net.Listen(*network, *address)
	if err != nil {
		log.Println(err)
		return
	}

	pf := packet.NewPacketFactory(id.NewIDCounter(id.Even))
	clients := sync.Map{}

	// close 1
	// sendto 1 msg
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			text := scanner.Text()
			index := strings.Index(text, " ")
			if index != -1 && index < len(text) {
				command := text[:index]
				text = text[index+1:]

				switch command {
				case "close":
					clientId, err := strconv.ParseUint(text, 10, 64)
					if err != nil {
						log.Println("illegal id", err)
						continue
					}
					rc, ok := clients.Load(clientId)
					if !ok {
						log.Printf("clientId not found '%d'\n", clientId)
						continue
					}
					rc.(*conn.RecvConn).Close()
					clients.Delete(clientId)
					continue

				case "sendto":
					index = strings.Index(text, " ")
					if index > -1 && index < len(text) {
						clientId, err := strconv.ParseUint(text[:index], 10, 64)
						if err != nil {
							log.Println("illegal id", err)
							continue
						}
						text = text[index+1:]
						rc, ok := clients.Load(clientId)
						if !ok {
							log.Println("clientId not found", clientId)
							continue
						}
						pkt := pf.NewMessagePacket([]byte{}, []byte(text), []byte{})
						err = rc.(*conn.RecvConn).Write(pkt)
						if err != nil {
							log.Println("write err:", err)
						}
					}
				}
			}
		}
	}()

	logrusLog := logrus.New()
	logrusLog.SetLevel(logrus.DebugLevel)
	server := newServer()

	for {
		netconn, err := ln.Accept()
		if err != nil {
			log.Printf("accept err: %s", err)
			break
		}
		rc, err := conn.NewRecvConn(netconn, conn.OptionRecvConnPacketFactory(pf),
			conn.OptionRecvConnDelegate(server))
		if err != nil {
			log.Printf("new recvconn err: %s", err)
			continue
		}
		clients.Store(rc.ClientID(), rc)

		go func() {
			for {
				pkt, err := rc.Read()
				if err != nil {
					log.Println("read error", err)
					return
				}
				msg := pkt.(*packet.MessagePacket)
				log.Println(rc.ClientID(), string(msg.MessageData.Key), string(msg.MessageData.Value))
			}
		}()
	}
}
