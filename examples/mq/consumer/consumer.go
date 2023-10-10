package consumer

import (
	"context"
	"encoding/json"
	"flag"
	"net"
	"net/http"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/sigaction"
	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/examples/mq/share"
)

func main() {
	pprof := flag.String("pprof", "", "pprof address to listen")
	broker := flag.String("broker", "127.0.0.1:1202", "broker to dial")
	topic := flag.String("topic", "test", "topic to produce to broker")
	level := flag.String("level", "info", "trace, debug, info, warn, error")

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
	opt := client.NewEndOptions()
	opt.SetLog(log)
	end, err := client.NewRetryEndWithDialer(dialer, opt)
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

	go func() {
		// consumer
		for {
			msg, err := end.Receive(context.TODO())
			if err != nil {
				log.Errorf("end receive err: %s", err)
				continue
			}
			msg.Done()
			log.Info(">", string(msg.Data()))
		}
	}()

	sig := sigaction.NewSignal()
	sig.Wait(context.TODO())
	end.Close()
}
