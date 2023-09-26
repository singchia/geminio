package main

import (
	"context"
	"flag"
	"net/http"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/sigaction"
	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio/examples/messager/share"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/geminio/server"
	"github.com/singchia/go-timer/v2"
)

var (
	tmr       timer.Timer
	syncHub   *synchub.SyncHub
	idCounter *id.IDCounter
)

func main() {
	pprof := flag.String("pprof", "", "pprof address to listen")
	network := flag.String("network", "tcp", "network to listen")
	address := flag.String("address", "127.0.0.1:1202", "address to listen")
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
	opt := server.NewEndOptions()
	opt.SetLog(log)
	ln, err := server.Listen(*network, *address, opt)
	if err != nil {
		log.Errorf("server listen err: %s", err)
		return
	}

	go func() {
		for {
			end, err := ln.Accept()
			if err != nil {
				log.Errorf("accept err: %s", err)
				break
			}
			go share.Receive(end)
			go share.Publish(end, *count)
		}
	}()

	sig := sigaction.NewSignal()
	sig.Wait(context.TODO())

	ln.Close()
}
