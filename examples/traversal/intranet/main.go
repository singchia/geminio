package main

import (
	"flag"
	"net/http"

	"github.com/jumboframes/armorigo/log"
)

var (
	pprof    *string
	level    *string
	tunnel   *string
	intranet *string
)

func main() {
	pprof = flag.String("pprof", "", "pprof address to listen")
	level = flag.String("level", "info", "trace, debug, info, warn, error")
	tunnel = flag.String("tunnel", "", "tunnel address to connect")
	intranet = flag.String("intranet", "", "intranet address to proxy")
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
}
