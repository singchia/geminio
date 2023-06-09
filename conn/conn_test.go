package conn

import (
	"net"
	"sync"
	"testing"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/packet"
	"github.com/singchia/geminio/pkg/id"
)

func BenchmarkConn(b *testing.B) {
	connServer, connClient, err := getConnPair()
	if err != nil {
		b.Error(err)
		return
	}
	defer connServer.Close()
	defer connClient.Close()

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
		connServer, errServer = NewRecvConn(tcpConnServer)
		close(done)
	}()

	connClient, errClient = newSendConn(tcpConnClient)
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
