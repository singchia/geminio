package conn

import (
	"fmt"
	"net"
	"sync"
	"testing"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
)

func writeToChanWithDone(ch chan<- int, value int, done <-chan struct{}) error {
	select {
	case _, ok := <-done:
		if !ok {
			return fmt.Errorf("channel write failed: channel closed")
		}
	default:
		select {
		case ch <- value:
			return nil
		case _, ok := <-done:
			if !ok {
				return fmt.Errorf("channel write failed: channel closed")
			}
		}
	}
	return nil
}

func BenchmarkSelectClosed(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	wg := sync.WaitGroup{}
	wg.Add(2)

	ch := make(chan int)
	done := make(chan struct{})

	go func() {
		defer wg.Done()
		for {
			_, ok := <-ch
			if !ok {
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < b.N; i++ {
			writeToChanWithDone(ch, i, done)

		}
	}()

	close(done)
	close(ch)
	wg.Wait()
}

func BenchmarkMtxClosed(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	wg := sync.WaitGroup{}
	wg.Add(2)
	mtx := sync.RWMutex{}

	ch := make(chan int)
	chOK := true
	go func() {
		defer wg.Done()
		for {
			_, ok := <-ch
			if !ok {
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < b.N; i++ {
			mtx.RLock()
			if chOK {
				ch <- i
			}
			mtx.RUnlock()
		}
	}()
	mtx.Lock()
	chOK = false
	close(ch)
	mtx.Unlock()
	wg.Wait()
}

// It's just hard to distinguish io discard or io err
func BenchmarkLeaveOpen(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	wg := sync.WaitGroup{}
	wg.Add(1)
	ch := make(chan int, 1024)
	go func() {
		defer wg.Done()
		for {
			_, ok := <-ch
			if !ok {
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < b.N; i++ {
			select {
			case ch <- i:
			default:
				continue
			}
		}
	}()
	wg.Wait()
}

func BenchmarkConn(b *testing.B) {
	connServer, connClient, err := getConnPair()
	if err != nil {
		b.Error(err)
		return
	}
	defer func() {
		connServer.Close()
		connClient.Close()
	}()

	benchRW(b, connClient, connServer)
	benchRW(b, connServer, connClient)
}

func benchRW(b *testing.B, reader Reader, writer Writer) {
	pf := packet.NewPacketFactory(id.NewIDCounter(id.Even))
	size := int64(128 * 1024)
	buf := make([]byte, 128)
	pkt := pf.NewMessagePacket(buf, []byte{}, []byte{})

	b.SetBytes(size)
	b.ResetTimer()
	b.ReportAllocs()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		count := 0
		for {
			pkt, err := reader.Read()
			if err != nil {
				return
			}
			if pkt.Type() != packet.TypeMessagePacket {
				return
			}
			count += 1
			if count == b.N {
				return
			}
		}
	}()
	for i := 0; i < b.N; i++ {
		writer.Write(pkt)
	}
	wg.Wait()
}

func getConnPair() (Conn, Conn, error) {
	log.SetLevel(log.LevelDebug)
	tcpConnServer, tcpConnClient, err := getTCPConnPair()
	if err != nil {
		return nil, nil, err
	}
	var connServer, connClient Conn
	var errServer, errClient error
	done := make(chan struct{})
	go func() {
		connServer, errServer = NewServerConn(tcpConnServer)
		close(done)
	}()

	connClient, errClient = newClientConn(tcpConnClient)
	if errClient != nil {
		return nil, nil, errClient
	}

	<-done
	if errServer != nil {
		return nil, nil, errServer
	}
	return connServer, connClient, nil
}

func getTCPConnPair() (net.Conn, net.Conn, error) {
	lst, errlisn := net.Listen("tcp", "localhost:12345")
	if errlisn != nil {
		return nil, nil, errlisn
	}
	defer lst.Close()

	var connServer net.Conn
	var erracpt error
	done := make(chan struct{})
	go func() {
		connServer, erracpt = lst.Accept()
		close(done)
	}()

	connClient, errdial := net.Dial("tcp", lst.Addr().String())
	if errdial != nil {
		return nil, nil, errdial
	}

	<-done
	if erracpt != nil {
		return nil, nil, erracpt
	}
	return connServer, connClient, nil
}
