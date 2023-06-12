package geminio

import "github.com/singchia/geminio/conn"

// event delegates
type ClientDelegate interface {
	conn.ClientConnDelegate
}

type ServerDelegate interface {
	conn.ServerConnDelegate
}
