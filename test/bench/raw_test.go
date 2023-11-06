package bench

import (
	"io"
	"net"
	"sync"
	"testing"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/server"
)

func BenchmarkTCP(b *testing.B) {
	sConn, cConn, err := getTCPConnectionPair()
	if err != nil {
		b.Fatal(err)
	}
	defer sConn.Close()
	defer cConn.Close()

	bench(b, sConn, cConn)
	bench(b, cConn, sConn)
}

func BenchmarkEnd(b *testing.B) {
	log.SetLevel(log.LevelError)
	sEnd, cEnd, err := getEndPair()
	if err != nil {
		b.Fatal(err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	bench(b, sEnd, cEnd)
	bench(b, cEnd, sEnd)
}

func BenchmarkStream(b *testing.B) {
	log.SetLevel(log.LevelError)
	ss, cs, err := getEndStream()
	if err != nil {
		b.Fatal(err)
	}
	defer ss.Close()
	defer cs.Close()

	bench(b, ss, cs)
	bench(b, cs, ss)
}

func bench(b *testing.B, rd io.Reader, wr io.Writer) {
	buf := make([]byte, 128*1024)
	buf2 := make([]byte, 128*1024)
	b.SetBytes(128 * 1024)
	b.ResetTimer()
	b.ReportAllocs()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		count := 0
		for {
			n, _ := rd.Read(buf2)
			count += n
			if count == 128*1024*b.N {
				return
			}
		}
	}()
	for i := 0; i < b.N; i++ {
		wr.Write(buf)
	}
	wg.Wait()
}

func getEndStream() (geminio.Stream, geminio.Stream, error) {
	sEnd, cEnd, err := getEndPair()
	if err != nil {
		return nil, nil, err
	}

	var ss geminio.Stream
	var sErr error
	done := make(chan struct{})
	go func() {
		ss, sErr = sEnd.AcceptStream()
		close(done)
	}()

	cs, err := cEnd.OpenStream()
	if err != nil {
		return nil, nil, err
	}

	<-done
	if sErr != nil {
		return nil, nil, sErr
	}
	return ss, cs, nil
}

func getEndPair() (geminio.End, geminio.End, error) {
	sConn, cConn, err := getTCPConnectionPair()
	if err != nil {
		return nil, nil, err
	}

	var sEnd geminio.End
	var sErr error
	done := make(chan struct{})
	go func() {
		sEnd, sErr = server.NewEndWithConn(sConn)
		close(done)
	}()

	dialer := func() (net.Conn, error) { return cConn, nil }
	cEnd, err := client.NewEndWithDialer(dialer)
	if err != nil {
		return nil, nil, err
	}

	<-done
	if sErr != nil {
		return nil, nil, sErr
	}
	return sEnd, cEnd, nil
}

func getTCPConnectionPair() (net.Conn, net.Conn, error) {
	lst, err := net.Listen("tcp", "localhost:12345")
	if err != nil {
		return nil, nil, err
	}
	defer lst.Close()

	var conn0 net.Conn
	var err0 error
	done := make(chan struct{})
	go func() {
		conn0, err0 = lst.Accept()
		close(done)
	}()

	conn1, err := net.Dial("tcp", lst.Addr().String())
	if err != nil {
		return nil, nil, err
	}

	<-done
	if err0 != nil {
		return nil, nil, err0
	}
	return conn0, conn1, nil
}
