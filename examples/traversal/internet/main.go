package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	_ "net/http/pprof"
	"sync"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/rproxy"
	"github.com/jumboframes/armorigo/sigaction"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/server"
)

var (
	tunnel   *string
	internet *string
	pprof    *string
	level    *string
)

var (
	ends    = map[uint64]geminio.End{}
	endsMtx sync.RWMutex
)

func main() {
	internet = flag.String("internet", "0.0.0.0:22", "internet address to listen and proxy to tunnel")
	tunnel = flag.String("tunnel", "0.0.0.0:2432", "tunnel address to listen for intranet")
	pprof = flag.String("pprof", "", "pprof address to listen")
	level = flag.String("level", "info", "trace, debug, info, warn, error")
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

	// internet
	internetln, err := net.Listen("tcp", *internet)
	if err != nil {
		log.Errorf("net listen internet addr: %s err: %s", *internet, err)
		return
	}

	proxy, err := rproxy.NewRProxy(internetln,
		rproxy.OptionRProxyDial(dial),
		rproxy.OptionRProxyWaitOnErr(3*time.Second))
	if err != nil {
		log.Errorf("new rproxy err: %s", err)
		return
	}
	go proxy.Proxy(context.TODO())

	// tunnel
	tunnelln, err := server.Listen("tcp", *tunnel)
	if err != nil {
		log.Errorf("net listen tunnel addr: %s err: %s", *tunnel, err)
		return
	}
	go func() {
		for {
			end, err := tunnelln.AcceptEnd()
			if err != nil {
				log.Errorf("tunnel accept end err: %s", err)
				break
			}
			endsMtx.Lock()
			ends[end.ClientID()] = end
			endsMtx.Unlock()

			go func() {
				for {
					msg, err := end.Receive(context.TODO())
					if err != nil {
						if err == io.EOF {
							break
						}
						log.Errorf("end receive err: %s", err)
						continue
					}
					msg.Done()
				}
				endsMtx.Lock()
				delete(ends, end.ClientID())
				endsMtx.Unlock()
			}()
		}
	}()

	sig := sigaction.NewSignal()
	sig.Wait(context.TODO())

	internetln.Close()
	proxy.Close()
	tunnelln.Close()
	foreachEnd(func(end geminio.End) {
		end.Close()
	})
}

func picEnd() geminio.End {
	endsMtx.RLock()
	defer endsMtx.RUnlock()

	for _, end := range ends {
		return end
	}
	return nil
}

func foreachEnd(fun func(geminio.End)) {
	endsMtx.RLock()
	defer endsMtx.RUnlock()

	for _, end := range ends {
		fun(end)
	}
}

func dial(dst net.Addr, custom interface{}) (net.Conn, error) {
	end := picEnd()
	if end == nil {
		return nil, errors.New("no available tunnel end")
	}
	return end.OpenStream()
}
