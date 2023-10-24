package main

import (
	"context"
	"flag"
	"net/http"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/sigaction"
	"github.com/singchia/geminio/server"
	"github.com/singchia/go-timer/v2"
)

func main() {
	pprof := flag.String("pprof", "", "pprof address to listen")
	broker := flag.String("broker", "127.0.0.1:1202", "broker to dial")
	buffer := flag.Int("buffer", 8, "topic buffer")
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
	// global log
	log.SetLevel(lvl)

	// broker
	b := NewBroker(*buffer)

	// log for geminio
	glog := log.NewLog()
	glog.SetLevel(lvl)
	// timer
	tmr := timer.NewTimer()
	// accept ends
	opt := server.NewEndOptions()
	opt.SetLog(glog)
	opt.SetTimer(tmr)
	ln, err := server.Listen("tcp", *broker, opt)
	if err != nil {
		log.Errorf("server listen err: %s", err)
		return
	}

	// serve
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
