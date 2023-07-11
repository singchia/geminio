package packet

import (
	"encoding/binary"
	"encoding/json"
	"io"

	"github.com/jumboframes/armorigo/log"
)

type SessionAbove interface {
	ClientID() uint64
	SessionID() uint64
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

func (snPkt *SessionPacket) NegotiateID() uint64 {
	return snPkt.negotiateID
}

func (snPkt *SessionPacket) SessionID() uint64 {
	return snPkt.negotiateID
}

func (snPkt *SessionPacket) SessionIDAcquire() bool {
	return snPkt.sessionIDAcquire
}

func (snPkt *SessionPacket) Encode() ([]byte, error) {
	hdr, err := snPkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(snPkt.SessionData)
	if err != nil {
		return nil, err
	}
	length := len(data) + 10
	pkt := make([]byte, length)
	pkt[0] = snPkt.SessionFlags.Priority
	pkt[1] |= byte(snPkt.SessionFlags.Qos) << 4 >> 4
	if snPkt.SessionFlags.sessionIDAcquire {
		pkt[1] |= 0x10
	}
	binary.BigEndian.PutUint64(pkt[2:10], snPkt.negotiateID)
	copy(pkt[10:length], data)

	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, pkt...), nil
}

func (snPkt *SessionPacket) Decode(data []byte) (uint32, error) {
	length := int(snPkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	snPkt.SessionFlags.Priority = data[0]
	snPkt.SessionFlags.Qos = int8(data[1] & 0x0F)
	snPkt.SessionFlags.sessionIDAcquire = (data[1] & 0x10) != 0
	snPkt.negotiateID = binary.BigEndian.Uint64(data[2:10])
	// data
	snData := &SessionData{}
	err := json.Unmarshal(data[10:length], snData)
	if err != nil {
		log.Errorf("session packet decode err: %s", err)
		return 0, err
	}
	snPkt.SessionData = snData
	return uint32(length), nil
}

func (snPkt *SessionPacket) DecodeFromReader(reader io.Reader) error {
	length := int(snPkt.PacketLen)
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	snPkt.SessionFlags.Priority = data[0]
	snPkt.SessionFlags.Qos = int8(data[1] & 0x0F)
	snPkt.SessionFlags.sessionIDAcquire = (data[1] & 0x10) != 0
	snPkt.negotiateID = binary.BigEndian.Uint64(data[2:10])
	// data
	snData := &SessionData{}
	err = json.Unmarshal(data[10:length], snData)
	if err != nil {
		log.Errorf("session packet decode from reader err: %s", err)
		return err
	}
	snPkt.SessionData = snData
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

func (snAckPkt *SessionAckPacket) NegotiateID() uint64 {
	return snAckPkt.negotiateID
}

func (snAckPkt *SessionAckPacket) SessionID() uint64 {
	return snAckPkt.sessionID
}

func (snAckPkt *SessionAckPacket) SetError(err error) {
	snAckPkt.SessionData.Error = err.Error()
}

func (snAckPkt *SessionAckPacket) Encode() ([]byte, error) {
	hdr, err := snAckPkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(snAckPkt.SessionData)
	if err != nil {
		return nil, err
	}
	length := len(data) + 18
	pkt := make([]byte, length)
	// session id
	binary.BigEndian.PutUint64(pkt[2:10], snAckPkt.negotiateID)
	binary.BigEndian.PutUint64(pkt[10:18], snAckPkt.sessionID)
	copy(pkt[18:length], data)

	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, pkt...), nil
}

func (snAckPkt *SessionAckPacket) Decode(data []byte) (uint32, error) {
	length := int(snAckPkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	snAckPkt.negotiateID = binary.BigEndian.Uint64(data[2:10])
	snAckPkt.sessionID = binary.BigEndian.Uint64(data[10:18])
	// data
	snData := &SessionData{}
	err := json.Unmarshal(data[18:length], snData)
	if err != nil {
		log.Errorf("session ack packet decode err: %s", err)
		return 0, err
	}
	snAckPkt.SessionData = snData
	return uint32(length), nil
}

func (snAckPkt *SessionAckPacket) DecodeFromReader(reader io.Reader) error {
	length := int(snAckPkt.PacketLen)
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	snAckPkt.negotiateID = binary.BigEndian.Uint64(data[2:10])
	snAckPkt.sessionID = binary.BigEndian.Uint64(data[10:18])
	// data
	snData := &SessionData{}
	err = json.Unmarshal(data[18:length], snData)
	if err != nil {
		log.Errorf("session ack packet decode from reader err: %s", err)
		return err
	}
	snAckPkt.SessionData = snData
	return nil
}

type DismissPacket struct {
	*PacketHeader
	sessionID   uint64
	SessionData *SessionData

	// the following fields are not encoded into packet
	basePacket
}

func (disPkt *DismissPacket) SessionID() uint64 {
	return disPkt.sessionID
}

func (disPkt *DismissPacket) Encode() ([]byte, error) {
	hdr, err := disPkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(disPkt.SessionData)
	if err != nil {
		return nil, err
	}
	length := len(data) + 8
	pkt := make([]byte, length)
	// session id
	binary.BigEndian.PutUint64(pkt[:8], disPkt.sessionID)
	// data
	copy(pkt[8:length], data)
	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, pkt...), nil
}

func (disPkt *DismissPacket) Decode(data []byte) (uint32, error) {
	length := int(disPkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	// session id
	disPkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	disData := &SessionData{}
	err := json.Unmarshal(data[8:length], disData)
	if err != nil {
		log.Errorf("dismiss packet decode err: %s", err)
		return 0, err
	}
	disPkt.SessionData = disData
	return uint32(length), nil
}

func (disPkt *DismissPacket) DecodeFromReader(reader io.Reader) error {
	length := int(disPkt.PacketLen)
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		log.Errorf("dismiss packet decode from reader err: %s", err)
		return err
	}
	// session id
	disPkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	disData := &SessionData{}
	err = json.Unmarshal(data[8:length], disData)
	if err != nil {
		return err
	}
	disPkt.SessionData = disData
	return nil
}

type DismissAckPacket struct {
	*PacketHeader
	sessionID   uint64
	SessionData *SessionData

	// the following fields are not encoded into packet
	basePacket
}

func (disAckPkt *DismissAckPacket) SessionID() uint64 {
	return disAckPkt.sessionID
}

func (disAckPkt *DismissAckPacket) Encode() ([]byte, error) {
	hdr, err := disAckPkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(disAckPkt.SessionData)
	if err != nil {
		return nil, err
	}
	length := len(data) + 8
	pkt := make([]byte, length)
	// session id
	binary.BigEndian.PutUint64(pkt[:8], disAckPkt.sessionID)
	copy(pkt[8:length], data)

	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, pkt...), nil
}

func (disAckPkt *DismissAckPacket) Decode(data []byte) (uint32, error) {
	length := int(disAckPkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	// session id
	disAckPkt.sessionID = binary.BigEndian.Uint64(data[:8])
	// data
	disData := &SessionData{}
	err := json.Unmarshal(data[8:length], disData)
	if err != nil {
		return 0, err
	}
	disAckPkt.SessionData = disData
	return uint32(length), nil
}

func (disAckPkt *DismissAckPacket) DecodeFromReader(reader io.Reader) error {
	length := int(disAckPkt.PacketLen)
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	// session id
	disAckPkt.sessionID = binary.BigEndian.Uint64(data[0:8])
	// data
	disData := &SessionData{}
	err = json.Unmarshal(data[8:length], disData)
	if err != nil {
		return err
	}
	disAckPkt.SessionData = disData
	return nil
}
