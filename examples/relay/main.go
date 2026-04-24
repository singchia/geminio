package main

import (
	"context"
	"flag"
	"net"
	"net/http"
	_ "net/http/pprof"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/rproxy"
	"github.com/jumboframes/armorigo/sigaction"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/server"
)

var (
	pprof    *string
	level    *string
	in       *string
	relayIn  *string
	out      *string
	relayOut *string
)

var (
	end geminio.End
)

func main() {
	pprof = flag.String("pprof", "", "pprof address to listen")
	level = flag.String("level", "info", "trace, debug, info, warn, error")
	in = flag.String("in", "", "in address to listen and relay, format as addr:port")
	relayIn = flag.String("relay_in", "", "relay in address to listen and relay, format as addr:port")
	out = flag.String("out", "", "out address to relay, default empty, format as addr:port, mutually exclusive with realy_out")
	relayOut = flag.String("relay_out", "", "next relay address, default empty, format as addr:port, mutually exclusive with out")

	flag.Parse()

	if *out == "" && *relayOut == "" {
		log.Errorf("out and relay_out empty")
		return
	}
	if *out != "" && *relayOut != "" {
		log.Errorf("out conflicts with relay_out")
	}
	if *in == "" && *relayIn == "" {
		log.Errorf("in and relay_in empty")
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

	if *in != "" {
		rawln, err := net.Listen("tcp", *in)
		if err != nil {
			log.Errorf("net listen addr: %s err: %s", *in, err)
			return
		}
		inProxy, err := rproxy.NewRProxy(rawln, rproxy.OptionRProxyDial(dial))
		if err != nil {
			log.Errorf("in proxy err: %s", err)
			return
		}
		go inProxy.Proxy(context.TODO())
	}

	if *relayIn != "" {
		relayln, err := server.Listen("tcp", *relayIn)
		if err != nil {
			log.Errorf("server listen addr: %s err: %s", *relayIn, err)
			return
		}
		// serve
		go func() {
			for {
				end, err := relayln.AcceptEnd()
				if err != nil {
					log.Errorf("accept err: %s", err)
					break
				}
				log.Debugf("accept end: %v", end.ClientID())
				relayProxy, err := rproxy.NewRProxy(end, rproxy.OptionRProxyDial(dial))
				if err != nil {
					log.Errorf("relay proxy err: %s", err)
					return
				}
				go relayProxy.Proxy(context.TODO())
			}
		}()
	}

	if *relayOut != "" {
		end, err = client.NewEnd("tcp", *relayOut)
		if err != nil {
			log.Errorf("client new end addr: %s err: %s", *relayOut, err)
		}
	}

	sig := sigaction.NewSignal()
	sig.Wait(context.TODO())
	if end != nil {
		end.Close()
	}
}

func dial(dst net.Addr, custom interface{}) (net.Conn, error) {
	if *out != "" {
		return net.Dial("tcp", *out)
	}
	return end.OpenStream()
}
