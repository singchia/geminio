package packet

import (
	"encoding/binary"
	"io"
	"strconv"
)

const (
	PacketIDNull uint64 = 0
)

func PacketIDHex(id uint64) string {
	return strconv.FormatUint(id, 16)
}

const (
	ClientIDNull uint64 = 0
)

func ClientIDToHex(id uint64) string {
	return strconv.FormatUint(id, 16)
}

const (
	// 0 means sessionID required
	SessionIDNull uint64 = 0
	// 1 means default session
	SessionID1 uint64 = 1
)

func SessionIDHex(id uint64) string {
	return strconv.FormatUint(id, 16)
}

type Packet interface {
	Decode(data []byte) (uint32, error)
	DecodeFromReader(reader io.Reader) error
	Encode() ([]byte, error)

	Consistency() Cnss
	ID() uint64
	Type() Type
}

type Version byte

const (
	V01 = 0x01
)

type Type byte

func (typ Type) String() string {
	switch typ {
	case TypeConnPacket:
		return "conn packet"
	case TypeConnAckPacket:
		return "conn ack packet"
	case TypeDisConnPacket:
		return "dis conn packet"
	case TypeDisConnAckPacket:
		return "dis conn ack packet"
	case TypeHeartbeatPacket:
		return "heartbeat packet"
	case TypeHeartbeatAckPacket:
		return "heartbeat ack packet"
	case TypeSessionPacket:
		return "session packet"
	case TypeSessionAckPacket:
		return "session ack packet"
	case TypeDismissPacket:
		return "dismiss packet"
	case TypeDismissAckPacket:
		return "dismiss ack packet"
	case TypeMessagePacket:
		return "message packet"
	case TypeMessageAckPacket:
		return "message ack packet"
	case TypeStreamPacket:
		return "stream packet"
	case TypeRequestPacket:
		return "request packet"
	case TypeResponsePacket:
		return "response packet"
	case TypeRegisterPacket:
		return "register packet"
	case TypeRegisterAckPacket:
		return "register ack packet"
	}
	return "unknown packet"
}

const (
	TypeConnPacket         Type = 0x01
	TypeConnAckPacket      Type = 0x02
	TypeDisConnPacket      Type = 0x11
	TypeDisConnAckPacket   Type = 0x12
	TypeHeartbeatPacket    Type = 0x21
	TypeHeartbeatAckPacket Type = 0x22
	TypeSessionPacket      Type = 0x31
	TypeSessionAckPacket   Type = 0x32
	TypeDismissPacket      Type = 0x41
	TypeDismissAckPacket   Type = 0x42
	TypeMessagePacket      Type = 0x51
	TypeMessageAckPacket   Type = 0x52
	TypeStreamPacket       Type = 0x61
	TypeRequestPacket      Type = 0x71
	TypeResponsePacket     Type = 0x72
	TypeRegisterPacket     Type = 0x81
	TypeRegisterAckPacket  Type = 0x82
)

type Cnss byte

const (
	CnssAtMostOnce      Cnss = 0x00
	CnssAtLeastOnce     Cnss = 0x01
	CnssAtEffectiveOnce Cnss = 0x02
)

type RetCode byte

const (
	RetCodeOK  RetCode = 0x00
	RetCodeERR RetCode = 0x01
)

type PacketHeader struct {
	Version   Version
	Typ       Type
	PacketID  uint64
	PacketLen uint32
	Cnss      Cnss
}

func (pktHdr *PacketHeader) Consistency() Cnss {
	return pktHdr.Cnss
}

func (pktHdr *PacketHeader) ID() uint64 {
	return pktHdr.PacketID
}

func (pktHdr *PacketHeader) Encode() ([]byte, error) {
	hdr := make([]byte, 14)
	hdr[0] = byte(pktHdr.Version)
	hdr[1] = byte(pktHdr.Typ)
	binary.BigEndian.PutUint64(hdr[2:10], pktHdr.PacketID)
	binary.BigEndian.PutUint32(hdr[10:14], pktHdr.PacketLen)
	return hdr, nil
}

func (pktHdr *PacketHeader) Type() Type {
	return pktHdr.Typ
}

func (pktHdr *PacketHeader) Decode(data []byte) (uint32, error) {
	if len(data) < 14 {
		return 0, ErrIncompletePacket
	}
	pktHdr.Version = Version(data[0])
	pktHdr.Typ = Type(data[1])
	pktHdr.PacketID = binary.BigEndian.Uint64(data[2:10])
	pktHdr.PacketLen = binary.BigEndian.Uint32(data[10:14])
	return 14, nil
}

func (pktHdr *PacketHeader) DecodeFromReader(reader io.Reader) error {
	data := make([]byte, 14)
	_, err := io.ReadFull(reader, data)
	if err != nil {
		return err
	}
	pktHdr.Version = Version(data[0])
	pktHdr.Typ = Type(data[1])
	pktHdr.PacketID = binary.BigEndian.Uint64(data[2:10])
	pktHdr.PacketLen = binary.BigEndian.Uint32(data[10:14])
	return nil
}
