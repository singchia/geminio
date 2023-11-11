package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"sync"

	"github.com/jumboframes/armorigo/log"
	"github.com/jumboframes/armorigo/sigaction"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/delegate"
	"github.com/singchia/geminio/examples/chatroom/share"
	"github.com/singchia/geminio/pkg/id"
	"github.com/singchia/geminio/server"
	"github.com/singchia/go-timer/v2"
)

var (
	room *ChatRoom
)

type ChatRoom struct {
	*delegate.UnimplementedDelegate

	idFactory id.IDFactory

	mtx     *sync.RWMutex
	clients map[string]geminio.End
}

func NewChatRoom() *ChatRoom {
	return &ChatRoom{
		UnimplementedDelegate: &delegate.UnimplementedDelegate{},
		idFactory:             id.DefaultIncIDCounter,
		mtx:                   new(sync.RWMutex),
		clients:               map[string]geminio.End{},
	}
}

func (room *ChatRoom) GetClientID(meta []byte) (uint64, error) {
	room.mtx.RLock()
	defer room.mtx.RUnlock()

	_, ok := room.clients[string(meta)]
	if !ok {
		return 0, errors.New("another one onlined")
	}
	return room.idFactory.GetID(), nil
}

func (room *ChatRoom) ConnOnline(client delegate.ConnDescriber) error {
	user := string(client.Meta())
	// notify all users
	ud := &share.UserData{
		Username: "system",
		Data:     []byte(fmt.Sprintf("%s joined room", user)),
	}
	data, err := json.Marshal(ud)
	if err != nil {
		log.Errorf("json marshal err: %s", err)
		return err
	}
	room.foreachUser(func(end geminio.End) {
		end.Publish(context.TODO(), end.NewMessage(data))
	}, user)
	return nil
}

func (room *ChatRoom) ConnOffline(client delegate.ConnDescriber) error {
	user := string(client.Meta())
	room.delUser(string(user))
	// notify all users
	ud := &share.UserData{
		Username: "system",
		Data:     []byte(fmt.Sprintf("%s left room", user)),
	}
	data, err := json.Marshal(ud)
	if err != nil {
		log.Errorf("json marshal err: %s", err)
		return err
	}
	room.foreachUser(func(end geminio.End) {
		end.Publish(context.TODO(), end.NewMessage(data))
	})
	return nil
}

func (room *ChatRoom) addUser(name string, end geminio.End) error {
	room.mtx.Lock()
	defer room.mtx.Unlock()

	_, ok := room.clients[name]
	if ok {
		return errors.New("duplicated user")
	}
	room.clients[name] = end
	return nil
}

func (room *ChatRoom) delUser(name string) {
	room.mtx.Lock()
	defer room.mtx.Unlock()

	delete(room.clients, name)
}

func (room *ChatRoom) foreachUser(fun func(end geminio.End), excepts ...string) {
	room.mtx.RLock()
	defer room.mtx.RUnlock()

	for user, end := range room.clients {
		jumpover := false
		for _, elem := range excepts {
			if user == elem {
				jumpover = true
				break
			}
		}
		if !jumpover {
			fun(end)
		}
	}
}

func main() {
	pprof := flag.String("pprof", "", "pprof address to listen")
	addr := flag.String("addr", "127.0.0.1:1202", "chatroom address to listen")
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
	// chatroom
	room = NewChatRoom()
	// log for geminio
	glog := log.NewLog()
	glog.SetLevel(lvl)
	// timer
	tmr := timer.NewTimer()
	// accept ends
	opt := server.NewEndOptions()
	opt.SetLog(glog)
	opt.SetTimer(tmr)
	opt.SetDelegate(room)
	ln, err := server.Listen("tcp", *addr, opt)
	if err != nil {
		log.Errorf("server listen err: %s", err)
		return
	}
	// serve
	go func() {
		for {
			end, err := ln.AcceptEnd()
			if err != nil {
				log.Errorf("accept err: %s", err)
				break
			}
			room.addUser(string(end.Meta()), end)
			log.Infof("accept meta: %v, clientID: %v", string(end.Meta()), end.ClientID())
			go handle(end)
		}
	}()
	sig := sigaction.NewSignal()
	sig.Wait(context.TODO())
	ln.Close()
}

func handle(end geminio.End) {
	for {
		msg, err := end.Receive(context.TODO())
		if err != nil {
			break
		}
		msg.Done()
		ud := &share.UserData{
			Username: string(end.Meta()),
			Data:     msg.Data(),
		}
		data, err := json.Marshal(ud)
		if err != nil {
			log.Errorf("json marshal err: %s", err)
			continue
		}
		// for each user except self
		room.foreachUser(func(end geminio.End) {
			end.Publish(context.TODO(), end.NewMessage(data))
		}, string(end.Meta()))
	}
}
