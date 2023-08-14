package application

import (
	"time"

	"github.com/singchia/geminio"
)

// request implements geminio.Request
type request struct {
	*geminio.RequestAttribute
	method   string
	data     []byte
	id       uint64
	clientID uint64
	streamID uint64
	timeout  time.Duration
}

// Get ID, which is packetID at under layer
func (req *request) ID() uint64 {
	return req.id
}

// Get StreamID for the request
func (req *request) StreamID() uint64 {
	return req.streamID
}

// Get ClientID for the request
func (req *request) ClientID() uint64 {
	return req.clientID
}

// Get Method for the request
func (req *request) Method() string {
	return req.method
}

// Get Timeout for the request, if no timeout set, the return.IsZero() is true
func (req *request) Timeout() time.Duration {
	return req.timeout
}

// Get Data for the request
func (req *request) Data() []byte {
	return req.data
}

// response implements geminio.Response
type response struct {
	err    error
	data   []byte
	method string
	//custom []byte
	// response share id with requestID, distinguish by packet type
	requestID uint64
	clientID  uint64
	streamID  uint64
}

func (rsp *response) Error() error {
	return rsp.err
}

func (rsp *response) SetError(err error) {
	rsp.err = err
}

func (rsp *response) ID() uint64 {
	return rsp.requestID
}

func (rsp *response) StreamID() uint64 {
	return rsp.streamID
}

func (rsp *response) ClientID() uint64 {
	return rsp.clientID
}

func (rsp *response) Method() string {
	return rsp.method
}

func (rsp *response) Data() []byte {
	return rsp.data
}

// Set Data to response, set it in RPC.
func (rsp *response) SetData(data []byte) {
	rsp.data = data
}

type message struct {
	*geminio.MessageAttribute
	err  error
	data []byte
	//custom []byte
	// ids
	id       uint64
	clientID uint64
	streamID uint64
	// we need stream to handle ack
	sm *stream
}

func (msg *message) Error(err error) {
	return
}

func (msg *message) Done() error {
	return nil
}

func (msg *message) ID() uint64 {
	return msg.id
}

func (msg *message) ClientID() uint64 {
	return msg.clientID
}

func (msg *message) StreamID() uint64 {
	return msg.streamID
}

func (msg *message) Cnss() geminio.Cnss {
	return geminio.Cnss(msg.MessageAttribute.Cnss)
}

func (msg *message) Timeout() time.Duration {
	return msg.MessageAttribute.Timeout
}

func (msg *message) Data() []byte {
	return msg.data
}
