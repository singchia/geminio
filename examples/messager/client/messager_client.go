package main

import (
	"context"
	"flag"
	"net"
	"net/http"
	_ "net/http/pprof"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/sigaction"

	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/examples/messager/share"
)

func main() {
	pprof := flag.String("pprof", "", "pprof address to listen")
	network := flag.String("network", "tcp", "network to dial")
	address := flag.String("address", "127.0.0.1:1202", "address to dial")
	level := flag.String("level", "info", "trace, debug, info, warn, error")
	count := flag.Int("count", 10, "message count")

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

	// new client
	dialer := func() (net.Conn, error) {
		return net.Dial(*network, *address)
	}
	opt := client.NewEndOptions()
	opt.SetLog(log)
	end, err := client.NewEndWithDialer(dialer, opt)
	if err != nil {
		log.Errorf("new end err: %s", err)
		return
	}
	go share.Receive(end)
	go share.Publish(end, *count)

	sig := sigaction.NewSignal()
	sig.Wait(context.TODO())

	end.Close()
}
