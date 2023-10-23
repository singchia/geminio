package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/sigaction"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/delegate"
	"github.com/singchia/geminio/examples/mq/share"
)

var (
	end    geminio.End
	pprof  *string
	broker *string
	topic  *string
	level  *string
)

type FakeClient struct {
	*delegate.UnimplementedDelegate
}

func (client *FakeClient) EndReOnline(delegate.ClientDescriber) {
	if end != nil {
		// reconnect
		role := &share.Claim{
			Role:  "consumer",
			Topic: *topic,
		}
		data, _ := json.Marshal(role)
		_, err := end.Call(context.TODO(), "claim", end.NewRequest(data))
		if err != nil {
			log.Errorf("call err: %s after reconnect", err)
		}
	}
}

func main() {
	pprof = flag.String("pprof", "", "pprof address to listen")
	broker = flag.String("broker", "127.0.0.1:1202", "broker to dial")
	topic = flag.String("topic", "test", "topic to produce to broker")
	level = flag.String("level", "info", "trace, debug, info, warn, error")

	flag.Parse()

	if *pprof != "" {
		go func() {
			http.ListenAndServe(*pprof, nil)
		}()
	}
	lvl, err := log.ParseLevel(*level)
	if err != nil {
		log.Errorf("parse log level err: %s", err)
		return
	}
	log := log.NewLog()
	log.SetLevel(lvl)

	// new producer
	dialer := func() (net.Conn, error) {
		return net.Dial("tcp", *broker)
	}
	fc := &FakeClient{
		UnimplementedDelegate: &delegate.UnimplementedDelegate{},
	}
	opt := client.NewRetryEndOptions()
	opt.SetLog(log)
	opt.SetWaitRemoteRPCs("claim")
	opt.SetDelegate(fc)
	end, err = client.NewRetryEndWithDialer(dialer, opt)
	if err != nil {
		log.Errorf("new end err: %s", err)
		return
	}
	// claim the role and topic
	role := &share.Claim{
		Role:  "consumer",
		Topic: *topic,
	}
	data, _ := json.Marshal(role)
	_, err = end.Call(context.TODO(), "claim", end.NewRequest(data))
	if err != nil {
		log.Errorf("call claim err: %s", err)
		return
	}

	go func() {
		// consumer
		for {
			msg, err := end.Receive(context.TODO())
			if err != nil {
				log.Errorf("end receive err: %s", err)
				continue
			}
			msg.Done()
			fmt.Println(">", string(msg.Data()))
		}
	}()

	sig := sigaction.NewSignal()
	sig.Wait(context.TODO())
	end.Close()
}
