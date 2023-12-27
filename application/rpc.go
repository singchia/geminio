package application

import (
	"context"
	"io"
	"regexp"
	"time"

	"github.com/jumboframes/armorigo/synchub"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/options"
	"github.com/singchia/geminio/packet"
)

// geminio.RPCer
func (sm *stream) NewRequest(data []byte, opts ...*options.NewRequestOptions) geminio.Request {
	id := sm.pf.NewPacketID()
	opt := options.MergeNewRequestOptions(opts...)
	req := &request{
		//RequestAttribute: &geminio.RequestAttribute{},
		data:     data,
		id:       id,
		clientID: sm.cn.ClientID(),
		streamID: sm.dg.DialogueID(),
		custom:   opt.Custom,
	}
	return req
}

func (sm *stream) addLocalRPC(method string, rpc geminio.RPC) {
	sm.rpcMtx.Lock()
	defer sm.rpcMtx.Unlock()
	sm.localRPCs[method] = rpc
}

func (sm *stream) delLocalRPC(method string) {
	sm.rpcMtx.Lock()
	defer sm.rpcMtx.Unlock()
	delete(sm.localRPCs, method)
}

// Register will overwrite the old method if exists.
func (sm *stream) Register(ctx context.Context, method string, rpc geminio.RPC) error {
	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return io.EOF
	}
	sm.addLocalRPC(method, rpc)
	pkt := sm.pf.NewRegisterPacketWithSessionID(sm.dg.DialogueID(), []byte(method))
	sync := sm.shub.New(pkt.ID())
	sm.writeInCh <- pkt
	sm.mtx.RUnlock()
	select {
	case event := <-sync.C():
		if event.Error != nil {
			sm.log.Debugf("register err: %s, clientID: %d, dialogueID: %d, packetID: %d",
				event.Error, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID())
			sm.delLocalRPC(method)
			return event.Error
		}
	case <-ctx.Done():
		sm.delLocalRPC(method)
		return ctx.Err()
	}
	return nil
}

func (sm *stream) Call(ctx context.Context, method string, req geminio.Request, opts ...*options.CallOptions) (geminio.Response, error) {
	if req.ClientID() != sm.cn.ClientID() {
		return nil, ErrMismatchClientID
	}
	if req.StreamID() != sm.dg.DialogueID() {
		return nil, ErrMismatchStreamID
	}
	co := options.MergeCallOptions(opts...)

	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return nil, io.EOF
	}

	// check remote RPC exists
	sm.rpcMtx.RLock()
	_, ok := sm.remoteRPCs[method]
	if !ok {
		sm.rpcMtx.RUnlock()
		return nil, ErrRemoteRPCUnregistered
	}
	sm.rpcMtx.RUnlock()
	// transfer to underlayer packet
	pkt := sm.pf.NewRequestPacketWithIDAndSessionID(req.ID(), sm.dg.DialogueID(), []byte(method), req.Data())
	if co.Timeout != nil {
		// if timeout exists, we should deliver it
		pkt.Data.Deadline = time.Now().Add(*co.Timeout)
	}
	pkt.Data.Custom = req.Custom()

	deadline, ok := ctx.Deadline()
	if ok {
		// if deadline exists, we should deliver it
		pkt.Data.Context.Deadline = deadline
	}
	var sync synchub.Sync
	syncOpts := []synchub.SyncOption{}
	if co.Timeout != nil {
		// the sync may has timeout
		syncOpts = append(syncOpts, synchub.WithTimeout(*co.Timeout))
	}
	sync = sm.shub.New(req.ID(), syncOpts...)
	sm.writeInCh <- pkt
	sm.mtx.RUnlock()

	// we don't set ctx to sync, because select perform better
	select {
	case <-ctx.Done():
		sync.Cancel(false)

		if ctx.Err() == context.DeadlineExceeded {
			// we don't deliver deadline exceeded since the pkt already has it
			return nil, ctx.Err()
		}
		sm.mtx.RLock()
		if !sm.streamOK {
			sm.mtx.RUnlock()
			return nil, io.EOF
		}
		// notify peer if context Canceled
		cancelType := packet.RequestCancelTypeCanceled
		cancelPkt := sm.pf.NewRequestCancelPacketWithIDAndSessionID(pkt.ID(), sm.dg.DialogueID(), cancelType)
		sm.writeInCh <- cancelPkt
		sm.mtx.RUnlock()
		return nil, ctx.Err()

	case event := <-sync.C():
		if event.Error != nil {
			sm.log.Debugf("request return err: %s, clientID: %d, dialogueID: %d, reqID: %d",
				event.Error, sm.cn.ClientID(), sm.dg.DialogueID(), req.ID())
			return nil, event.Error
		}
		rsp := event.Ack.(*response)
		return rsp, nil
	}
}

func (sm *stream) CallAsync(ctx context.Context, method string, req geminio.Request, ch chan *geminio.Call, opts ...*options.CallOptions) (*geminio.Call, error) {
	if req.ClientID() != sm.cn.ClientID() {
		return nil, ErrMismatchClientID
	}
	if req.StreamID() != sm.dg.DialogueID() {
		return nil, ErrMismatchStreamID
	}
	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return nil, io.EOF
	}
	pkt := sm.pf.NewRequestPacketWithIDAndSessionID(req.ID(), sm.dg.DialogueID(), []byte(method), req.Data())
	// deadline and timeout for peer
	if req.Timeout() != 0 {
		pkt.Data.Deadline = time.Now().Add(req.Timeout())
	}
	pkt.Data.Custom = req.Custom()

	deadline, ok := ctx.Deadline()
	if ok {
		pkt.Data.Context.Deadline = deadline
	}
	if ch == nil {
		ch = make(chan *geminio.Call, 1)
	}
	call := &geminio.Call{
		Method:  method,
		Request: req,
		Done:    ch,
	}
	// deadline and timeout for local
	syncOpts := []synchub.SyncOption{synchub.WithContext(ctx),
		synchub.WithCallback(func(event *synchub.Event) {
			if event.Error != nil {
				sm.log.Debugf("request packet err: %s, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
					event.Error, sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
				call.Error = event.Error
				ch <- call
				return
			}
			sm.log.Tracef("response return succeed, clientID: %d, dialogueID: %d, packetID: %d, packetType: %s",
				sm.cn.ClientID(), sm.dg.DialogueID(), pkt.ID(), pkt.Type().String())
			ch <- call
			return
		})}
	if req.Timeout() != 0 {
		syncOpts = append(syncOpts, synchub.WithTimeout(req.Timeout()))
	}
	// Add a new sync for the async call
	sm.shub.New(pkt.ID(), syncOpts...)
	sm.writeInCh <- pkt
	sm.mtx.RUnlock()
	return call, nil
}

func (sm *stream) Hijack(rpc geminio.HijackRPC, opts ...*options.HijackOptions) error {
	pRPC := &patternRPC{}
	fo := options.MergeHijackOptions(opts...)
	if fo.Pattern != nil {
		reg, err := regexp.Compile(*fo.Pattern)
		if err != nil {
			return err
		}
		pRPC.pattern = reg
	}
	if fo.Match != nil {
		pRPC.match = *fo.Match
	}
	pRPC.rpc = rpc
	sm.hijackRPC = pRPC
	return nil
}
