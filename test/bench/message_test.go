package bench

import (
	"context"
	"testing"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/test"
)

func BenchmarkMessage(b *testing.B) {
	log.SetLevel(log.LevelError)
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		b.Fatal(err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			resp, err := sEnd.Receive(context.TODO())
			if err != nil {
				return
			}
			resp.Done()
		}
	}()

	b.SetBytes(128 * 1024)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		err = cEnd.Publish(context.TODO(), cEnd.NewMessage([]byte("hello")))
		if err != nil {
			b.Fatal(err)
		}
	}
	sEnd.Close()
	cEnd.Close()
	<-done
}
