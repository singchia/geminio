package id

import (
	"crypto/rand"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

type Mode string

const (
	Even   Mode = "even"
	Odd    Mode = "odd"
	Unique Mode = "unique"
	Inc    Mode = "inc"
)

var (
	DefaultIncIDCounter = NewIDCounter(Inc)
)

type IDCounter struct {
	counter uint32
	once    *sync.Once

	ids  map[uint64]struct{}
	mtx  sync.RWMutex
	mode Mode
}

func NewIDCounter(mode Mode) *IDCounter {
	idCounter := &IDCounter{
		counter: 0,
		once:    new(sync.Once),
		ids:     make(map[uint64]struct{}),
		mode:    mode,
	}
	if mode == Even || mode == Odd || mode == Inc {
		idCounter.counter = randomUint32()
	}
	return idCounter
}

func (idCounter *IDCounter) ReserveID(id uint64) {
	idCounter.mtx.Lock()
	defer idCounter.mtx.Unlock()
	idCounter.ids[id] = struct{}{}
}

func (idCounter *IDCounter) GetID() uint64 {
	switch idCounter.mode {
	case Even:
		return uint64(time.Now().Unix()<<32) +
			uint64(atomic.AddUint32(&idCounter.counter, 2))
	case Odd:
		idCounter.once.Do(func() {
			atomic.AddUint32(&idCounter.counter, 1)
		})
		return uint64(time.Now().Unix()<<32) +
			uint64(atomic.AddUint32(&idCounter.counter, 2))
	case Inc:
		return uint64(time.Now().Unix()<<32) +
			uint64(atomic.AddUint32(&idCounter.counter, 1))
	case Unique:
		idCounter.mtx.Lock()
		for i := uint64(1); i < math.MaxUint64; i++ {
			_, ok := idCounter.ids[i]
			if !ok {
				idCounter.ids[i] = struct{}{}
				idCounter.mtx.Unlock()
				return i
			}
		}
		idCounter.mtx.Unlock()
	}
	return 0
}

func (idCounter *IDCounter) GetIDByMeta(meta []byte) (uint64, error) {
	return idCounter.GetID(), nil
}

func (idCounter *IDCounter) DelID(i uint64) {
	switch idCounter.mode {
	case Unique:
		idCounter.mtx.Lock()
		delete(idCounter.ids, i)
		idCounter.mtx.Unlock()
	}
}

func (idCounter *IDCounter) Close() {
	idCounter.ids = nil
}

func randomUint32() uint32 {
	var b [4]byte
	_, err := io.ReadFull(rand.Reader, b[:])
	if err != nil {
		return 0
	}
	b[0] |= 0x00 // a random even number
	return (uint32(b[0]) << 0) | (uint32(b[1]) << 8) | (uint32(b[2]) << 16) | (uint32(b[3]) << 24)
}
