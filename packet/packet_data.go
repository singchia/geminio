package packet

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
)

type RequestPacket struct {
	*MessagePacket
}

type ResponsePacket struct {
	*MessageAckPacket
}

type MessagePacket struct {
	*PacketHeader
	sessionID   uint64
	MessageData *MessageData

	// the following fields are not encoded into packet
	basePacket
}

// TODO 待优化
type MessageData struct {
	Key    []byte `json:"key,omitempty"`
	Value  []byte `json:"value,omitempty"`
	Custom []byte `json:"custom,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (msgPkt *MessagePacket) SessionID() uint64 {
	return msgPkt.sessionID
}

func (msgPkt *MessagePacket) Encode() ([]byte, error) {
	hdr, err := msgPkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(msgPkt.MessageData)
	if err != nil {
		return nil, err
	}
	length := len(data) + 8
	pkt := make([]byte, length)
	// session id
	binary.BigEndian.PutUint64(pkt[:8], msgPkt.sessionID)
	// data
	copy(pkt[8:length], data)
	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, pkt...), nil
}

func (msgPkt *MessagePacket) Decode(data []byte) (uint32, error) {
	if len(data) < int(msgPkt.PacketLen) {
		return 0, ErrIncompletePacket
	}
	// session id
	msgPkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	msgData := &MessageData{}
	err := json.Unmarshal(data[8:msgPkt.PacketLen], msgData)
	if err != nil {
		return 0, err
	}
	msgPkt.MessageData = msgData
	return msgPkt.PacketLen, nil
}

func (msgPkt *MessagePacket) DecodeFromReader(reader io.Reader) error {
	if msgPkt.PacketLen < 8 {
		return errors.New("illegal packet")
	}
	data := make([]byte, msgPkt.PacketLen)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	// session id
	msgPkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	msgData := &MessageData{}
	err = json.Unmarshal(data[8:msgPkt.PacketLen], msgData)
	if err != nil {
		return err
	}
	msgPkt.MessageData = msgData
	return nil
}

type MessageAckPacket struct {
	*PacketHeader
	sessionID   uint64
	MessageData *MessageData

	// the following fields are not encoded into packet
	basePacket
}

func (msgPkt *MessageAckPacket) SessionID() uint64 {
	return msgPkt.sessionID
}

func (msgPkt *MessageAckPacket) Encode() ([]byte, error) {
	hdr, err := msgPkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(msgPkt.MessageData)
	if err != nil {
		return nil, err
	}
	length := len(data) + 8
	pkt := make([]byte, length)
	// session id
	binary.BigEndian.PutUint64(pkt[:8], msgPkt.sessionID)
	// data
	copy(pkt[8:length], data)

	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, pkt...), nil
}

func (msgPkt *MessageAckPacket) Decode(data []byte) (uint32, error) {
	if len(data) < int(msgPkt.PacketLen) {
		return 0, ErrIncompletePacket
	}
	// session id
	msgPkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	msgData := &MessageData{}
	err := json.Unmarshal(data[8:msgPkt.PacketLen], msgData)
	if err != nil {
		return 0, err
	}
	msgPkt.MessageData = msgData
	return msgPkt.PacketLen, nil
}

func (msgPkt *MessageAckPacket) DecodeFromReader(reader io.Reader) error {
	if msgPkt.PacketLen < 8 {
		return errors.New("illegal packet")
	}
	data := make([]byte, msgPkt.PacketLen)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	// session id
	msgPkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	msgData := &MessageData{}
	err = json.Unmarshal(data[8:msgPkt.PacketLen], msgData)
	if err != nil {
		return err
	}
	msgPkt.MessageData = msgData
	return nil
}

type StreamPacket struct {
	*PacketHeader
	sessionID uint64
	Data      []byte

	// the following fields are not encoded into packet
	basePacket
}

func (streamPkt *StreamPacket) SessionID() uint64 {
	return streamPkt.sessionID
}

func (streamPkt *StreamPacket) Encode() ([]byte, error) {
	hdr, err := streamPkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	length := len(streamPkt.Data) + 8
	pkt := make([]byte, length)
	// session id
	binary.BigEndian.PutUint64(pkt[:8], streamPkt.sessionID)
	// data
	copy(pkt[8:length], streamPkt.Data)
	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, pkt...), nil
}

func (streamPkt *StreamPacket) Decode(data []byte) (uint32, error) {
	length := int(streamPkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	// session id
	streamPkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	streamPkt.Data = data[8:length]
	return uint32(length), nil
}

func (streamPkt *StreamPacket) DecodeFromReader(reader io.Reader) error {
	if streamPkt.PacketLen < 8 {
		return errors.New("illegal packet")
	}
	length := int(streamPkt.PacketLen)
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	// session id
	streamPkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	streamPkt.Data = data[8:length]
	return nil
}

type RegisterPacket struct {
	*PacketHeader
	SessionID uint64
	Method    []byte

	// the following fields are not encoded into packet
	basePacket
}

func (registerPkt *RegisterPacket) Encode() ([]byte, error) {
	hdr, err := registerPkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	length := len(registerPkt.Method) + 8
	pkt := make([]byte, length)
	// sessin id
	binary.BigEndian.PutUint64(pkt[:8], registerPkt.SessionID)
	// method
	copy(pkt[8:length], registerPkt.Method)
	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, pkt...), nil
}

func (registerPkt *RegisterPacket) Decode(data []byte) (uint32, error) {
	length := int(registerPkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	// session id
	registerPkt.SessionID = binary.BigEndian.Uint64(data[:8])
	// method
	registerPkt.Method = data[8:length]
	return uint32(length), nil
}

func (registerPkt *RegisterPacket) DecodeFromReader(reader io.Reader) error {
	if registerPkt.PacketLen < 8 {
		return errors.New("illegal packet")
	}
	length := int(registerPkt.PacketLen)
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	registerPkt.SessionID = binary.BigEndian.Uint64(data[:8])
	// method
	registerPkt.Method = data[8:length]
	return nil
}

type RegisterData struct {
	Error string `json:"error,omitempty"`
}

type RegisterAckPacket struct {
	*PacketHeader
	SessionID    uint64
	RegisterData *RegisterData

	// the following fields are not encoded into packet
	basePacket
}

func (registerAckPkt *RegisterAckPacket) Encode() ([]byte, error) {
	hdr, err := registerAckPkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(registerAckPkt.RegisterData)
	if err != nil {
		return nil, err
	}
	length := len(data) + 8
	pkt := make([]byte, length)
	// sessin id
	binary.BigEndian.PutUint64(pkt[:8], registerAckPkt.SessionID)
	// data
	copy(pkt[8:length], data)
	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, pkt...), nil
}

func (registerAckPkt *RegisterAckPacket) Decode(data []byte) (uint32, error) {
	length := int(registerAckPkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	// session id
	registerAckPkt.SessionID = binary.BigEndian.Uint64(data[:8])
	// data
	registerData := &RegisterData{}
	err := json.Unmarshal(data[8:length], registerData)
	if err != nil {
		return 0, err
	}
	registerAckPkt.RegisterData = registerData
	return uint32(length), nil
}

func (registerAckPkt *RegisterAckPacket) DecodeFromReader(reader io.Reader) error {
	if registerAckPkt.PacketLen < 8 {
		return errors.New("illegal packet")
	}
	length := int(registerAckPkt.PacketLen)

	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	// session id
	registerAckPkt.SessionID = binary.BigEndian.Uint64(data[:8])
	// data
	registerData := &RegisterData{}
	err = json.Unmarshal(data[8:length], registerData)
	if err != nil {
		return err
	}
	registerAckPkt.RegisterData = registerData
	return nil
}
