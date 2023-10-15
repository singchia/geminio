package main

import (
	"context"
	"flag"
	"net/http"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/sigaction"
	"github.com/singchia/geminio/server"
)

func main() {
	pprof := flag.String("pprof", "", "pprof address to listen")
	broker := flag.String("broker", "127.0.0.1:1202", "broker to dial")
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
	// broker
	b := NewBroker()

	// accept ends
	opt := server.NewEndOptions()
	opt.SetLog(log)
	ln, err := server.Listen("tcp", *broker, opt)
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
			log.Debugf("accept end: %v", end.ClientID())
			go b.Handle(end)
		}
	}()
	sig := sigaction.NewSignal()
	sig.Wait(context.TODO())

	ln.Close()
}
