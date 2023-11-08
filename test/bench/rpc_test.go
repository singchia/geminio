package bench

import (
	"context"
	"testing"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/test"
)

func BenchmarkRPC(b *testing.B) {
	log.SetLevel(log.LevelError)
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		b.Fatal(err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	buf1 := make([]byte, 256)
	buf2 := make([]byte, 4096)

	echoServer := func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData(buf2)
	}
	sEnd.Register(context.TODO(), "hello", echoServer)

	b.SetBytes(128 * 1024)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := cEnd.Call(context.TODO(), "hello", cEnd.NewRequest(buf1))
		if err != nil {
			b.Fatal(err)
		}
	}
}
