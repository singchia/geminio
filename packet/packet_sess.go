package packet

import (
	"encoding/binary"
	"encoding/json"
	"io"
)

// TODO
type SessionFlags struct {
	Priority uint32
	Qos      uint32
}

type SessionPacket struct {
	*PacketHeader
	SessionFlags
	SessionID   uint64
	SessionData *SessionData
}

type SessionData struct {
	Meta []byte `json:"meta,omitempty"`
	//Error error  `json:"error,omitempty"`
	Error string `json:"error,omitempty"`
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
	length := len(data) + 16
	pkt := make([]byte, length)
	binary.BigEndian.PutUint32(pkt[0:4], snPkt.SessionFlags.Priority)
	binary.BigEndian.PutUint32(pkt[4:8], snPkt.SessionFlags.Qos)
	binary.BigEndian.PutUint64(pkt[8:16], snPkt.SessionID)
	copy(pkt[16:length], data)

	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, pkt...), nil
}

func (snPkt *SessionPacket) Decode(data []byte) (uint32, error) {
	length := int(snPkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	snPkt.SessionFlags.Priority = binary.BigEndian.Uint32(data[0:4])
	snPkt.SessionFlags.Qos = binary.BigEndian.Uint32(data[0:8])
	snPkt.SessionID = binary.BigEndian.Uint64(data[8:16])
	// data
	snData := &SessionData{}
	err := json.Unmarshal(data[16:length], snData)
	if err != nil {
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
	snPkt.SessionFlags.Priority = binary.BigEndian.Uint32(data[0:4])
	snPkt.SessionFlags.Qos = binary.BigEndian.Uint32(data[0:8])
	snPkt.SessionID = binary.BigEndian.Uint64(data[8:16])
	// data
	snData := &SessionData{}
	err = json.Unmarshal(data[16:length], snData)
	if err != nil {
		return err
	}
	snPkt.SessionData = snData
	return nil
}

type SessionAckPacket struct {
	*PacketHeader
	SessionFlags // TODO acked flags
	SessionID    uint64
	SessionData  *SessionData
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
	length := len(data) + 16
	pkt := make([]byte, length)
	// session id
	binary.BigEndian.PutUint64(pkt[8:16], snAckPkt.SessionID)
	copy(pkt[16:length], data)

	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, pkt...), nil
}

func (snAckPkt *SessionAckPacket) Decode(data []byte) (uint32, error) {
	length := int(snAckPkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	snAckPkt.SessionID = binary.BigEndian.Uint64(data[8:16])
	// data
	snData := &SessionData{}
	err := json.Unmarshal(data[16:length], snData)
	if err != nil {
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
	snAckPkt.SessionID = binary.BigEndian.Uint64(data[8:16])
	// data
	snData := &SessionData{}
	err = json.Unmarshal(data[16:length], snData)
	if err != nil {
		return err
	}
	snAckPkt.SessionData = snData
	return nil
}

type DismissPacket struct {
	*PacketHeader
	SessionID   uint64
	SessionData *SessionData
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
	binary.BigEndian.PutUint64(pkt[:8], disPkt.SessionID)
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
	disPkt.SessionID = binary.BigEndian.Uint64(data[:8])
	// data
	disData := &SessionData{}
	err := json.Unmarshal(data[8:length], disData)
	if err != nil {
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
		return err
	}
	// session id
	disPkt.SessionID = binary.BigEndian.Uint64(data[:8])
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
	SessionID   uint64
	SessionData *SessionData
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
	binary.BigEndian.PutUint64(pkt[:8], disAckPkt.SessionID)
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
	disAckPkt.SessionID = binary.BigEndian.Uint64(data[:8])
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
	disAckPkt.SessionID = binary.BigEndian.Uint64(data[0:8])
	// data
	disData := &SessionData{}
	err = json.Unmarshal(data[8:length], disData)
	if err != nil {
		return err
	}
	disAckPkt.SessionData = disData
	return nil
}
