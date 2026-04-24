package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/sigaction"
	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/examples/chatroom/share"
)

func main() {
	pprof := flag.String("pprof", "", "pprof address to listen")
	addr := flag.String("addr", "127.0.0.1:1202", "chatroom address to connect")
	level := flag.String("level", "info", "trace, debug, info, warn, error")
	username := flag.String("username", "", "username to identify user")
	flag.Parse()

	if *pprof != "" {
		go func() {
			http.ListenAndServe(*pprof, nil)
		}()
	}
	if *username == "" {
		log.Error("usename not supported")
		return
	}
	lvl, err := log.ParseLevel(*level)
	if err != nil {
		log.Errorf("parse log level err: %s", err)
		return
	}
	// global log
	log.SetLevel(lvl)
	// end options
	opt := client.NewEndOptions()
	opt.SetLog(log.DefaultLog)
	opt.SetMeta([]byte(*username))
	end, err := client.NewEnd("tcp", *addr, opt)
	if err != nil {
		log.Errorf("new end err: %s", err)
		return
	}

	go func() {
		fmt.Print("> ")
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			text := scanner.Text()
			fmt.Print("> ")
			err = end.Publish(context.TODO(), end.NewMessage([]byte(text)))
			if err != nil {
				if err == io.EOF {
					break
				}
				log.Errorf("publish err: %s", err)
				continue
			}
		}
		fmt.Println("end.")
	}()

	go func() {
		for {
			msg, err := end.Receive(context.TODO())
			if err != nil {
				return
			}
			ud := &share.UserData{}
			err = json.Unmarshal(msg.Data(), ud)
			if err != nil {
				log.Errorf("unmarshal err: %s", err)
				continue
			}
			fmt.Printf("\n[%s]: %s\n", ud.Username, string(ud.Data))
			fmt.Print("> ")
			msg.Done()
		}
	}()

	sig := sigaction.NewSignal()
	sig.Wait(context.TODO())
	end.Close()
}
