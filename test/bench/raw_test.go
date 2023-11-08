package bench

import (
	"io"
	"sync"
	"testing"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio/test"
)

func BenchmarkEnd(b *testing.B) {
	log.SetLevel(log.LevelError)
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		b.Fatal(err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	bench(b, sEnd, cEnd)
	bench(b, cEnd, sEnd)
}

func BenchmarkStream(b *testing.B) {
	log.SetLevel(log.LevelError)
	ss, cs, err := test.GetEndStream()
	if err != nil {
		b.Fatal(err)
	}
	defer ss.Close()
	defer cs.Close()

	bench(b, ss, cs)
	bench(b, cs, ss)
}

func bench(b *testing.B, rd io.Reader, wr io.Writer) {
	buf := make([]byte, 128*1024)
	buf2 := make([]byte, 128*1024)
	b.SetBytes(128 * 1024)
	b.ResetTimer()
	b.ReportAllocs()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		count := 0
		for {
			n, _ := rd.Read(buf2)
			count += n
			if count == 128*1024*b.N {
				return
			}
		}
	}()
	for i := 0; i < b.N; i++ {
		wr.Write(buf)
	}
	wg.Wait()
}
