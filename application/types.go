package application

import (
	"errors"
	"time"

	"github.com/singchia/geminio/options"
)

// request implements geminio.Request
type request struct {
	method   string
	data     []byte
	custom   []byte
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

func (req *request) Custom() []byte {
	return req.custom
}

func (req *request) SetTimeout(timeout time.Duration) {
	req.timeout = timeout
}

func (req *request) SetCustom(custom []byte) {
	req.custom = custom
}

func (req *request) SetClientID(clientID uint64) {
	req.clientID = clientID
}

func (req *request) SetStreamID(streamID uint64) {
	req.streamID = streamID
}

// response implements geminio.Response
type response struct {
	err    error
	method string
	data   []byte
	custom []byte
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

func (rsp *response) Custom() []byte {
	return rsp.custom
}

// Set Data to response, set it in RPC.
func (rsp *response) SetData(data []byte) {
	rsp.data = data
}

func (rsp *response) SetCustom(data []byte) {
	rsp.custom = data
}

func (rsp *response) SetClientID(clientID uint64) {
	rsp.clientID = clientID
}

func (rsp *response) SetStreamID(streamID uint64) {
	rsp.streamID = streamID
}

type message struct {
	err    error
	data   []byte
	custom []byte
	// ids
	id       uint64
	clientID uint64
	streamID uint64
	topic    string
	// meta
	timeout time.Duration
	cnss    options.Cnss
	// we need stream to handle ack
	sm *stream
}

func (msg *message) Error(err error) error {
	if msg.sm == nil {
		return errors.New("message' stream is nil")
	}
	return msg.sm.ackMessage(msg.id, err)
}

func (msg *message) Done() error {
	if msg.sm == nil {
		return errors.New("message' stream is nil")
	}
	return msg.sm.ackMessage(msg.id, nil)
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

func (msg *message) Cnss() options.Cnss {
	return msg.cnss
}

func (msg *message) Timeout() time.Duration {
	return msg.timeout
}

func (msg *message) Topic() string {
	return msg.topic
}

func (msg *message) Data() []byte {
	return msg.data
}

func (msg *message) Custom() []byte {
	return msg.custom
}

func (msg *message) SetTimeout(timeout time.Duration) {
	msg.timeout = timeout
}

func (msg *message) SetCustom(custom []byte) {
	msg.custom = custom
}

func (msg *message) SetTopic(topic string) {
	msg.topic = topic
}

func (msg *message) SetClientID(clientID uint64) {
	msg.clientID = clientID
}

func (msg *message) SetStreamID(streamID uint64) {
	msg.streamID = streamID
}
