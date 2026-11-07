package integration

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/test/harness"
)

func TestOpenStreamContextLifetime(t *testing.T) {
	harness.LogSilence(t)
	s, c := harness.NewEndPair(t)
	for _, pair := range [][2]geminio.End{{c, s}, {s, c}} {
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := geminio.OpenStreamContext(canceled, pair[0]); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		st, err := geminio.OpenStreamContext(ctx, pair[0])
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		peer, err := pair[1].AcceptStream()
		if err != nil {
			t.Fatal(err)
		}
		st.SetDeadline(time.Now().Add(time.Second))
		peer.SetDeadline(time.Now().Add(time.Second))
		if _, err := peer.Write([]byte("alive")); err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 5)
		if _, err := io.ReadFull(st, b); err != nil {
			t.Fatal(err)
		}
		if string(b) != "alive" {
			t.Fatal(string(b))
		}
		st.ForceClose()
		peer.ForceClose()
	}
}
