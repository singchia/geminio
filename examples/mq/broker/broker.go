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

	// topic buffers
	topics map[string]chan string // key: topic, value: channel for messages
	buffer int

	// consumer
	consumers map[string]map[uint64]chan string // key: topic, subKey: clientID, value: channel for messages

	// syncer
	syncers map[string]chan struct{} // key: topic, value: quit channel
}

func NewBroker(buffer int) *Broker {
	b := &Broker{
		mtx:       new(sync.RWMutex),
		clients:   map[uint64]*roleEnd{},
		topics:    map[string]chan string{},
		consumers: map[string]map[uint64]chan string{},
		syncers:   map[string]chan struct{}{},
		buffer:    buffer,
	}

	return b
}

func (broker *Broker) initTopic(topic string) {
	_, ok := broker.topics[topic]
	if !ok {
		broker.topics[topic] = make(chan string, broker.buffer)
	}
}

func (broker *Broker) getTopic(topic string) chan string {
	ch, ok := broker.topics[topic]
	if !ok {
		return nil
	}
	return ch
}

// consumer
func (broker *Broker) addConsumer(topic string, clientID uint64) chan string {
	log.Debugf("add consumer: %d, topic: %s", clientID, topic)
	topicConsumers, ok := broker.consumers[topic]
	if !ok {
		topicConsumers = map[uint64]chan string{}
		// start topic syncer
		broker.addSyncer(topic)
	}
	ch := make(chan string, 1024)
	topicConsumers[clientID] = ch
	broker.consumers[topic] = topicConsumers
	return ch
}

func (broker *Broker) delConsumer(clientID uint64) {
	client, ok := broker.clients[clientID]
	if !ok {
		log.Errorf("del consumer clientID: %d not found", clientID)
		return
	}
	topicConsumers, ok := broker.consumers[client.topic]
	if !ok {
		log.Errorf("consumer topic: %s not found", client.topic)
		return
	}
	if len(topicConsumers) == 1 {
		delete(broker.consumers, client.topic)
		// end topic syncer
		broker.deleteSyncer(client.topic)
	}
	ch, ok := topicConsumers[clientID]
	if ok {
		close(ch)
		delete(topicConsumers, clientID)
	}
}

func (broker *Broker) getConsumersWithMtx(topic string) []chan string {
	broker.mtx.RLock()
	defer broker.mtx.RUnlock()

	topicConsumers, ok := broker.consumers[topic]
	if !ok {
		return nil
	}
	chs := []chan string{}
	for _, ch := range topicConsumers {
		chs = append(chs, ch)
	}
	return chs
}

// syncer
func (broker *Broker) addSyncer(topic string) {
	log.Debugf("add syncer topic: %s", topic)
	closeCh := make(chan struct{})
	broker.syncers[topic] = closeCh

	buf, ok := broker.topics[topic]
	if !ok {
		log.Errorf("topic: %s buffer not found", topic)
		return
	}
	go func() {
		for {
			select {
			case msg := <-buf:
				log.Tracef("sync msg: %v from topic: %s", msg, topic)
				// sync to all consumers
				chs := broker.getConsumersWithMtx(topic)
				if chs == nil {
					log.Errorf("topic: %s consumer not found")
					continue
				}
				for _, ch := range chs {
					ch <- msg
				}
			case <-closeCh:
				return
			}
		}
	}()
}

func (broker *Broker) deleteSyncer(topic string) {
	log.Debugf("del syncer topic: %s", topic)
	ch, ok := broker.syncers[topic]
	if !ok {
		log.Errorf("topic: %s syncer not found", topic)
		return
	}
	close(ch)
	delete(broker.syncers, topic)
}

func (broker *Broker) Handle(end geminio.End) error {
	clientID := end.ClientID()
	log.Debugf("add client: %d", clientID)
	broker.mtx.Lock()
	broker.clients[clientID] = &roleEnd{
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
		broker.mtx.RLock()
		client, ok := broker.clients[msg.ClientID()]
		if !ok {
			log.Errorf("client: %d not found while receiving msg", msg.ClientID())
			broker.mtx.RUnlock()
			continue
		}

		ch := broker.getTopic(client.topic)
		if ch == nil {
			log.Errorf("client: %d topic: %s not found while receiving msg", msg.ClientID(), client.topic)
			msg.Error(errors.New("no such topic"))
			broker.mtx.RUnlock()
			continue
		}
		log.Debugf("end: %d receive msg: %s topic: %s", msg.ClientID(), string(msg.Data()), client.topic)
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
	log.Debugf("del client: %d", clientID)
	broker.delConsumer(clientID)
	delete(broker.clients, clientID)
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
		broker.initTopic(claim.Topic)
		broker.mtx.Unlock()
	case "consumer":
		broker.mtx.Lock()
		client, ok := broker.clients[clientID]
		if !ok {
			log.Errorf("client: %v not found", clientID)
			broker.mtx.Unlock()
			return
		}
		client.role = claim.Role
		client.topic = claim.Topic

		// initial producer topic buffer
		broker.initTopic(claim.Topic)

		// initial consumer topic buffer
		ch := broker.addConsumer(claim.Topic, clientID)
		// consumer msg to end
		go func() {
			for {
				select {
				case msg, ok := <-ch:
					if !ok {
						return
					}
					log.Tracef("msg: %s from consumer: %d", msg, clientID)
					broker.mtx.RLock()
					client, ok := broker.clients[clientID]
					if ok {
						client.end.Publish(context.TODO(), client.end.NewMessage([]byte(msg)))
					}
					broker.mtx.RUnlock()
				}
			}
		}()
		broker.mtx.Unlock()
	}
}
