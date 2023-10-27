package main

import (
	"flag"
	"net"
	"net/http"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/rproxy"
)

func main() {
	pprof := flag.String("pprof", "", "pprof address to listen")
	in := flag.String("in", "0.0.0.0:65522", "in address to listen and relay")
	relayIn := flag.String("relay_in", "", "relay in address to listen and relay")
	out := flag.String("out", "", "out address to relay")
	relayNext := flag.String("relay_next", "127.0.0.1:2433", "next relay address")
	level := flag.String("level", "info", "trace, debug, info, warn, error")
	flag.Parse()

	if *out == "" && *relayNext == "" {
		return
	}

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

	rawln, err := net.Listen("tcp", *in)
	if err != nil {
		log.Errorf("net listen addr: %s err: %s", *in, err)
		return
	}

	relayln, err := net.Listen("tcp", *relayIn)
	if err != nil {
		log.Errorf("net listen addr: %s err: %s", *relayIn, err)
		return
	}

	rproxy.NewRProxy(rawln, rproxy.OptionRProxyDial(dialRaw))

	rproxy.NewRProxy(relayln, rproxy.OptionRProxyDial(dialRaw))
}

func dialRaw(dst net.Addr, custom interface{}) (net.Conn, error) {
	return nil, nil
}
