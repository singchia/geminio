package synchub

import (
	"errors"
	"log"
	"sync"

	"github.com/singchia/go-timer"
)

var (
	ErrTimeout     = errors.New("timeout")
	ErrNotfound    = errors.New("not found")
	ErrForceClosed = errors.New("force closed")
)

type SyncHub struct {
	syncs sync.Map
	tmr   timer.Timer
}

func NewSyncHub(tmr timer.Timer) *SyncHub {
	sh := &SyncHub{
		syncs: sync.Map{},
		tmr:   tmr,
	}
	return sh
}

type Unit struct {
	Value interface{}
	Ack   interface{}
	Error error
}

type unitchan struct {
	ch   chan *Unit
	unit *Unit
	tick timer.Tick
	cb   func(*Unit)
}

func (sh *SyncHub) Sync(syncId interface{}, value interface{}, timeout uint64) chan *Unit {
	unit := &Unit{
		Value: value,
		Ack:   nil,
		Error: nil,
	}
	ch := make(chan *Unit, 1)
	uNc := &unitchan{ch: ch, unit: unit}
	tick, err := sh.tmr.Time(timeout, syncId, nil, sh.timeout)
	if err != nil {
		log.Printf("timer set err: %s\n", err)
		return nil
	}
	uNc.tick = tick
	value, ok := sh.syncs.Swap(syncId, uNc)
	if ok {
		uNcOld := value.(*unitchan)
		uNcOld.tick.Cancel()
		uNcOld.unit.Error = errors.New("the id was resynced")
		uNcOld.ch <- uNcOld.unit
	}
	return ch
}

func (sh *SyncHub) SyncCallback(syncId interface{}, value interface{}, timeout uint64, cb func(*Unit)) {
	unit := &Unit{
		Value: value,
		Ack:   nil,
		Error: nil,
	}
	uNc := &unitchan{unit: unit, cb: cb}
	tick, err := sh.tmr.Time(timeout, syncId, nil, sh.timeout)
	if err != nil {
		log.Printf("timer set err: %s\n", err)
		return
	}
	uNc.tick = tick
	value, ok := sh.syncs.Swap(syncId, uNc)
	if ok {
		uNcOld := value.(*unitchan)
		uNcOld.tick.Cancel()
		uNcOld.unit.Error = errors.New("the id was resynced")
		if uNc.ch != nil {
			uNcOld.ch <- uNcOld.unit
		}
		if uNc.cb != nil {
			uNc.cb(uNc.unit)
		}
	}
}

func (sh *SyncHub) Cancel(syncId interface{}) {
	value, ok := sh.syncs.Load(syncId)
	if ok {
		uNc := value.(*unitchan)
		uNc.tick.Cancel()
		sh.syncs.Delete(syncId)
		log.Printf("syncId: %v canceled\n", syncId)
	}
}

func (sh *SyncHub) Ack(syncId interface{}, ack interface{}) {
	defer sh.syncs.Delete(syncId)

	value, ok := sh.syncs.Load(syncId)
	if !ok {
		return
	}
	uNc := value.(*unitchan)
	uNc.tick.Cancel()
	uNc.unit.Ack = ack
	if uNc.ch != nil {
		uNc.ch <- uNc.unit
	}
	if uNc.cb != nil {
		uNc.cb(uNc.unit)
	}
}

func (sh *SyncHub) Error(syncId interface{}, err error) {
	defer sh.syncs.Delete(syncId)

	value, ok := sh.syncs.Load(syncId)
	if !ok {
		return
	}
	uNc := value.(*unitchan)
	uNc.tick.Cancel()
	uNc.unit.Error = err
	if uNc.ch != nil {
		uNc.ch <- uNc.unit
	}
	if uNc.cb != nil {
		uNc.cb(uNc.unit)
	}
}

func (sh *SyncHub) Close() {
	sh.syncs.Range(func(key, value interface{}) bool {
		log.Printf("syncId: %v force closed\n", key)
		uNc := value.(*unitchan)
		uNc.unit.Error = ErrForceClosed
		if uNc.ch != nil {
			uNc.ch <- uNc.unit
		}
		if uNc.cb != nil {
			uNc.cb(uNc.unit)
		}
		return true
	})
}

func (sh *SyncHub) timeout(data interface{}) error {
	defer sh.syncs.Delete(data)

	value, ok := sh.syncs.Load(data)
	if !ok {
		log.Printf("syncId: %v not found\n", data)
		return nil
	}
	uNc := value.(*unitchan)
	uNc.unit.Error = ErrTimeout
	if uNc.ch != nil {
		uNc.ch <- uNc.unit
	}
	if uNc.cb != nil {
		uNc.cb(uNc.unit)
	}
	log.Printf("syncId: %v timeout\n", data)
	return nil
}
