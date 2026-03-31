package security

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/singchia/geminio"
	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/server"
	"github.com/singchia/geminio/test"
)

// ==================== Input Validation Tests ====================

func TestLargePayload(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	sEnd.Register(context.TODO(), "echo", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData(req.Data())
	})

	// Test with various large payload sizes
	sizes := []int{
		1024 * 1024,       // 1MB
		5 * 1024 * 1024,   // 5MB
		10 * 1024 * 1024,  // 10MB (default limit)
	}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			payload := make([]byte, size)
			for i := range payload {
				payload[i] = byte(i % 256)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			resp, err := cEnd.Call(ctx, "echo", cEnd.NewRequest(payload))
			if err != nil {
				t.Logf("call with %d bytes: %v", size, err)
				return
			}

			if !bytes.Equal(resp.Data(), payload) {
				t.Errorf("response data mismatch for size %d", size)
			}
		})
	}
}

func TestEmptyPayload(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	go func() {
		for {
			msg, err := sEnd.Receive(context.TODO())
			if err != nil {
				return
			}
			msg.Done()
		}
	}()

	// Empty message
	msg := cEnd.NewMessage([]byte{})
	if err := cEnd.Publish(context.TODO(), msg); err != nil {
		t.Errorf("publish empty message failed: %v", err)
	}

	// Empty RPC
	sEnd.Register(context.TODO(), "echo", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData(req.Data())
	})

	resp, err := cEnd.Call(context.TODO(), "echo", cEnd.NewRequest([]byte{}))
	if err != nil {
		t.Errorf("call with empty payload failed: %v", err)
	}
	if len(resp.Data()) != 0 {
		t.Errorf("expected empty response, got %d bytes", len(resp.Data()))
	}
}

func TestNilData(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	go func() {
		for {
			msg, err := sEnd.Receive(context.TODO())
			if err != nil {
				return
			}
			msg.Done()
		}
	}()

	// Nil message
	msg := cEnd.NewMessage(nil)
	if err := cEnd.Publish(context.TODO(), msg); err != nil {
		t.Errorf("publish nil message failed: %v", err)
	}
}

func TestSpecialCharacters(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	sEnd.Register(context.TODO(), "echo", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData(req.Data())
	})

	specialData := []byte{
		0x00, 0x01, 0xFF, 0xFE,
		'\n', '\r', '\t', ' ',
		'<', '>', '&', '"', '\'', '/',
		'{', '}', '[', ']',
	}

	resp, err := cEnd.Call(context.TODO(), "echo", cEnd.NewRequest(specialData))
	if err != nil {
		t.Fatalf("call with special characters failed: %v", err)
	}

	if !bytes.Equal(resp.Data(), specialData) {
		t.Errorf("special characters data mismatch")
	}
}

// ==================== Boundary Tests ====================

func TestBoundaryMessageID(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	// Send many messages to test ID rollover
	count := 10000
	received := make(chan int, 1)

	go func() {
		c := 0
		for {
			msg, err := sEnd.Receive(context.TODO())
			if err != nil {
				return
			}
			msg.Done()
			c++
			if c == count {
				received <- c
				return
			}
		}
	}()

	for i := 0; i < count; i++ {
		msg := cEnd.NewMessage([]byte(fmt.Sprintf("msg-%d", i)))
		if err := cEnd.Publish(context.TODO(), msg); err != nil {
			t.Fatalf("publish %d failed: %v", i, err)
		}
	}

	select {
	case <-received:
		// Success
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for messages")
	}
}

func TestBoundaryStreamCount(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	// Accept streams on server side
	go func() {
		for {
			_, err := sEnd.AcceptStream()
			if err != nil {
				return
			}
		}
	}()

	// Test opening many streams
	numStreams := 1000
	streams := make([]geminio.Stream, 0, numStreams)

	for i := 0; i < numStreams; i++ {
		s, err := cEnd.OpenStream()
		if err != nil {
			t.Logf("opened %d streams before error: %v", i, err)
			break
		}
		streams = append(streams, s)
	}

	t.Logf("successfully opened %d streams", len(streams))

	// Cleanup
	for _, s := range streams {
		s.Close()
	}
}

func TestBoundaryConnectionCount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	network := "tcp"
	address := "127.0.0.1:0"

	ln, err := server.Listen(network, address)
	if err != nil {
		t.Fatalf("server listen failed: %v", err)
	}
	defer ln.Close()

	// Accept connections
	go func() {
		for {
			end, err := ln.AcceptEnd()
			if err != nil {
				return
			}
			go func(e geminio.End) {
				time.Sleep(100 * time.Millisecond)
				e.Close()
			}(end)
		}
	}()

	// Create many connections
	numConns := 100
	var wg sync.WaitGroup

	for i := 0; i < numConns; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cEnd, err := client.NewEnd(network, ln.Addr().String())
			if err != nil {
				t.Logf("connection %d failed: %v", idx, err)
				return
			}
			time.Sleep(50 * time.Millisecond)
			cEnd.Close()
		}(i)
	}

	wg.Wait()
}

// ==================== DoS Protection Tests ====================

func TestDoSRapidConnections(t *testing.T) {
	network := "tcp"
	address := "127.0.0.1:0"

	ln, err := server.Listen(network, address)
	if err != nil {
		t.Fatalf("server listen failed: %v", err)
	}
	defer ln.Close()

	// Rapid connect/disconnect
	numAttempts := 100
	var wg sync.WaitGroup

	for i := 0; i < numAttempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.Dial(network, ln.Addr().String())
			if err != nil {
				return
			}
			conn.Close()
		}()
	}

	wg.Wait()
}

func TestDoSRapidMessages(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	// Server side slow consumer - receives but never acks, with delay to simulate back-pressure
	go func() {
		for {
			_, err := sEnd.Receive(context.TODO())
			if err != nil {
				return
			}
			// Slow consumer: delay before next receive
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// Rapid fire messages
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < 10000; i++ {
		msg := cEnd.NewMessage(make([]byte, 1024))
		if err := cEnd.Publish(ctx, msg); err != nil {
			break // back-pressure or timeout, expected
		}
	}
}

func TestDoSMemoryExhaustion(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	go func() {
		for {
			msg, err := sEnd.Receive(context.TODO())
			if err != nil {
				return
			}
			msg.Done()
		}
	}()

	initialMem := runtime.MemStats{}
	runtime.ReadMemStats(&initialMem)

	// Try to exhaust memory with large messages
	largePayload := make([]byte, 10*1024*1024) // 10MB

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 100; i++ {
		msg := cEnd.NewMessage(largePayload)
		err := cEnd.Publish(ctx, msg)
		if err != nil {
			t.Logf("publish %d failed (possibly due to backpressure): %v", i, err)
			break
		}
	}

	runtime.GC()
	finalMem := runtime.MemStats{}
	runtime.ReadMemStats(&finalMem)

	t.Logf("Memory delta: %d MB", (finalMem.Alloc-initialMem.Alloc)/(1024*1024))
}

// ==================== Fuzzing Tests ====================

func TestFuzzMessageContent(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	go func() {
		for {
			msg, err := sEnd.Receive(context.TODO())
			if err != nil {
				return
			}
			msg.Done()
		}
	}()

	// Fuzz with random bytes
	testData := [][]byte{
		bytes.Repeat([]byte{0xFF}, 1024),
		bytes.Repeat([]byte{0x00}, 1024),
		[]byte("NULL\x00BYTE"),
		[]byte("\\x00\\x01\\x02"),
		[]byte("日本語テスト"),
		[]byte("🚀🎉💻🔒"),
	}

	for _, data := range testData {
		msg := cEnd.NewMessage(data)
		if err := cEnd.Publish(context.TODO(), msg); err != nil {
			t.Errorf("publish fuzz data failed: %v", err)
		}
	}
}

func TestFuzzRPCMethod(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	// Register normal handler
	sEnd.Register(context.TODO(), "normal", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData([]byte("ok"))
	})

	// Call with various method names
	methods := []string{
		"",
		"   ",
		"very-long-method-name-" + strings.Repeat("x", 1000),
		"method/with/slashes",
		"method.with.dots",
		"method-with-unicode-日本語",
		"METHOD_WITH_UPPERCASE",
		"method\x00null",
	}

	for _, method := range methods {
		_, err := cEnd.Call(context.TODO(), method, cEnd.NewRequest([]byte("test")))
		// Should either succeed or fail gracefully
		if err != nil {
			t.Logf("method %q: %v", method, err)
		}
	}
}

// ==================== Permission Tests ====================

func TestUnauthorizedRPC(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	// Call unregistered method
	_, err = cEnd.Call(context.TODO(), "unregistered-method", cEnd.NewRequest([]byte("test")))
	if err == nil {
		t.Error("expected error for unregistered method")
	}
}

// ==================== Resource Exhaustion Tests ====================

func TestResourceExhaustionStreams(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	// Accept streams in background
	go func() {
		for {
			_, err := sEnd.AcceptStream()
			if err != nil {
				return
			}
		}
	}()

	// Open streams without closing
	for i := 0; i < 10000; i++ {
		_, err := cEnd.OpenStream()
		if err != nil {
			t.Logf("opened %d streams before error: %v", i, err)
			break
		}
	}
}

func TestResourceExhaustionGoroutines(t *testing.T) {
	initialGoroutines := runtime.NumGoroutine()

	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}

	go func() {
		for {
			msg, err := sEnd.Receive(context.TODO())
			if err != nil {
				return
			}
			msg.Done()
		}
	}()

	// Create many concurrent operations
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				msg := cEnd.NewMessage([]byte("test"))
				cEnd.Publish(context.TODO(), msg)
			}
		}()
	}
	wg.Wait()

	sEnd.Close()
	cEnd.Close()

	// Give time for cleanup
	time.Sleep(100 * time.Millisecond)
	runtime.GC()

	finalGoroutines := runtime.NumGoroutine()
	if finalGoroutines > initialGoroutines+20 {
		t.Errorf("possible goroutine leak: started %d, ended %d", initialGoroutines, finalGoroutines)
	}
}

// ==================== Injection Tests ====================

func TestSQLInjection(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	sEnd.Register(context.TODO(), "echo", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData(req.Data())
	})

	sqlInjections := []string{
		"'; DROP TABLE users; --",
		"1 OR 1=1",
		"1; DELETE FROM users",
		"' UNION SELECT * FROM passwords --",
	}

	for _, injection := range sqlInjections {
		resp, err := cEnd.Call(context.TODO(), "echo", cEnd.NewRequest([]byte(injection)))
		if err != nil {
			t.Errorf("SQL injection test failed: %v", err)
			continue
		}
		if string(resp.Data()) != injection {
			t.Errorf("data mismatch for SQL injection payload")
		}
	}
}

func TestCommandInjection(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	go func() {
		for {
			msg, err := sEnd.Receive(context.TODO())
			if err != nil {
				return
			}
			msg.Done()
		}
	}()

	cmdInjections := []string{
		"; cat /etc/passwd",
		"| rm -rf /",
		"`whoami`",
		"$(id)",
		"\n/bin/sh",
	}

	for _, injection := range cmdInjections {
		msg := cEnd.NewMessage([]byte(injection))
		if err := cEnd.Publish(context.TODO(), msg); err != nil {
			t.Errorf("command injection publish failed: %v", err)
		}
	}
}

func TestPathTraversal(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	go func() {
		for {
			msg, err := sEnd.Receive(context.TODO())
			if err != nil {
				return
			}
			msg.Done()
		}
	}()

	pathTraversals := []string{
		"../../../etc/passwd",
		"..\\..\\..\\windows\\system32\\config\\sam",
		"/etc/shadow",
		"file:///etc/passwd",
	}

	for _, path := range pathTraversals {
		msg := cEnd.NewMessage([]byte(path))
		if err := cEnd.Publish(context.TODO(), msg); err != nil {
			t.Errorf("path traversal publish failed: %v", err)
		}
	}
}

// ==================== Race Condition Tests ====================

func TestRaceCloseAndSend(t *testing.T) {
	for i := 0; i < 10; i++ {
		sEnd, cEnd, err := test.GetEndPair()
		if err != nil {
			t.Fatalf("get end pair failed: %v", err)
		}

		var wg sync.WaitGroup
		wg.Add(2)

		// Concurrent close and send
		go func() {
			defer wg.Done()
			time.Sleep(time.Millisecond)
			sEnd.Close()
		}()

		go func() {
			defer wg.Done()
			time.Sleep(time.Millisecond)
			cEnd.Publish(context.TODO(), cEnd.NewMessage([]byte("test")))
			cEnd.Close()
		}()

		wg.Wait()
	}
}

func TestRaceMultipleCloses(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			sEnd.Close()
		}()
		go func() {
			defer wg.Done()
			cEnd.Close()
		}()
	}
	wg.Wait()
}

// ==================== Timing Attack Tests ====================

func TestTimingSideChannel(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	sEnd.Register(context.TODO(), "exists", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData([]byte("found"))
	})

	// Measure timing for existing and non-existing methods
	existingTimes := []time.Duration{}
	nonExistingTimes := []time.Duration{}

	for i := 0; i < 10; i++ {
		start := time.Now()
		cEnd.Call(context.TODO(), "exists", cEnd.NewRequest([]byte("test")))
		existingTimes = append(existingTimes, time.Since(start))

		start = time.Now()
		cEnd.Call(context.TODO(), "notexists", cEnd.NewRequest([]byte("test")))
		nonExistingTimes = append(nonExistingTimes, time.Since(start))
	}

	var existingAvg, nonExistingAvg time.Duration
	for _, d := range existingTimes {
		existingAvg += d
	}
	for _, d := range nonExistingTimes {
		nonExistingAvg += d
	}
	existingAvg /= time.Duration(len(existingTimes))
	nonExistingAvg /= time.Duration(len(nonExistingTimes))

	diff := existingAvg - nonExistingAvg
	if diff < 0 {
		diff = -diff
	}

	t.Logf("Existing method avg: %v", existingAvg)
	t.Logf("Non-existing method avg: %v", nonExistingAvg)
	t.Logf("Difference: %v", diff)

	// If difference is significant, might indicate timing side channel
	if diff > 10*time.Millisecond {
		t.Log("Warning: possible timing side channel detected")
	}
}
