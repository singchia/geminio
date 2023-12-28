package packet

import (
	"encoding/binary"
	"encoding/json"
	"io"

	"github.com/jumboframes/armorigo/log"
)

type SessionAbove interface {
	SessionID() uint64
	SetSessionID(sessionID uint64)
}

type SessionFlags struct {
	Priority         uint8 // 8 bits
	Qos              int8  // 4 bits, unused now
	sessionIDAcquire bool  // If peer's call to assign sessionID 1 bit
	// reserved 3 bits
}

type SessionPacket struct {
	*PacketHeader
	SessionFlags              // 16 bits
	negotiateID  uint64       // 64 bits
	SessionData  *SessionData // elastic fields

	// the following fields are not encoded into packet
	basePacket
}

type SessionData struct {
	Meta  []byte `json:"meta,omitempty"`
	Error string `json:"error,omitempty"`
	Peer  string `json:"peer,omitempty"`
}

func SessionLayer(pkt Packet) bool {
	if pkt.Type() == TypeSessionPacket ||
		pkt.Type() == TypeSessionAckPacket ||
		pkt.Type() == TypeDismissPacket ||
		pkt.Type() == TypeDismissAckPacket {
		return true
	}
	return false
}

func (pkt *SessionPacket) NegotiateID() uint64 {
	return pkt.negotiateID
}

func (pkt *SessionPacket) SessionID() uint64 {
	return pkt.negotiateID
}

func (pkt *SessionPacket) SetSessionID(sessionID uint64) {
	pkt.negotiateID = sessionID
}

func (pkt *SessionPacket) SessionIDAcquire() bool {
	return pkt.sessionIDAcquire
}

func (pkt *SessionPacket) Encode() ([]byte, error) {
	hdr, err := pkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(pkt.SessionData)
	if err != nil {
		return nil, err
	}
	length := len(data) + 10
	next := make([]byte, length)
	next[0] = pkt.SessionFlags.Priority
	next[1] |= byte(pkt.SessionFlags.Qos) << 4 >> 4
	if pkt.SessionFlags.sessionIDAcquire {
		next[1] |= 0x10
	}
	binary.BigEndian.PutUint64(next[2:10], pkt.negotiateID)
	copy(next[10:length], data)

	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, next...), nil
}

func (pkt *SessionPacket) Decode(data []byte) (uint32, error) {
	length := int(pkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	pkt.SessionFlags.Priority = data[0]
	pkt.SessionFlags.Qos = int8(data[1] & 0x0F)
	pkt.SessionFlags.sessionIDAcquire = (data[1] & 0x10) != 0
	pkt.negotiateID = binary.BigEndian.Uint64(data[2:10])
	// data
	snData := &SessionData{}
	err := json.Unmarshal(data[10:length], snData)
	if err != nil {
		log.Errorf("session packet decode err: %s", err)
		return 0, err
	}
	pkt.SessionData = snData
	return uint32(length), nil
}

func (pkt *SessionPacket) DecodeFromReader(reader io.Reader) error {
	length := int(pkt.PacketLen)
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	pkt.SessionFlags.Priority = data[0]
	pkt.SessionFlags.Qos = int8(data[1] & 0x0F)
	pkt.SessionFlags.sessionIDAcquire = (data[1] & 0x10) != 0
	pkt.negotiateID = binary.BigEndian.Uint64(data[2:10])
	// data
	snData := &SessionData{}
	err = json.Unmarshal(data[10:length], snData)
	if err != nil {
		log.Errorf("session packet decode from reader err: %s", err)
		return err
	}
	pkt.SessionData = snData
	return nil
}

type SessionAckPacket struct {
	*PacketHeader
	SessionFlags        // 16 bits, unused now
	negotiateID  uint64 // 8 bytes
	sessionID    uint64 // 8 bytes
	SessionData  *SessionData

	// the following fields are not encoded into packet
	basePacket
}

func (pkt *SessionAckPacket) NegotiateID() uint64 {
	return pkt.negotiateID
}

func (pkt *SessionAckPacket) SessionID() uint64 {
	return pkt.sessionID
}

func (pkt *SessionAckPacket) SetSessionID(sessionID uint64) {
	pkt.sessionID = sessionID
}

func (pkt *SessionAckPacket) SetError(err error) {
	pkt.SessionData.Error = err.Error()
}

func (pkt *SessionAckPacket) Encode() ([]byte, error) {
	hdr, err := pkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(pkt.SessionData)
	if err != nil {
		return nil, err
	}
	length := len(data) + 18
	next := make([]byte, length)
	// session id
	binary.BigEndian.PutUint64(next[2:10], pkt.negotiateID)
	binary.BigEndian.PutUint64(next[10:18], pkt.sessionID)
	copy(next[18:length], data)

	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, next...), nil
}

func (pkt *SessionAckPacket) Decode(data []byte) (uint32, error) {
	length := int(pkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	pkt.negotiateID = binary.BigEndian.Uint64(data[2:10])
	pkt.sessionID = binary.BigEndian.Uint64(data[10:18])
	// data
	snData := &SessionData{}
	err := json.Unmarshal(data[18:length], snData)
	if err != nil {
		log.Errorf("session ack packet decode err: %s", err)
		return 0, err
	}
	pkt.SessionData = snData
	return uint32(length), nil
}

func (pkt *SessionAckPacket) DecodeFromReader(reader io.Reader) error {
	length := int(pkt.PacketLen)
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	pkt.negotiateID = binary.BigEndian.Uint64(data[2:10])
	pkt.sessionID = binary.BigEndian.Uint64(data[10:18])
	// data
	snData := &SessionData{}
	err = json.Unmarshal(data[18:length], snData)
	if err != nil {
		log.Errorf("session ack packet decode from reader err: %s", err)
		return err
	}
	pkt.SessionData = snData
	return nil
}

type DismissPacket struct {
	*PacketHeader
	sessionID   uint64
	SessionData *SessionData

	// the following fields are not encoded into packet
	basePacket
}

func (pkt *DismissPacket) SessionID() uint64 {
	return pkt.sessionID
}

func (pkt *DismissPacket) SetSessionID(sessionID uint64) {
	pkt.sessionID = sessionID
}

func (pkt *DismissPacket) Encode() ([]byte, error) {
	hdr, err := pkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(pkt.SessionData)
	if err != nil {
		return nil, err
	}
	length := len(data) + 8
	next := make([]byte, length)
	// session id
	binary.BigEndian.PutUint64(next[:8], pkt.sessionID)
	// data
	copy(next[8:length], data)
	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, next...), nil
}

func (pkt *DismissPacket) Decode(data []byte) (uint32, error) {
	length := int(pkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	// session id
	pkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	disData := &SessionData{}
	err := json.Unmarshal(data[8:length], disData)
	if err != nil {
		log.Errorf("dismiss packet decode err: %s", err)
		return 0, err
	}
	pkt.SessionData = disData
	return uint32(length), nil
}

func (pkt *DismissPacket) DecodeFromReader(reader io.Reader) error {
	length := int(pkt.PacketLen)
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		log.Errorf("dismiss packet decode from reader err: %s", err)
		return err
	}
	// session id
	pkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	disData := &SessionData{}
	err = json.Unmarshal(data[8:length], disData)
	if err != nil {
		return err
	}
	pkt.SessionData = disData
	return nil
}

type DismissAckPacket struct {
	*PacketHeader
	sessionID   uint64
	SessionData *SessionData

	// the following fields are not encoded into packet
	basePacket
}

func (pkt *DismissAckPacket) SessionID() uint64 {
	return pkt.sessionID
}

func (pkt *DismissAckPacket) SetSessionID(sessionID uint64) {
	pkt.sessionID = sessionID
}

func (pkt *DismissAckPacket) Encode() ([]byte, error) {
	hdr, err := pkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(pkt.SessionData)
	if err != nil {
		return nil, err
	}
	length := len(data) + 8
	next := make([]byte, length)
	// session id
	binary.BigEndian.PutUint64(next[:8], pkt.sessionID)
	copy(next[8:length], data)

	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, next...), nil
}

func (pkt *DismissAckPacket) Decode(data []byte) (uint32, error) {
	length := int(pkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	// session id
	pkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	disData := &SessionData{}
	err := json.Unmarshal(data[8:length], disData)
	if err != nil {
		return 0, err
	}
	pkt.SessionData = disData
	return uint32(length), nil
}

func (pkt *DismissAckPacket) DecodeFromReader(reader io.Reader) error {
	length := int(pkt.PacketLen)
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	// session id
	pkt.sessionID = binary.BigEndian.Uint64(data[0:8])
	// data
	disData := &SessionData{}
	err = json.Unmarshal(data[8:length], disData)
	if err != nil {
		return err
	}
	pkt.SessionData = disData
	return nil
}
