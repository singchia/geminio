package packet

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestStreamResetCodec(t *testing.T) {
	p := NewStreamResetPacket(1<<62 + 7)
	p.NegotiateID = 12345
	b, e := p.Encode()
	if e != nil {
		t.Fatal(e)
	}
	decoded, _, e := Decode(b)
	if e != nil {
		t.Fatal(e)
	}
	if decoded.(*StreamResetPacket).DialogueID != p.DialogueID || decoded.(*StreamResetPacket).NegotiateID != p.NegotiateID {
		t.Fatal("id lost")
	}
	decoded, e = DecodeFromReader(bytes.NewReader(b))
	if e != nil {
		t.Fatal(e)
	}
	if decoded.(*StreamResetPacket).DialogueID != p.DialogueID || decoded.(*StreamResetPacket).NegotiateID != p.NegotiateID {
		t.Fatal("reader id lost")
	}
	for _, size := range []uint32{0, 7, 9, 1000000} {
		bad := append([]byte(nil), b...)
		binary.BigEndian.PutUint32(bad[10:], size)
		if _, e = DecodeFromReader(bytes.NewReader(bad)); e == nil {
			t.Fatalf("accepted length %d", size)
		}
	}
}
