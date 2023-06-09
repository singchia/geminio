package geminio

import "github.com/singchia/geminio/conn"

// event delegates
type ClientDelegate interface {
	conn.SendConnDelegate
}

type ServerDelegate interface {
	conn.RecvConnDelegate
}
