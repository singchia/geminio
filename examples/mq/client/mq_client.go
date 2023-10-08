package main

import (
	"flag"
	"net/http"

	"github.com/jumboframes/armorigo/log"
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

	// new producer
}
