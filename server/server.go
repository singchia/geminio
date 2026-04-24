package server

import (
	"net"

	"github.com/singchia/gemino"
)

type Listener interface {
	// Accept waits for and returns the next end to the listener.
	AcceptEnd() (gemino.End, error)

	// Accept waits for and returns the next connection to the listener.
	// the returned Conn is actually a End
	Accept() (net.Conn, error)

	// Close closes the listener.
	// Any blocked Accept operations will be unblocked and return errors.
	Close() error

	// Addr returns the listener's network address.
	Addr() net.Addr
}

type ret struct {
	end gemino.End
	err error
}

type listener struct {
	opts []*EndOptions
	ln   net.Listener
	ch   chan *ret
}

func Listen(network, address string, opts ...*EndOptions) (Listener, error) {
	ln, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	return &listener{
		ln:   ln,
		opts: opts,
		ch:   make(chan *ret, 32)}, nil
}

func (ln *listener) AcceptEnd() (gemino.End, error) {
	netconn, err := ln.ln.Accept()
	if err != nil {
		return nil, err
	}
	go func() {
		end, err := NewEndWithConn(netconn, ln.opts...)
		ln.ch <- &ret{end, err}
	}()
	ret := <-ln.ch
	return ret.end, ret.err
}

func (ln *listener) Accept() (net.Conn, error) {
	return ln.AcceptEnd()
}

func (ln *listener) Close() error {
	return ln.ln.Close()
}

func (ln *listener) Addr() net.Addr {
	return ln.ln.Addr()
}
