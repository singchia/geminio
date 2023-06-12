package iodefine

import (
	"errors"
	"strings"
)

type IOType uint

const (
	IN  IOType = 0
	OUT IOType = 1
)

type IORet uint

const (
	IOSuccess       IORet = 0
	IOData          IORet = 1
	IOReconnect     IORet = 2
	IONew           IORet = 3
	IONewActive     IORet = 4
	IONewPassive    IORet = 5
	IOClosed        IORet = 6
	IOClosedActive  IORet = 7
	IOClosedPassive IORet = 8
	IOErr           IORet = 9
	IOExit          IORet = 10
	IODiscard       IORet = 11
)

var (
	ErrIOTimeout = errors.New("io time out")
)

func ErrUseOfClosedNetwork(err error) bool {
	return strings.Contains(err.Error(), "use of closed network connection")
}
