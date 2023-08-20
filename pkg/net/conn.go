package net

import (
	"errors"
	"net"
	"time"
)

type addr struct{}

func (*addr) Network() string {
	return "unimplemented Network"
}

func (*addr) String() string {
	return "unimplemented String"
}

type UnimplementedConn struct{}

func (conn *UnimplementedConn) Read(b []byte) (n int, err error) {
	return 0, errors.New("unimplemented Read")
}

func (conn *UnimplementedConn) Write(b []byte) (n int, err error) {
	return 0, errors.New("unimplemented Write")
}

func (conn *UnimplementedConn) Close() error {
	return errors.New("unimplemented Close")
}

func (conn *UnimplementedConn) LocalAddr() net.Addr {
	return &addr{}
}

func (conn *UnimplementedConn) RemoteAddr() net.Addr {
	return &addr{}
}

func (conn *UnimplementedConn) SetDeadline(t time.Time) error {
	return errors.New("unimplemented SetDeadline")
}

func (conn *UnimplementedConn) SetReadDeadline(t time.Time) error {
	return errors.New("unimplemented SetReadDeadline")
}

func (conn *UnimplementedConn) SetWriteDeadline(t time.Time) error {
	return errors.New("unimplemented SetWriteDeadline")
}
