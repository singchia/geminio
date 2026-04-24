package bench

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/gemino"
	"github.com/singchia/gemino/test"
)

// ==================== Message Benchmarks ====================

func BenchmarkMessageThroughput(b *testing.B) {
	defer reportOpsPerSec(b)
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
		if err := cEnd.Publish(context.TODO(), cEnd.NewMessage(make([]byte, 128*1024))); err != nil {
			b.Fatal(err)
		}
	}
	cEnd.Close() // signal EOF so receiver goroutine exits
	<-done
}

func BenchmarkMessageLatency(b *testing.B) {
	defer reportOpsPerSec(b)
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

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		start := time.Now()
		if err := cEnd.Publish(context.TODO(), cEnd.NewMessage([]byte("ping"))); err != nil {
			b.Fatal(err)
		}
		b.SetBytes(int64(time.Since(start)))
	}
	cEnd.Close() // signal EOF so receiver goroutine exits
	<-done
}

func BenchmarkMessageConcurrent(b *testing.B) {
	defer reportOpsPerSec(b)
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
		count := 0
		for {
			resp, err := sEnd.Receive(context.TODO())
			if err != nil {
				return
			}
			resp.Done()
			count++
			if count == b.N {
				return
			}
		}
	}()

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := cEnd.Publish(context.TODO(), cEnd.NewMessage([]byte("hello"))); err != nil {
				b.Fatal(err)
			}
		}
	})
	<-done
}

// ==================== RPC Benchmarks ====================

func BenchmarkRPCLatency(b *testing.B) {
	defer reportOpsPerSec(b)
	log.SetLevel(log.LevelError)
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		b.Fatal(err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	sEnd.Register(context.TODO(), "echo", func(ctx context.Context, req gemino.Request, resp gemino.Response) {
		resp.SetData(req.Data())
	})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := cEnd.Call(context.TODO(), "echo", cEnd.NewRequest([]byte("ping")))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRPCThroughput(b *testing.B) {
	defer reportOpsPerSec(b)
	log.SetLevel(log.LevelError)
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		b.Fatal(err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	payload := make([]byte, 64*1024)
	sEnd.Register(context.TODO(), "echo", func(ctx context.Context, req gemino.Request, resp gemino.Response) {
		resp.SetData(req.Data())
	})

	b.SetBytes(int64(len(payload) * 2))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := cEnd.Call(context.TODO(), "echo", cEnd.NewRequest(payload))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRPCConcurrent(b *testing.B) {
	defer reportOpsPerSec(b)
	log.SetLevel(log.LevelError)
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		b.Fatal(err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	sEnd.Register(context.TODO(), "echo", func(ctx context.Context, req gemino.Request, resp gemino.Response) {
		resp.SetData(req.Data())
	})

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := cEnd.Call(context.TODO(), "echo", cEnd.NewRequest([]byte("hello")))
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkRPCDifferentSizes(b *testing.B) {
	defer reportOpsPerSec(b)
	sizes := []int{64, 256, 1024, 4096, 16384, 65536, 262144}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("size_%d", size), func(b *testing.B) {
			log.SetLevel(log.LevelError)
			sEnd, cEnd, err := test.GetEndPair()
			if err != nil {
				b.Fatal(err)
			}
			defer sEnd.Close()
			defer cEnd.Close()

			payload := make([]byte, size)
			sEnd.Register(context.TODO(), "echo", func(ctx context.Context, req gemino.Request, resp gemino.Response) {
				resp.SetData(req.Data())
			})

			b.SetBytes(int64(size * 2))
			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, err := cEnd.Call(context.TODO(), "echo", cEnd.NewRequest(payload))
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ==================== Stream Benchmarks ====================

func BenchmarkStreamThroughput(b *testing.B) {
	defer reportOpsPerSec(b)
	log.SetLevel(log.LevelError)
	sEnd, cEnd, ss, cs, err := test.GetEndStream()
	if err != nil {
		b.Fatal(err)
	}
	defer sEnd.Close()
	defer cEnd.Close()
	defer ss.Close()
	defer cs.Close()

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
			n, err := ss.Read(buf2)
			count += n
			if count >= 128*1024*b.N {
				return
			}
			if err != nil {
				return
			}
		}
	}()

	for i := 0; i < b.N; i++ {
		cs.Write(buf)
	}
	wg.Wait()
}

func BenchmarkStreamConcurrent(b *testing.B) {
	defer reportOpsPerSec(b)
	log.SetLevel(log.LevelError)
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		b.Fatal(err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	numStreams := 10
	streams := make([]gemino.Stream, numStreams)
	for i := 0; i < numStreams; i++ {
		s, err := cEnd.OpenStream()
		if err != nil {
			b.Fatal(err)
		}
		streams[i] = s
	}

	// Accept all streams and drain them
	go func() {
		for i := 0; i < numStreams; i++ {
			ss, err := sEnd.AcceptStream()
			if err != nil {
				return
			}
			go func(s gemino.Stream) {
				buf := make([]byte, 4096)
				for {
					_, err := s.Read(buf)
					if err != nil {
						return
					}
				}
			}(ss)
		}
	}()
	time.Sleep(100 * time.Millisecond)

	buf := make([]byte, 4096)
	b.SetBytes(int64(len(buf)))
	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		idx := 0
		for pb.Next() {
			streams[idx%numStreams].Write(buf)
			idx++
		}
	})
}

// ==================== End Benchmarks ====================

func BenchmarkEndRawThroughput(b *testing.B) {
	defer reportOpsPerSec(b)
	log.SetLevel(log.LevelError)
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		b.Fatal(err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

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
			n, _ := sEnd.Read(buf2)
			count += n
			if count >= 128*1024*b.N {
				return
			}
		}
	}()

	for i := 0; i < b.N; i++ {
		cEnd.Write(buf)
	}
	wg.Wait()
}

// ==================== Connection Benchmarks ====================

func BenchmarkConnectionCreation(b *testing.B) {
	defer reportOpsPerSec(b)
	log.SetLevel(log.LevelError)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		sEnd, cEnd, err := test.GetEndPair()
		if err != nil {
			b.Fatal(err)
		}
		sEnd.Close()
		cEnd.Close()
	}
}

func BenchmarkStreamCreation(b *testing.B) {
	defer reportOpsPerSec(b)
	log.SetLevel(log.LevelError)
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		b.Fatal(err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	go func() {
		for {
			_, err := sEnd.AcceptStream()
			if err != nil {
				return
			}
		}
	}()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		s, err := cEnd.OpenStream()
		if err != nil {
			b.Fatal(err)
		}
		s.Close()
	}
}

// ==================== Memory Benchmarks ====================

func BenchmarkMemoryAllocation(b *testing.B) {
	defer reportOpsPerSec(b)
	log.SetLevel(log.LevelError)
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		b.Fatal(err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	sEnd.Register(context.TODO(), "echo", func(ctx context.Context, req gemino.Request, resp gemino.Response) {
		resp.SetData(req.Data())
	})

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := cEnd.Call(context.TODO(), "echo", cEnd.NewRequest([]byte("hello")))
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMemoryPressure tests memory behavior under high load
func BenchmarkMemoryPressure(b *testing.B) {
	defer reportOpsPerSec(b)
	log.SetLevel(log.LevelError)
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		b.Fatal(err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	// Server side message processing
	go func() {
		for {
			msg, err := sEnd.Receive(context.TODO())
			if err != nil {
				return
			}
			// Simulate some processing
			time.Sleep(time.Microsecond * 100)
			msg.Done()
		}
	}()

	b.ResetTimer()
	b.ReportAllocs()

	// Client sends messages as fast as possible
	for i := 0; i < b.N; i++ {
		data := make([]byte, 1024)
		cEnd.Publish(context.TODO(), cEnd.NewMessage(data))
	}
}
