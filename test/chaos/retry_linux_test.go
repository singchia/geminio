//go:build linux

package chaos

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/server"
	"github.com/singchia/go-xtables/iptables"
	"github.com/singchia/go-xtables/pkg/network"
)

func TestPacketDrop(t *testing.T) {
	port := 12345
	ln, err := server.Listen("tcp", "localhost:"+strconv.Itoa(port))
	if err != nil {
		log.Errorf("net listen err: %s", err)
		return
	}
	count := int32(10)
	index := int32(0)

	go func() {
		// server
		for {
			end, err := ln.AcceptEnd()
			if err != nil {
				log.Errorf("accept end err: %s", err)
				break
			}
			go func(end geminio.End) {
				for {
					msg, err := end.Receive(context.TODO())
					if err != nil {
						if err == io.EOF {
							break
						}
						log.Errorf("end receive err: %s", err)
						continue
					}
					msg.Done()
					value := atomic.AddInt32(&index, 1)
					if value == count {
						return
					}
				}
			}(end)
		}
	}()

	// retry client
	dialer := func() (net.Conn, error) {
		return net.Dial("tcp", "localhost:"+strconv.Itoa(port))
	}
	end, err := client.NewRetryEndWithDialer(dialer)
	if err != nil {
		log.Errorf("new retry end err: %s", err)
		return
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		dropDportPacket(port, 10)
	}()
	for i := 0; i < int(count); i++ {
		for {
			err := end.Publish(context.TODO(), end.NewMessage([]byte("retry test")))
			if err != nil {
				time.Sleep(time.Second)
				// continue publish until success
				continue
			}
		}
	}
	wg.Wait()
}

func dropDportPacket(dport int, wait int) error {
	ipt := iptables.NewIPTables().Table(iptables.TableTypeFilter).
		Chain(iptables.ChainTypeINPUT).MatchProtocol(false, network.ProtocolTCP).
		MatchTCP(iptables.WithMatchTCPDstPort(false, dport)).TargetDrop()

	err := ipt.Insert()
	if err != nil {
		return err
	}

	tick := time.NewTicker(time.Duration(wait) * time.Second)
	<-tick.C
	err = ipt.Delete()
	return err
}
