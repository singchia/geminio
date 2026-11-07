package packet

import (
	"encoding/binary"
	"io"
)

// V1 uses both byte and packet credits. Packet credits bound queue occupancy,
// including zero-length writes. The byte window admits every legal V01 packet.
const (
	FlowControlV1     uint32 = 1
	FlowWindowBytes   uint32 = 16 * 1024 * 1024
	FlowWindowPackets uint32 = 64
)

// WindowUpdatePacket returns consumed receive capacity; it never consumes credit.
// Credit is incremental, on the ordered reliable connection, not an absolute limit.
type WindowUpdatePacket struct {
	*PacketHeader
	DialogueID uint64
	Credit     uint32
	Packets    uint32
}

func NewWindowUpdatePacket(sessionID uint64, credit, packets uint32) *WindowUpdatePacket {
	return &WindowUpdatePacket{PacketHeader: &PacketHeader{Version: V01, Typ: TypeWindowUpdatePacket, PacketLen: 16}, DialogueID: sessionID, Credit: credit, Packets: packets}
}
func (p *WindowUpdatePacket) SessionID() uint64      { return p.DialogueID }
func (p *WindowUpdatePacket) SetSessionID(id uint64) { p.DialogueID = id }
func (p *WindowUpdatePacket) Encode() ([]byte, error) {
	if p.Credit == 0 || p.Packets == 0 {
		return nil, ErrIllegalPacket
	}
	hdr, err := p.PacketHeader.Encode()
	if err != nil {
		return nil, err
	}
	data := make([]byte, 16)
	binary.BigEndian.PutUint64(data, p.DialogueID)
	binary.BigEndian.PutUint32(data[8:], p.Credit)
	binary.BigEndian.PutUint32(data[12:], p.Packets)
	binary.BigEndian.PutUint32(hdr[10:], 16)
	return append(hdr, data...), nil
}
func (p *WindowUpdatePacket) Decode(data []byte) (uint32, error) {
	if p.PacketLen != 16 {
		return 0, ErrIllegalPacket
	}
	if len(data) < 16 {
		return 0, ErrIncompletePacket
	}
	p.DialogueID = binary.BigEndian.Uint64(data)
	p.Credit = binary.BigEndian.Uint32(data[8:])
	p.Packets = binary.BigEndian.Uint32(data[12:])
	if p.Credit == 0 || p.Packets == 0 {
		return 0, ErrIllegalPacket
	}
	return 16, nil
}
func (p *WindowUpdatePacket) DecodeFromReader(r io.Reader) error {
	if p.PacketLen != 16 {
		return ErrIllegalPacket
	}
	var data [16]byte
	if _, err := io.ReadFull(r, data[:]); err != nil {
		return err
	}
	_, err := p.Decode(data[:])
	return err
}
