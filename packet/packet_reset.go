package packet

import (
	"encoding/binary"
	"io"
)

const StreamResetV1 uint32 = 1

// StreamResetPacket terminates a dialogue without a drain or acknowledgement.
// It is legal only after independent stream_reset capability negotiation.
type StreamResetPacket struct {
	*PacketHeader
	DialogueID  uint64
	NegotiateID uint64 // recipient-local opening ID; zero once unnecessary
}

func NewStreamResetPacket(id uint64) *StreamResetPacket {
	return &StreamResetPacket{&PacketHeader{Version: V01, Typ: TypeStreamResetPacket, PacketLen: 16}, id, 0}
}
func (p *StreamResetPacket) SessionID() uint64      { return p.DialogueID }
func (p *StreamResetPacket) SetSessionID(id uint64) { p.DialogueID = id }
func (p *StreamResetPacket) Encode() ([]byte, error) {
	h, e := p.PacketHeader.Encode()
	if e != nil {
		return nil, e
	}
	binary.BigEndian.PutUint32(h[10:], 16)
	var b [16]byte
	binary.BigEndian.PutUint64(b[:], p.DialogueID)
	binary.BigEndian.PutUint64(b[8:], p.NegotiateID)
	return append(h, b[:]...), nil
}
func (p *StreamResetPacket) Decode(b []byte) (uint32, error) {
	if p.PacketLen != 16 {
		return 0, ErrIllegalPacket
	}
	if len(b) < 16 {
		return 0, ErrIncompletePacket
	}
	p.DialogueID = binary.BigEndian.Uint64(b)
	p.NegotiateID = binary.BigEndian.Uint64(b[8:])
	return 16, nil
}
func (p *StreamResetPacket) DecodeFromReader(r io.Reader) error {
	if p.PacketLen != 16 {
		return ErrIllegalPacket
	}
	var b [16]byte
	if _, e := io.ReadFull(r, b[:]); e != nil {
		return e
	}
	_, e := p.Decode(b[:])
	return e
}
