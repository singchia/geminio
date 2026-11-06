package packet

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestWindowUpdateRoundTrip(t *testing.T) {
	p := NewWindowUpdatePacket(123, 65536, 4)
	wire, err := p.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, reader := range []bool{false, true} {
		var got Packet
		if reader {
			got, err = DecodeFromReader(bytes.NewReader(wire))
		} else {
			got, _, err = Decode(wire)
		}
		if err != nil {
			t.Fatal(err)
		}
		q := got.(*WindowUpdatePacket)
		if q.SessionID() != 123 || q.Credit != 65536 || q.Packets != 4 {
			t.Fatalf("corrupt update: %+v", q)
		}
	}
}
func TestWindowUpdateRejectsMalformed(t *testing.T) {
	good, _ := NewWindowUpdatePacket(1, 100, 1).Encode()
	for _, size := range []uint32{0, 15, 17, ^uint32(0)} {
		data := append([]byte(nil), good...)
		binary.BigEndian.PutUint32(data[10:], size)
		if _, _, err := Decode(data); err == nil {
			t.Fatalf("accepted size %d", size)
		}
		if _, err := DecodeFromReader(bytes.NewReader(data)); err == nil {
			t.Fatalf("reader accepted size %d", size)
		}
	}
	for _, offset := range []int{22, 26} {
		data := append([]byte(nil), good...)
		binary.BigEndian.PutUint32(data[offset:], 0)
		if _, _, err := Decode(data); err == nil {
			t.Fatal("accepted zero credit")
		}
		if _, err := DecodeFromReader(bytes.NewReader(data)); err == nil {
			t.Fatal("reader accepted zero credit")
		}
	}
}
