package main

import (
	"context"
	"flag"
	"io"
	"net"
	"net/http"
	_ "net/http/pprof"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/rproxy"
	"github.com/jumboframes/armorigo/sigaction"
	"github.com/singchia/geminio/client"
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

	if *tunnel == "" || *intranet == "" {
		log.Errorf("tunnel or intranet unsupported")
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

	dialer := func() (net.Conn, error) {
		return net.Dial("tcp", *tunnel)
	}
	end, err := client.NewRetryEndWithDialer(dialer)
	if err != nil {
		log.Errorf("new end err: %s", err)
		return
	}

	proxy, err := rproxy.NewRProxy(end,
		rproxy.OptionRProxyDial(dial),
		rproxy.OptionRProxyQuitOn(io.EOF))
	if err != nil {
		log.Errorf("new proxy err: %s", err)
		return
	}
	go proxy.Proxy(context.TODO())

	sig := sigaction.NewSignal()
	sig.Wait(context.TODO())

	end.Close()
	proxy.Close()
}

func dial(dst net.Addr, custom interface{}) (net.Conn, error) {
	return net.Dial("tcp", *intranet)
}
