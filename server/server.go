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
	ln net.Listener
}

func Listen(network, address string) (Listener, error) {
	ln, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}
	return &listener{ln}, nil
}

func (ln *listener) Accept() (geminio.End, error) {
	return nil, nil
}

func (ln *listener) Close() error {
	return ln.ln.Close()
}

func (ln *listener) Addr() net.Addr {
	return ln.ln.Addr()
}
