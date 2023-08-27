package application

import (
	"io"
	"net"
	"os"
	"time"

	"github.com/jumboframes/armorigo/synchub"
)

const (
	deadlineReadSyncKey  = "geminio:read_deadline"
	deadlineWriteSyncKey = "geminio:write_deadline"
)

// geminio.Raw
func (sm *stream) Read(b []byte) (int, error) {
	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return 0, io.EOF
	}
	sm.mtx.RUnlock()

	sm.cacheMtx.Lock()
	if sm.cache != nil && len(sm.cache) != 0 {
		n := copy(b, sm.cache)
		sm.cache = sm.cache[n:]
		sm.cacheMtx.Unlock()
		return n, nil
	}
	sm.cacheMtx.Unlock()

	if sm.readDeadlineExceeded() {
		select {
		case pkt, ok := <-sm.streamCh:
			if !ok {
				return 0, io.EOF
			}
			sm.cacheMtx.Lock()
			n := copy(b, pkt.Data)
			sm.cache = pkt.Data[n:]
			sm.cacheMtx.Unlock()
			return n, nil
		default:
			return 0, os.ErrDeadlineExceeded
		}
	}

	dlCh := make(chan struct{})
	sm.dlMtx.Lock()
	e := sm.dlReadChList.PushBack(dlCh)
	sm.dlMtx.Unlock()

	select {
	case pkt, ok := <-sm.streamCh:
		if !ok {
			sm.dlMtx.Lock()
			sm.dlReadChList.Remove(e)
			sm.dlMtx.Unlock()
			return 0, io.EOF
		}
		sm.cacheMtx.Lock()
		n := copy(b, pkt.Data)
		sm.cache = pkt.Data[n:]
		sm.cacheMtx.Unlock()

		// if we have't used the deadline channel, then release it
		sm.dlMtx.Lock()
		sm.dlReadChList.Remove(e)
		sm.dlMtx.Unlock()
		return n, nil
	case <-dlCh:
		return 0, os.ErrDeadlineExceeded
	}
}

func (sm *stream) Write(b []byte) (int, error) {
	sm.mtx.RLock()
	if !sm.streamOK {
		sm.mtx.RUnlock()
		return 0, io.EOF
	}
	sm.mtx.RUnlock()

	newb := make([]byte, len(b))
	copy(newb, b)
	pkt := sm.pf.NewStreamPacketWithSessionID(sm.dg.DialogueID(), newb)

	if sm.writeDeadlineExceeded() {
		select {
		case sm.writeInCh <- pkt:
			return len(b), nil
		default:
			return 0, os.ErrDeadlineExceeded
		}
	}

	dlCh := make(chan struct{})
	sm.dlMtx.Lock()
	e := sm.dlWriteChList.PushBack(dlCh)
	sm.dlMtx.Unlock()

	select {
	case sm.writeInCh <- pkt:
		sm.dlMtx.Lock()
		sm.dlWriteChList.Remove(e)
		sm.dlMtx.Unlock()
		return len(b), nil
	case <-dlCh:
		return 0, os.ErrDeadlineExceeded
	}
}

func (sm *stream) LocalAddr() net.Addr {
	return sm.cn.LocalAddr()
}

func (sm *stream) RemoteAddr() net.Addr {
	return sm.cn.RemoteAddr()
}

// SetDeadline sets the read and write deadlines associated
// with the connection. It is equivalent to calling both
// SetReadDeadline and SetWriteDeadline.
//
// A deadline is an absolute time after which I/O operations
// fail instead of blocking. The deadline applies to all future
// and pending I/O, not just the immediately following call to
// Read or Write. After a deadline has been exceeded, the
// connection can be refreshed by setting a deadline in the future.
//
// If the deadline is exceeded a call to Read or Write or to other
// I/O methods will return an error that wraps os.ErrDeadlineExceeded.
// This can be tested using errors.Is(err, os.ErrDeadlineExceeded).
// The error's Timeout method will return true, but note that there
// are other possible errors for which the Timeout method will
// return true even if the deadline has not been exceeded.
//
// An idle timeout can be implemented by repeatedly extending
// the deadline after successful Read or Write calls.
//
// A zero value for t means I/O operations will not time out.
func (sm *stream) SetDeadline(t time.Time) error {
	sm.dlMtx.Lock()
	defer sm.dlMtx.Unlock()
	err := sm.setReadDeadline(t)
	if err != nil {
		return err
	}
	err = sm.setWriteDeadline(t)
	if err != nil {
		return err
	}
	return nil
}

// SetReadDeadline sets the deadline for future Read calls
// and any currently-blocked Read call.
// A zero value for t means Read will not time out.
func (sm *stream) SetReadDeadline(t time.Time) error {
	sm.dlMtx.Lock()
	defer sm.dlMtx.Unlock()
	return sm.setReadDeadline(t)
}

func (sm *stream) setReadDeadline(t time.Time) error {
	if sm.dlReadSync != nil {
		sm.dlReadSync.Cancel(false)
		sm.dlReadSync = nil
	}
	duration := t.Sub(time.Now())
	if duration > 0 {
		sm.dlReadSync = sm.shub.Add(deadlineReadSyncKey,
			synchub.WithTimeout(duration),
			synchub.WithCallback(func(event *synchub.Event) {
				if event.Error != synchub.ErrSyncTimeout {
					return
				}
				// deadline exceeds
				sm.dlMtx.RLock()
				defer sm.dlMtx.RUnlock()

				e := sm.dlReadChList.Front()
				for e != nil {
					ch, _ := e.Value.(chan struct{})
					// deadline exceeds and notify all waiting Read/Write
					close(ch)
					olde := e
					e = e.Next()
					sm.dlReadChList.Remove(olde)
				}
			}))
	}
	sm.dlRead = t
	return nil
}

// SetWriteDeadline sets the deadline for future Write calls
// and any currently-blocked Write call.
// Even if write times out, it may return n > 0, indicating that
// some of the data was successfully written.
// A zero value for t means Write will not time out.
func (sm *stream) SetWriteDeadline(t time.Time) error {
	sm.dlMtx.Lock()
	defer sm.dlMtx.Unlock()
	return sm.setWriteDeadline(t)
}

func (sm *stream) setWriteDeadline(t time.Time) error {
	if sm.dlWriteSync != nil {
		sm.dlWriteSync.Cancel(false)
		sm.dlWriteSync = nil
	}
	duration := t.Sub(time.Now())
	if duration > 0 {
		sm.dlWriteSync = sm.shub.Add(deadlineWriteSyncKey,
			synchub.WithTimeout(duration),
			synchub.WithCallback(func(event *synchub.Event) {
				if event.Error != synchub.ErrSyncTimeout {
					return
				}
				// deadline exceeds
				sm.dlMtx.RLock()
				defer sm.dlMtx.RUnlock()

				e := sm.dlWriteChList.Front()
				for e != nil {
					ch, _ := e.Value.(chan struct{})
					// deadline exceeds and notify all waiting Read/Write
					close(ch)
					olde := e
					e = e.Next()
					sm.dlWriteChList.Remove(olde)
				}
			}))
	}
	sm.dlWrite = t
	return nil
}

func (sm *stream) readDeadlineExceeded() bool {
	sm.dlMtx.RLock()
	defer sm.dlMtx.RUnlock()

	if !sm.dlRead.IsZero() && time.Now().After(sm.dlRead) {
		return true
	}
	return false
}

func (sm *stream) writeDeadlineExceeded() bool {
	sm.dlMtx.RLock()
	defer sm.dlMtx.RUnlock()

	if !sm.dlWrite.IsZero() && time.Now().After(sm.dlWrite) {
		return true
	}
	return false
}
