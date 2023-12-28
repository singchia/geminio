package packet

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"time"
)

type AppAbove interface {
	SessionAbove
}

// Request packet for RPC call
type RequestPacket struct {
	*MessagePacket
}

// Response packet for RPC handler
type ResponsePacket struct {
	*MessageAckPacket
}

type RequestCancelType int16

const (
	RequestCancelTypeCanceled         RequestCancelType = 1
	RequestCancelTypeDeadlineExceeded RequestCancelType = 2
)

// Request cancel packet for RPC cancelation
type RequestCancelPacket struct {
	*PacketHeader
	sessionID  uint64
	cancelType RequestCancelType
}

func (pkt *RequestCancelPacket) SessionID() uint64 {
	return pkt.sessionID
}

func (pkt *RequestCancelPacket) SetSessionID(sessionID uint64) {
	pkt.sessionID = sessionID
}

func (pkt *RequestCancelPacket) Encode() ([]byte, error) {
	hdr, err := pkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	length := 10
	next := make([]byte, length)
	// session id
	binary.BigEndian.PutUint64(next[:8], pkt.sessionID)
	// cancel type
	binary.BigEndian.PutUint16(next[8:10], uint16(pkt.cancelType))
	// set next length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, next...), nil
}

func (pkt *RequestCancelPacket) Decode(data []byte) (uint32, error) {
	if len(data) < int(pkt.PacketLen) {
		return 0, ErrIncompletePacket
	}
	// session id
	pkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// cancel type
	pkt.cancelType = RequestCancelType(binary.BigEndian.Uint16(data[8:10]))
	return pkt.PacketLen, nil
}

func (pkt *RequestCancelPacket) DecodeFromReader(reader io.Reader) error {
	if pkt.PacketLen < 8 {
		return ErrIllegalPacket
	}
	data := make([]byte, pkt.PacketLen)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	// session id
	pkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// cancel type
	pkt.cancelType = RequestCancelType(binary.BigEndian.Uint16(data[8:pkt.PacketLen]))
	return nil
}

type MessagePacket struct {
	*PacketHeader
	sessionID uint64
	Data      *MessageData

	// the following fields are not encoded into packet
	basePacket
}

// TODO 待优化
type MessageData struct {
	Key      []byte        `json:"key,omitempty"`
	Value    []byte        `json:"value,omitempty"`
	Custom   []byte        `json:"custom,omitempty"`
	Error    string        `json:"error,omitempty"`
	Timeout  time.Duration `json:"timeout,omitempty"`
	Deadline time.Time     `json:"deadline,omitempty"`
	Context  struct {
		Deadline time.Time `json:"deadline,omitempty"`
	} `json:"context,omitempty"`
}

func (pkt *MessagePacket) SessionID() uint64 {
	return pkt.sessionID
}

func (pkt *MessagePacket) SetSessionID(sessionID uint64) {
	pkt.sessionID = sessionID
}

func (pkt *MessagePacket) Encode() ([]byte, error) {
	hdr, err := pkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(pkt.Data)
	if err != nil {
		return nil, err
	}
	length := len(data) + 8
	next := make([]byte, length)
	// session id
	binary.BigEndian.PutUint64(next[:8], pkt.sessionID)
	// data
	copy(next[8:length], data)
	// set next length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, next...), nil
}

func (pkt *MessagePacket) Decode(data []byte) (uint32, error) {
	if len(data) < int(pkt.PacketLen) {
		return 0, ErrIncompletePacket
	}
	// session id
	pkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	msgData := &MessageData{}
	err := json.Unmarshal(data[8:pkt.PacketLen], msgData)
	if err != nil {
		return 0, err
	}
	pkt.Data = msgData
	return pkt.PacketLen, nil
}

func (pkt *MessagePacket) DecodeFromReader(reader io.Reader) error {
	if pkt.PacketLen < 8 {
		return errors.New("illegal packet")
	}
	data := make([]byte, pkt.PacketLen)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	// session id
	pkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	msgData := &MessageData{}
	err = json.Unmarshal(data[8:pkt.PacketLen], msgData)
	if err != nil {
		return err
	}
	pkt.Data = msgData
	return nil
}

type MessageAckPacket struct {
	*PacketHeader
	sessionID uint64
	Data      *MessageData

	// the following fields are not encoded into packet
	basePacket
}

func (pkt *MessageAckPacket) SessionID() uint64 {
	return pkt.sessionID
}

func (pkt *MessageAckPacket) SetSessionID(sessionID uint64) {
	pkt.sessionID = sessionID
}

func (pkt *MessageAckPacket) Encode() ([]byte, error) {
	hdr, err := pkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(pkt.Data)
	if err != nil {
		return nil, err
	}
	length := len(data) + 8
	next := make([]byte, length)
	// session id
	binary.BigEndian.PutUint64(next[:8], pkt.sessionID)
	// data
	copy(next[8:length], data)

	// set next length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, next...), nil
}

func (pkt *MessageAckPacket) Decode(data []byte) (uint32, error) {
	if len(data) < int(pkt.PacketLen) {
		return 0, ErrIncompletePacket
	}
	// session id
	pkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	msgData := &MessageData{}
	err := json.Unmarshal(data[8:pkt.PacketLen], msgData)
	if err != nil {
		return 0, err
	}
	pkt.Data = msgData
	return pkt.PacketLen, nil
}

func (pkt *MessageAckPacket) DecodeFromReader(reader io.Reader) error {
	if pkt.PacketLen < 8 {
		return errors.New("illegal packet")
	}
	data := make([]byte, pkt.PacketLen)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	// session id
	pkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	msgData := &MessageData{}
	err = json.Unmarshal(data[8:pkt.PacketLen], msgData)
	if err != nil {
		return err
	}
	pkt.Data = msgData
	return nil
}

type StreamPacket struct {
	*PacketHeader
	sessionID uint64
	Data      []byte

	// the following fields are not encoded into packet
	basePacket
}

func (pkt *StreamPacket) Length() int {
	return len(pkt.Data)
}

func (pkt *StreamPacket) SessionID() uint64 {
	return pkt.sessionID
}

func (pkt *StreamPacket) SetSessionID(sessionID uint64) {
	pkt.sessionID = sessionID
}

func (pkt *StreamPacket) Encode() ([]byte, error) {
	hdr, err := pkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	length := len(pkt.Data) + 8
	next := make([]byte, length)
	// session id
	binary.BigEndian.PutUint64(next[:8], pkt.sessionID)
	// data
	copy(next[8:length], pkt.Data)
	// set next length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, next...), nil
}

func (pkt *StreamPacket) Decode(data []byte) (uint32, error) {
	length := int(pkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	// session id
	pkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	pkt.Data = data[8:length]
	return uint32(length), nil
}

func (pkt *StreamPacket) DecodeFromReader(reader io.Reader) error {
	if pkt.PacketLen < 8 {
		return errors.New("illegal packet")
	}
	length := int(pkt.PacketLen)
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	// session id
	pkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	pkt.Data = data[8:length]
	return nil
}

type RegisterPacket struct {
	*PacketHeader
	sessionID uint64
	method    []byte

	// the following fields are not encoded into packet
	basePacket
}

func (pkt *RegisterPacket) Length() int {
	return len(pkt.method)
}

func (pkt *RegisterPacket) Method() string {
	return string(pkt.method)
}

func (pkt *RegisterPacket) SessionID() uint64 {
	return pkt.sessionID
}

func (pkt *RegisterPacket) SetSessionID(sessionID uint64) {
	pkt.sessionID = sessionID
}

func (pkt *RegisterPacket) Encode() ([]byte, error) {
	hdr, err := pkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	length := len(pkt.method) + 8
	next := make([]byte, length)
	// sessin id
	binary.BigEndian.PutUint64(next[:8], pkt.sessionID)
	// method
	copy(next[8:length], pkt.method)
	// set next length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, next...), nil
}

func (pkt *RegisterPacket) Decode(data []byte) (uint32, error) {
	length := int(pkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	// session id
	pkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// method
	pkt.method = data[8:length]
	return uint32(length), nil
}

func (pkt *RegisterPacket) DecodeFromReader(reader io.Reader) error {
	if pkt.PacketLen < 8 {
		return errors.New("illegal packet")
	}
	length := int(pkt.PacketLen)
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	pkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// method
	pkt.method = data[8:length]
	return nil
}

type RegisterData struct {
	Error string `json:"error,omitempty"`
}

type RegisterAckPacket struct {
	*PacketHeader
	sessionID    uint64
	RegisterData *RegisterData

	// the following fields are not encoded into packet
	basePacket
}

func (pkt *RegisterAckPacket) SessionID() uint64 {
	return pkt.sessionID
}

func (pkt *RegisterAckPacket) SetSessionID(sessionID uint64) {
	pkt.sessionID = sessionID
}

func (pkt *RegisterAckPacket) Encode() ([]byte, error) {
	hdr, err := pkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(pkt.RegisterData)
	if err != nil {
		return nil, err
	}
	length := len(data) + 8
	next := make([]byte, length)
	// sessin id
	binary.BigEndian.PutUint64(next[:8], pkt.sessionID)
	// data
	copy(next[8:length], data)
	// set next length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, next...), nil
}

func (pkt *RegisterAckPacket) Decode(data []byte) (uint32, error) {
	length := int(pkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	// session id
	pkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	registerData := &RegisterData{}
	err := json.Unmarshal(data[8:length], registerData)
	if err != nil {
		return 0, err
	}
	pkt.RegisterData = registerData
	return uint32(length), nil
}

func (pkt *RegisterAckPacket) DecodeFromReader(reader io.Reader) error {
	if pkt.PacketLen < 8 {
		return errors.New("illegal packet")
	}
	length := int(pkt.PacketLen)

	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	// session id
	pkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	registerData := &RegisterData{}
	err = json.Unmarshal(data[8:length], registerData)
	if err != nil {
		return err
	}
	pkt.RegisterData = registerData
	return nil
}
