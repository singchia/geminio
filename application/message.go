package application

import (
	"context"
	"io"
	"time"

	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/options"
)

// geminio.Messager
func (sm *stream) NewMessage(data []byte) geminio.Message {
	id := sm.pf.NewPacketID()
	msg := &message{
		data:     data,
		id:       id,
		clientID: sm.cn.ClientID(),
		streamID: sm.dg.DialogueID(),
		sm:       sm,
	}
	return msg
}

func (sm *stream) ackMessage(pktID uint64, err error) error {
	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return io.EOF
	}

	pkt := sm.pf.NewMessageAckPacketWithSessionID(sm.dg.DialogueID(), pktID, err)
	sm.writeInCh <- pkt
	return nil
}

// Publish to peer, a sync function
func (sm *stream) Publish(ctx context.Context, msg geminio.Message, opts ...*options.PublishOptions) error {
	if msg.ClientID() != sm.cn.ClientID() {
		return ErrMismatchClientID
	}
	if msg.StreamID() != sm.dg.DialogueID() {
		return ErrMismatchStreamID
	}
	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return io.EOF
	}

	pkt := sm.pf.NewMessagePacketWithIDAndSessionID(msg.ID(), sm.dg.DialogueID(), nil, msg.Data())
	if msg.Timeout() != 0 {
		pkt.Data.Deadline = time.Now().Add(msg.Timeout())
	}
	deadline, ok := ctx.Deadline()
	if ok {
		pkt.Data.Context.Deadline = deadline
	}

	if msg.Cnss() == options.CnssAtMostOnce {
		// if consistency is set to be AtMostOnce, we don't care about context or timeout
		sm.writeInCh <- pkt
		sm.mtx.RUnlock()
		return nil
	}
	var sync synchub.Sync
	syncOpts := []synchub.SyncOption{synchub.WithContext(ctx)}
	if msg.Timeout() != 0 {
		// the sync may has timeout
		syncOpts = append(syncOpts, synchub.WithTimeout(msg.Timeout()))
	}
	sync = sm.shub.New(msg.ID(), syncOpts...)
	sm.writeInCh <- pkt
	sm.mtx.RUnlock()

	event := <-sync.C()
	if event.Error != nil {
		sm.log.Debugf("message return err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			event.Error, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
		// TODO we did't separate err from lib and user
		return event.Error
	}
	sm.log.Tracef("message return succeed, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
		sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
	return nil
}

func (sm *stream) PublishAsync(ctx context.Context, msg geminio.Message, ch chan *geminio.Publish) (*geminio.Publish, error) {
	if msg.ClientID() != sm.cn.ClientID() {
		return nil, ErrMismatchClientID
	}
	if msg.StreamID() != sm.dg.DialogueID() {
		return nil, ErrMismatchStreamID
	}
	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return nil, io.EOF
	}
	now := time.Now()
	pkt := sm.pf.NewMessagePacketWithIDAndSessionID(msg.ID(), sm.dg.DialogueID(), nil, msg.Data())
	if msg.Timeout() != 0 {
		pkt.Data.Deadline = now.Add(msg.Timeout())
	}
	deadline, ok := ctx.Deadline()
	if ok {
		pkt.Data.Context.Deadline = deadline
	}

	if msg.Cnss() == options.CnssAtMostOnce {
		// if consistency is set to be AtMostOnce, we don't care about context or timeout or async
		sm.writeInCh <- pkt
		sm.mtx.RUnlock()
		return nil, nil
	}
	if ch == nil {
		// we don't want block here
		ch = make(chan *geminio.Publish, 1)
	}
	publish := &geminio.Publish{
		Message: msg,
		Done:    ch,
	}
	// deadline and timeout for local
	opts := []synchub.SyncOption{synchub.WithContext(ctx), synchub.WithCallback(func(event *synchub.Event) {
		if event.Error != nil {
			sm.log.Debugf("message packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
				event.Error, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
			publish.Error = event.Error
			ch <- publish
			return
		}
		sm.log.Tracef("message return succeed, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
			sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
		ch <- publish
		return
	})}
	if msg.Timeout() != 0 {
		// the sync may has timeout
		opts = append(opts, synchub.WithTimeout(msg.Timeout()))
	}
	// Add a new sync for the async publish
	sm.shub.New(pkt.ID(), opts...)
	sm.writeInCh <- pkt
	sm.mtx.RUnlock()
	return publish, nil
}

// return EOF means the stream is closed
func (sm *stream) Receive(ctx context.Context) (geminio.Message, error) {
	select {
	case pkt, ok := <-sm.messageCh:
		if !ok {
			sm.log.Debugf("stream receive EOF, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
				sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
			return nil, io.EOF
		}
		msg := &message{
			timeout:  pkt.Data.Timeout,
			cnss:     options.Cnss(pkt.Cnss),
			data:     pkt.Data.Value,
			id:       pkt.PacketID,
			clientID: sm.cn.ClientID(),
			streamID: sm.dg.DialogueID(),
			sm:       sm,
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
