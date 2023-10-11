package main

import (
	"context"
	"encoding/json"
	"errors"
	_ "net/http/pprof"
	"sync"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/examples/mq/share"
)

type roleEnd struct {
	role  string
	topic string
	end   geminio.End
}

type Broker struct {
	mtx *sync.RWMutex
	// all clients
	clients map[uint64]*roleEnd

	// producer
	producerTopics map[string]chan string // key: topic, value: channel for messages

	// consumer
	consumerTopics map[string]map[uint64]chan string // key: topic, subKey: clientID, value: channel for messages

	// syncer
	syncers map[string]chan struct{} // key: topic, value: quit channel
}

func NewBroker() *Broker {
	b := &Broker{
		mtx:            new(sync.RWMutex),
		clients:        map[uint64]*roleEnd{},
		producerTopics: map[string]chan string{},
		consumerTopics: map[string]map[uint64]chan string{},
		syncers:        map[string]chan struct{}{},
	}
	return b
}

func (broker *Broker) Handle(end geminio.End) error {
	broker.mtx.Lock()
	broker.clients[end.ClientID()] = &roleEnd{
		end: end,
	}
	broker.mtx.Unlock()

	err := end.Register(context.TODO(), "claim", broker.claim)
	if err != nil {
		log.Errorf("end register function err: %s", err)
		return err
	}
	for {
		msg, err := end.Receive(context.TODO())
		if err != nil {
			log.Errorf("end receive err: %s", err)
			break
		}
		log.Debugf("end receive msg: %s", string(msg.Data()))
		broker.mtx.RLock()
		client, ok := broker.clients[msg.ClientID()]
		if !ok {
			log.Errorf("client not found while receive msg")
			broker.mtx.RUnlock()
			continue
		}

		ch, ok := broker.producerTopics[client.topic]
		select {
		case ch <- string(msg.Data()):
			msg.Done()
		default:
			msg.Error(errors.New("broker full"))
		}
		broker.mtx.RUnlock()
	}
	// destory the end
	// the consumer and producer will end here
	broker.mtx.Lock()
	client, ok := broker.clients[end.ClientID()]
	if ok {
		delete(broker.clients, client.end.ClientID())
		// remove the consumer
		consumers, ok := broker.consumerTopics[client.topic]
		if ok {
			for k, v := range consumers {
				if k == end.ClientID() {
					// to make the consumer quit
					close(v)
					delete(consumers, end.ClientID())
				}
			}
			if len(consumers) == 0 {
				// no such topic consumer, end the syncer
				syncer, ok := broker.syncers[client.topic]
				if ok {
					close(syncer)
					delete(broker.syncers, client.topic)
				}
			}
		}
	}
	broker.mtx.Unlock()
	return err
}

func (broker *Broker) claim(ctx context.Context, req geminio.Request, rsp geminio.Response) {
	claim := &share.Claim{}
	_ = json.Unmarshal(req.Data(), claim)
	clientID := req.ClientID()

	switch claim.Role {
	case "producer":
		broker.mtx.Lock()
		client, ok := broker.clients[req.ClientID()]
		if !ok {
			log.Errorf("client not found")
			broker.mtx.Unlock()
			return
		}
		client.role = claim.Role
		client.topic = claim.Topic

		// initial producer tpoic buffer
		_, ok = broker.producerTopics[claim.Topic]
		if !ok {
			broker.producerTopics[claim.Topic] = make(chan string, 1024)
		}
		broker.mtx.Unlock()
	case "consumer":
		broker.mtx.Lock()
		client, ok := broker.clients[clientID]
		if !ok {
			log.Errorf("client not found")
			broker.mtx.Unlock()
			return
		}
		client.role = claim.Role
		client.topic = claim.Topic

		// initial producer topic buffer
		_, ok = broker.producerTopics[claim.Topic]
		if !ok {
			broker.producerTopics[claim.Topic] = make(chan string, 1024)
		}
		// initial consumer topic buffer
		consumers, ok := broker.consumerTopics[claim.Topic]
		if !ok {
			consumers = map[uint64]chan string{}
			broker.consumerTopics[claim.Topic] = consumers
		}
		msgCh := make(chan string, 1024)
		consumers[req.ClientID()] = msgCh
		// consumer msg to end
		go func() {
			for {
				select {
				case msg := <-msgCh:
					broker.mtx.RLock()
					client, ok := broker.clients[clientID]
					if ok {
						client.end.Publish(context.TODO(), client.end.NewMessage([]byte(msg)))
					}
					broker.mtx.RUnlock()
				}
			}
		}()
		// syncer goroutine
		broker.syncer(claim.Topic)
		broker.mtx.Unlock()
	}
}

// if no consumer, the syncer will quit
func (broker *Broker) syncer(topic string) {
	_, ok := broker.syncers[topic]
	if ok {
		return
	}
	closeCh := make(chan struct{})
	broker.syncers[topic] = closeCh

	msgCh, ok := broker.producerTopics[topic]
	go func() {
		for {
			select {
			case msg := <-msgCh:
				// sync to all consumers
				broker.mtx.RLock()
				clients, ok := broker.consumerTopics[topic]
				if ok {
					for _, v := range clients {
						v <- msg
					}
				}
				broker.mtx.RUnlock()
			case <-closeCh:
				return
			}
		}
	}()
}
