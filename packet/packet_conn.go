package packet

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"time"
)

type ConnAbove interface {
	ClientID() uint64
	SetClientID(clientID uint64)
}

type Heartbeat time.Duration

const (
	Heartbeat5   Heartbeat = 5
	Heartbeat20  Heartbeat = 20
	Heartbeat80  Heartbeat = 80
	Heartbeat320 Heartbeat = 320
)

type ConnFlags struct {
	Heartbeat       Heartbeat // heartbeat define 2 bits
	clientIDAcquire bool      // If peer's call to assign clientID 1 bit
	packetIDAcquire bool      // 是否云端分配packetID 1 bit
	Retain          bool      // 是否保留连接上下文 1 bit，TODO
	Clear           bool      // 是否清除连接上下文 1 bit，TODO
}

// TODO authN
type ConnPacket struct {
	*PacketHeader
	ConnFlags
	ClientID uint64
	ConnData *ConnData
}

func ConnLayer(pkt Packet) bool {
	if pkt.Type() == TypeConnPacket ||
		pkt.Type() == TypeConnAckPacket ||
		pkt.Type() == TypeDisConnPacket ||
		pkt.Type() == TypeDisConnAckPacket ||
		pkt.Type() == TypeHeartbeatPacket ||
		pkt.Type() == TypeHeartbeatAckPacket {
		return true
	}
	return false
}

func (connPkt *ConnPacket) ClientIDAcquire() bool {
	return connPkt.clientIDAcquire
}

func (connPkt *ConnPacket) Encode() ([]byte, error) {
	hdr, err := connPkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(connPkt.ConnData)
	if err != nil {
		return nil, err
	}
	length := 10 + len(data)
	pkt := make([]byte, length)
	// conn flags
	if connPkt.Retain {
		pkt[0] |= 0x01
	}
	if connPkt.Clear {
		pkt[0] |= 0x02
	}
	if connPkt.packetIDAcquire {
		pkt[0] |= 0x04
	}
	if connPkt.clientIDAcquire {
		pkt[0] |= 0x08
	}
	if connPkt.Heartbeat != Heartbeat5 {
		pkt[0] |= byte(math.Pow(float64(connPkt.Heartbeat)/5, 0.25)) << 4
	}
	// client id
	binary.BigEndian.PutUint64(pkt[2:10], connPkt.ClientID)
	// data
	copy(pkt[10:length], data)
	// header length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, pkt...), nil
}

func (connPkt *ConnPacket) Decode(data []byte) (uint32, error) {
	length := int(connPkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	// conn flags
	flags := data[:1]
	connPkt.Retain = (flags[0] & 0x01) != 0
	connPkt.Clear = (flags[0] & 0x02) != 0
	connPkt.packetIDAcquire = (flags[0] & 0x04) != 0
	connPkt.clientIDAcquire = (flags[0] & 0x08) != 0
	heartbeat := float64(flags[0] & 0x30 >> 4)
	connPkt.Heartbeat = Heartbeat(5 * math.Pow(4, heartbeat))
	// client id
	connPkt.ClientID = binary.BigEndian.Uint64(data[2:10])
	// data
	connData := &ConnData{}
	err := json.Unmarshal(data[10:length], connData)
	if err != nil {
		return 0, err
	}
	connPkt.ConnData = connData
	return uint32(length), nil
}

func (connPkt *ConnPacket) DecodeFromReader(reader io.Reader) error {
	flags := make([]byte, 2)
	_, err := io.ReadFull(reader, flags)
	if err != nil {
		return err
	}
	// conn flags
	connPkt.Retain = (flags[0] & 0x01) != 0
	connPkt.Clear = (flags[0] & 0x02) != 0
	connPkt.packetIDAcquire = (flags[0] & 0x04) != 0
	connPkt.clientIDAcquire = (flags[0] & 0x08) != 0
	heartbeat := float64(flags[0] & 0x30 >> 4)
	connPkt.Heartbeat = Heartbeat(5 * math.Pow(4, heartbeat))
	// client id
	clientID := make([]byte, 8)
	_, err = io.ReadFull(reader, clientID)
	if err != nil {
		return err
	}
	connPkt.ClientID = binary.BigEndian.Uint64(clientID)
	// data
	data := make([]byte, int(connPkt.PacketLen-10))
	_, err = io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	connData := &ConnData{}
	err = json.Unmarshal(data, connData)
	if err != nil {
		return err
	}
	connPkt.ConnData = connData
	return nil
}

type ConnAckPacket struct {
	*PacketHeader
	RetCode  RetCode
	ClientID uint64
	ConnData *ConnData
}

// TODO 约束，包id由双方保障单调递增可信
type ConnData struct {
	Meta  []byte `json:"meta,omitempty"`
	Error string `json:"error,omitempty"`
}

func (connAckPkt *ConnAckPacket) Encode() ([]byte, error) {
	hdr, err := connAckPkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(connAckPkt.ConnData)
	if err != nil {
		return nil, err
	}
	length := len(data) + 9
	pkt := make([]byte, length)
	// ret code
	pkt[0] = byte(connAckPkt.RetCode)
	// client id
	binary.BigEndian.PutUint64(pkt[1:9], connAckPkt.ClientID)
	// data
	copy(pkt[9:length], data)

	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	return append(hdr, pkt...), nil
}

func (connAckPkt *ConnAckPacket) Decode(data []byte) (uint32, error) {
	length := int(connAckPkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	// ret code
	connAckPkt.RetCode = RetCode(data[0])
	// client id
	connAckPkt.ClientID = binary.BigEndian.Uint64(data[1:9])
	// data
	connData := &ConnData{}
	err := json.Unmarshal(data[9:length], connData)
	if err != nil {
		return 0, err
	}
	connAckPkt.ConnData = connData
	return uint32(length), nil
}

func (connAckPkt *ConnAckPacket) DecodeFromReader(reader io.Reader) error {
	length := int(connAckPkt.PacketLen)
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	// ret code
	connAckPkt.RetCode = RetCode(data[0])
	// client id
	connAckPkt.ClientID = binary.BigEndian.Uint64(data[1:9])
	// data
	connData := &ConnData{}
	err = json.Unmarshal(data[9:length], connData)
	if err != nil {
		return err
	}
	connAckPkt.ConnData = connData
	return nil
}

type DisConnPacket struct {
	*PacketHeader
}

func (disConnPkt *DisConnPacket) Encode() ([]byte, error) {
	return disConnPkt.PacketHeader.Encode()
}

func (disConnPkt *DisConnPacket) Decode(data []byte) (uint32, error) {
	return 0, nil
}

func (disConnPkt *DisConnPacket) DecodeFromReader(reader io.Reader) error {
	return nil
}

type DisConnAckPacket struct {
	*PacketHeader
	RetCode  RetCode
	ConnData *ConnData
}

func (disConnAckPkt *DisConnAckPacket) Encode() ([]byte, error) {
	hdr, err := disConnAckPkt.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(disConnAckPkt.ConnData)
	if err != nil {
		return nil, err
	}
	length := len(data) + 1
	pkt := make([]byte, length)
	// ret code
	pkt[0] = byte(disConnAckPkt.RetCode)
	// client id
	copy(pkt[1:length], data)

	// set pkt length
	binary.BigEndian.PutUint32(hdr[10:14], uint32(length))
	hdr = append(hdr, pkt...)
	return hdr, nil
}

func (disConnAckPkt *DisConnAckPacket) Decode(data []byte) (uint32, error) {
	length := int(disConnAckPkt.PacketLen)
	if len(data) < length {
		return 0, ErrIncompletePacket
	}
	// ret code
	disConnAckPkt.RetCode = RetCode(data[0])
	// data
	connData := &ConnData{}
	err := json.Unmarshal(data[1:length], connData)
	if err != nil {
		return 0, err
	}
	disConnAckPkt.ConnData = connData
	return uint32(length), nil
}

func (disConnAckPkt *DisConnAckPacket) DecodeFromReader(reader io.Reader) error {
	length := int(disConnAckPkt.PacketLen)
	data := make([]byte, length)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	// ret code
	disConnAckPkt.RetCode = RetCode(data[0])
	// data
	connData := &ConnData{}
	err = json.Unmarshal(data[1:length], connData)
	if err != nil {
		return err
	}
	disConnAckPkt.ConnData = connData
	return nil
}

type HeartbeatPacket struct {
	*PacketHeader
}

func (hbPkt *HeartbeatPacket) Encode() ([]byte, error) {
	return hbPkt.PacketHeader.Encode()
}

func (hbPkt *HeartbeatPacket) Decode(data []byte) (uint32, error) {
	return 0, nil
}

func (hbPkt *HeartbeatPacket) DecodeFromReader(reader io.Reader) error {
	return nil
}

type HeartbeatAckPacket struct {
	*PacketHeader
}

func (hbAckPkt *HeartbeatAckPacket) Encode() ([]byte, error) {
	return hbAckPkt.PacketHeader.Encode()
}

func (hbAckPkt *HeartbeatAckPacket) Decode(data []byte) (uint32, error) {
	return 0, nil
}

func (hbAckPkt *HeartbeatAckPacket) DecodeFromReader(reader io.Reader) error {
	return nil
}
