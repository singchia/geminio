package test

import (
	"net"
	"strconv"

	"github.com/singchia/gemino"
	"github.com/singchia/gemino/client"
	"github.com/singchia/gemino/server"
)

func GetEndStream() (gemino.End, gemino.End, gemino.Stream, gemino.Stream, error) {
	sEnd, cEnd, err := GetEndPair()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	var ss gemino.Stream
	var sErr error
	done := make(chan struct{})
	go func() {
		ss, sErr = sEnd.AcceptStream()
		close(done)
	}()

	cs, err := cEnd.OpenStream()
	if err != nil {
		sEnd.Close()
		cEnd.Close()
		return nil, nil, nil, nil, err
	}

	<-done
	if sErr != nil {
		sEnd.Close()
		cEnd.Close()
		return nil, nil, nil, nil, sErr
	}
	return sEnd, cEnd, ss, cs, nil
}

func GetEndPair() (gemino.End, gemino.End, error) {
	sConn, cConn := net.Pipe()

	var sEnd gemino.End
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

func GetTCPConnectionPair(port int) (net.Conn, net.Conn, error) {
	lst, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
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
