package iodefine

import "errors"

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
	IOClosed        IORet = 4
	IOClosedActive  IORet = 5
	IOClosedPassive IORet = 6
	IOErr           IORet = 7
	IONewActive     IORet = 8
	IONewPassive    IORet = 9
	IOExit          IORet = 10
)

var (
	ErrIOTimeout = errors.New("io time out")
)
