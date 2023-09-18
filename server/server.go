package server

import (
	"net"

	"github.com/singchia/geminio"
)

type Listener interface {
	// Accept waits for and returns the next end to the listener.
	Accept() (geminio.End, error)

	// Close closes the listener.
	// Any blocked Accept operations will be unblocked and return errors.
	Close() error

	// Addr returns the listener's network address.
	Addr() net.Addr
}

type listener struct {
	opts []*EndOptions
	ln   net.Listener
}

func Listen(network, address string, opts ...*EndOptions) (Listener, error) {
	ln, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	return &listener{ln: ln, opts: opts}, nil
}

func (ln *listener) Accept() (geminio.End, error) {
	netconn, err := ln.ln.Accept()
	if err != nil {
		return nil, err
	}
	end, err := NewEndWithConn(netconn, ln.opts...)
	return end, nil
}

func (ln *listener) Close() error {
	return ln.ln.Close()
}

func (ln *listener) Addr() net.Addr {
	return ln.ln.Addr()
}
