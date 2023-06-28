package main

import (
	"bufio"
	"flag"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/conn"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
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

func (s *server) ConnOnline(_ conn.ConnDescriber) error  { return nil }
func (s *server) ConnOffline(_ conn.ConnDescriber) error { return nil }
func (s *server) Heartbeat(_ conn.ConnDescriber) error   { return nil }
func (s *server) GetClientIDByMeta(meta []byte) (uint64, error) {
	return s.idFactory.GetID(), nil
}

func forceFree() {
	for _ = range time.Tick(30 * time.Second) {
		debug.FreeOSMemory()
	}
}

func init() {
	go forceFree()
}

func main() {

	go func() {
		http.ListenAndServe("0.0.0.0:6061", nil)
	}()

	network := flag.String("network", "tcp", "network to listen")
	address := flag.String("address", "127.0.0.1:1202", "address to listen")
	loglevel := flag.Int("loglevel", 3, "1: trace, 2: debug, 3: info, 4: warn, 5: error")
	flag.Parse()

	log.SetLevel(log.Level(*loglevel))

	ln, err := net.Listen(*network, *address)
	if err != nil {
		log.Error(err)
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
					clientID, err := strconv.ParseUint(text, 10, 64)
					if err != nil {
						log.Error("illegal id", err)
						continue
					}
					rc, ok := clients.Load(clientID)
					if !ok {
						log.Error("clientID not found '%d'\n", clientID)
						continue
					}
					rc.(*conn.ServerConn).Close()
					clients.Delete(clientID)
					continue

				case "sendto":
					index = strings.Index(text, " ")
					if index > -1 && index < len(text) {
						clientID, err := strconv.ParseUint(text[:index], 10, 64)
						if err != nil {
							log.Error("illegal id", err)
							continue
						}
						text = text[index+1:]
						rc, ok := clients.Load(clientID)
						if !ok {
							log.Error("clientID not found", clientID)
							continue
						}
						pkt := pf.NewMessagePacket([]byte{}, []byte(text), []byte{})
						err = rc.(*conn.ServerConn).Write(pkt)
						if err != nil {
							log.Error("write err:", err)
						}
					}
				}
			}
		}
	}()

	server := newServer()

	for {
		netconn, err := ln.Accept()
		if err != nil {
			log.Error("accept err: %s", err)
			break
		}
		go func(netconn net.Conn) {
			rc, err := conn.NewServerConn(netconn,
				conn.OptionServerConnPacketFactory(pf),
				conn.OptionServerConnDelegate(server))
			if err != nil {
				log.Error("new recvconn err: %s", err)
				return
			}
			clients.Store(rc.ClientID(), rc)

			go func(rc *conn.ServerConn) {
				for {
					//defer clients.Delete(rc.ClientID())
					pkt, err := rc.Read()
					if err != nil {
						log.Info("read error", err)
						return
					}
					rc.Write(pkt)
					msg := pkt.(*packet.MessagePacket)
					log.Debug(rc.ClientID(), string(msg.MessageData.Key), string(msg.MessageData.Value))
				}
			}(rc)
		}(netconn)
	}
}
