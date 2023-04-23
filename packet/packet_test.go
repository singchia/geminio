package packet

import (
	"errors"
	"testing"
)

func TestPacketHeader(t *testing.T) {
	hdr := &PacketHeader{
		Version:   V01,
		Typ:       TypeConnPacket,
		PacketID:  1,
		PacketLen: 14,
	}
	bytes, err := hdr.Encode()
	if err != nil {
		t.Error(err)
		return
	}

	newHdr := &PacketHeader{}
	_, err = newHdr.Decode(bytes)
	if err != nil {
		t.Error(err)
		return
	}
	if hdr.Typ != newHdr.Typ || hdr.Version != newHdr.Version ||
		hdr.PacketID != hdr.PacketID || hdr.PacketLen != hdr.PacketLen {
		t.Error(errors.New("unmatch encode and decode"))
		return
	}
}
