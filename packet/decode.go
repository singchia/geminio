package packet

import (
	"errors"
	"io"
)

var (
	ErrUnsupportedPacket = errors.New("unsupported packet")
	ErrIncompletePacket  = errors.New("incomplete packet")
	ErrExpectingData     = errors.New("expecting data")
	ErrInvalidArguments  = errors.New("invalid arguments")
	ErrIllegalPacket     = errors.New("illegal packet")
)

func Decode(data []byte) (Packet, uint32, error) {
	pktHdr := &PacketHeader{}
	n, err := pktHdr.Decode(data)
	if err != nil {
		return nil, 0, err
	}

	if uint32(len(data)-14) < pktHdr.PacketLen {
		return pktHdr, 14, ErrExpectingData
	}

	switch pktHdr.Typ {
	case TypeConnPacket:
		pkt := &ConnPacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeConnAckPacket:
		pkt := &ConnAckPacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeDisConnPacket:
		pkt := &DisConnPacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeDisConnAckPacket:
		pkt := &DisConnAckPacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeHeartbeatPacket:
		pkt := &HeartbeatPacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeHeartbeatAckPacket:
		pkt := &HeartbeatAckPacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeSessionPacket:
		pkt := &SessionPacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeSessionAckPacket:
		pkt := &SessionAckPacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeDismissPacket:
		pkt := &DismissPacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeDismissAckPacket:
		pkt := &DismissAckPacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeMessagePacket:
		pkt := &MessagePacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeMessageAckPacket:
		pkt := &MessageAckPacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeStreamPacket:
		pkt := &StreamPacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeRegisterPacket:
		pkt := &RegisterPacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeRegisterAckPacket:
		pkt := &RegisterAckPacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeRequestPacket:
		pkt := &RequestPacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	case TypeResponsePacket:
		pkt := &ResponsePacket{}
		pkt.PacketHeader = pktHdr
		n, err = pkt.Decode(data[14:])
		return pkt, n, err

	default:
		return nil, 10, ErrUnsupportedPacket
	}
}

func DecodeFromReader(reader io.Reader) (Packet, error) {
	if reader == nil {
		return nil, ErrInvalidArguments
	}
	pktHdr := &PacketHeader{}
	err := pktHdr.DecodeFromReader(reader)
	if err != nil {
		return nil, err
	}
	//log.Tracef("DecodeFromReader | incoming underlay %s", pktHdr.Typ.String())

	switch pktHdr.Typ {
	case TypeConnPacket:
		pkt := &ConnPacket{}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeConnAckPacket:
		pkt := &ConnAckPacket{}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeDisConnPacket:
		pkt := &DisConnPacket{}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeDisConnAckPacket:
		pkt := &DisConnAckPacket{}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeHeartbeatPacket:
		pkt := &HeartbeatPacket{}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeHeartbeatAckPacket:
		pkt := &HeartbeatAckPacket{}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeSessionPacket:
		pkt := &SessionPacket{}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeSessionAckPacket:
		pkt := &SessionAckPacket{}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeDismissPacket:
		pkt := &DismissPacket{}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeDismissAckPacket:
		pkt := &DismissAckPacket{}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeMessagePacket:
		pkt := &MessagePacket{}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeMessageAckPacket:
		pkt := &MessageAckPacket{}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeStreamPacket:
		pkt := &StreamPacket{}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeRegisterPacket:
		pkt := &RegisterPacket{}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeRegisterAckPacket:
		pkt := &RegisterAckPacket{}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeRequestPacket:
		pkt := &RequestPacket{
			&MessagePacket{},
		}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	case TypeResponsePacket:
		pkt := &ResponsePacket{
			&MessageAckPacket{},
		}
		pkt.PacketHeader = pktHdr
		err = pkt.DecodeFromReader(reader)
		return pkt, err

	default:
		return nil, ErrUnsupportedPacket
	}
}

func Encode(pkt Packet) ([]byte, error) {
	return pkt.Encode()
}

func EncodeToWriter(pkt Packet, writer io.Writer) error {
	data, err := pkt.Encode()
	if err != nil {
		return err
	}
	length := len(data)
	pos := 0
	for {
		m, err := writer.Write(data[pos:length])
		if err != nil {
			return err
		}
		pos += m
		if pos == length {
			break
		}
	}
	return nil
}
