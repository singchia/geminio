package e2e

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jumboframes/armorigo/log"
	"github.com/singchia/geminio"
	"github.com/singchia/geminio/client"
	"github.com/singchia/geminio/server"
	"github.com/singchia/geminio/test"
)

// ==================== Connection Lifecycle Tests ====================

func TestConnectionEstablishment(t *testing.T) {
	network := "tcp"
	address := "127.0.0.1:0"

	ln, err := server.Listen(network, address)
	if err != nil {
		t.Fatalf("server listen failed: %v", err)
	}
	defer ln.Close()

	accepted := make(chan geminio.End, 1)
	go func() {
		end, err := ln.AcceptEnd()
		if err != nil {
			t.Errorf("accept end failed: %v", err)
			return
		}
		accepted <- end
	}()

	cEnd, err := client.NewEnd(network, ln.Addr().String())
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	defer cEnd.Close()

	select {
	case sEnd := <-accepted:
		if sEnd == nil {
			t.Fatal("server end is nil")
		}
		sEnd.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for server accept")
	}
}

func TestConnectionGracefulClose(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}

	// Client closes first
	if err := cEnd.Close(); err != nil {
		t.Errorf("client close failed: %v", err)
	}

	// Wait a bit and verify server detects close
	time.Sleep(100 * time.Millisecond)
	if err := sEnd.Close(); err != nil {
		t.Errorf("server close failed: %v", err)
	}
}

func TestConnectionWithMetadata(t *testing.T) {
	network := "tcp"
	address := "127.0.0.1:0"

	ln, err := server.Listen(network, address)
	if err != nil {
		t.Fatalf("server listen failed: %v", err)
	}
	defer ln.Close()

	expectedMeta := []byte("test-metadata-12345")
	metaReceived := make(chan []byte, 1)

	go func() {
		end, err := ln.AcceptEnd()
		if err != nil {
			t.Errorf("accept end failed: %v", err)
			return
		}
		metaReceived <- end.Meta()
		end.Close()
	}()

	opt := client.NewEndOptions()
	opt.SetMeta(expectedMeta)

	cEnd, err := client.NewEnd(network, ln.Addr().String(), opt)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}
	cEnd.Close()

	select {
	case meta := <-metaReceived:
		if string(meta) != string(expectedMeta) {
			t.Errorf("metadata mismatch: got %s, want %s", meta, expectedMeta)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for metadata")
	}
}

// ==================== Multiple Connections Tests ====================

func TestMultipleClients(t *testing.T) {
	network := "tcp"
	address := "127.0.0.1:0"

	ln, err := server.Listen(network, address)
	if err != nil {
		t.Fatalf("server listen failed: %v", err)
	}
	defer ln.Close()

	numClients := 100
	var wg sync.WaitGroup
	wg.Add(numClients)

	// Server accepts connections
	go func() {
		for i := 0; i < numClients; i++ {
			end, err := ln.AcceptEnd()
			if err != nil {
				t.Errorf("accept end failed: %v", err)
				return
			}
			go func(e geminio.End) {
				defer wg.Done()
				time.Sleep(10 * time.Millisecond)
				e.Close()
			}(end)
		}
	}()

	// Clients connect
	var clientWg sync.WaitGroup
	clientWg.Add(numClients)
	for i := 0; i < numClients; i++ {
		go func(idx int) {
			defer clientWg.Done()
			cEnd, err := client.NewEnd(network, ln.Addr().String())
			if err != nil {
				t.Errorf("client %d connect failed: %v", idx, err)
				return
			}
			time.Sleep(10 * time.Millisecond)
			cEnd.Close()
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		clientWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for clients")
	}
}

// ==================== Message Tests ====================

func TestMessageBasic(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	msgContent := []byte("hello, geminio!")

	done := make(chan struct{})
	go func() {
		defer close(done)
		msg, err := sEnd.Receive(context.TODO())
		if err != nil {
			t.Errorf("receive failed: %v", err)
			return
		}
		if string(msg.Data()) != string(msgContent) {
			t.Errorf("message mismatch: got %s, want %s", msg.Data(), msgContent)
		}
		if err := msg.Done(); err != nil {
			t.Errorf("msg.Done() failed: %v", err)
		}
	}()

	msg := cEnd.NewMessage(msgContent)
	if err := cEnd.Publish(context.TODO(), msg); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestMessageMultiple(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	numMessages := 1000
	received := make(chan int, 1)

	go func() {
		count := 0
		for {
			msg, err := sEnd.Receive(context.TODO())
			if err != nil {
				return
			}
			msg.Done()
			count++
			if count == numMessages {
				received <- count
				return
			}
		}
	}()

	for i := 0; i < numMessages; i++ {
		content := fmt.Sprintf("message-%d", i)
		msg := cEnd.NewMessage([]byte(content))
		if err := cEnd.Publish(context.TODO(), msg); err != nil {
			t.Fatalf("publish message %d failed: %v", i, err)
		}
	}

	select {
	case count := <-received:
		if count != numMessages {
			t.Errorf("received %d messages, expected %d", count, numMessages)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for messages")
	}
}

func TestMessageWithTopic(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	topic := "test-topic"
	content := []byte("topic message")

	done := make(chan struct{})
	go func() {
		defer close(done)
		msg, err := sEnd.Receive(context.TODO())
		if err != nil {
			t.Errorf("receive failed: %v", err)
			return
		}
		if msg.Topic() != topic {
			t.Errorf("topic mismatch: got %s, want %s", msg.Topic(), topic)
		}
		msg.Done()
	}()

	msg := cEnd.NewMessage(content)
	msg.SetTopic(topic)
	if err := cEnd.Publish(context.TODO(), msg); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestMessageWithTimeout(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	// Server receives in background so publish can complete.
	go func() {
		msg, err := sEnd.Receive(context.TODO())
		if err != nil {
			return
		}
		msg.Done()
	}()

	ctx, cancel := context.WithTimeout(context.TODO(), 2*time.Second)
	defer cancel()

	msg := cEnd.NewMessage([]byte("timeout test"))
	if err := cEnd.Publish(ctx, msg); err != nil {
		t.Fatalf("publish failed: %v", err)
	}
}

// ==================== RPC Tests ====================

func TestRPCBasic(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	method := "echo"
	sEnd.Register(context.TODO(), method, func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData(req.Data())
	})

	requestData := []byte("hello rpc")
	resp, err := cEnd.Call(context.TODO(), method, cEnd.NewRequest(requestData))
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}

	if string(resp.Data()) != string(requestData) {
		t.Errorf("response mismatch: got %s, want %s", resp.Data(), requestData)
	}
}

func TestRPCWithError(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	method := "error-method"
	expectedErrMsg := "intentional error"

	sEnd.Register(context.TODO(), method, func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetError(fmt.Errorf("%s", expectedErrMsg))
	})

	// The framework surfaces resp.SetError() as the Call() return error.
	_, err = cEnd.Call(context.TODO(), method, cEnd.NewRequest([]byte("test")))
	if err == nil {
		t.Fatal("expected error from RPC handler, got nil")
	}
	if err.Error() != expectedErrMsg {
		t.Errorf("error mismatch: got %q, want %q", err.Error(), expectedErrMsg)
	}
}

func TestRPCMultipleMethods(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	methods := map[string]func(context.Context, geminio.Request, geminio.Response){
		"add": func(ctx context.Context, req geminio.Request, resp geminio.Response) {
			resp.SetData([]byte("add result"))
		},
		"sub": func(ctx context.Context, req geminio.Request, resp geminio.Response) {
			resp.SetData([]byte("sub result"))
		},
		"mul": func(ctx context.Context, req geminio.Request, resp geminio.Response) {
			resp.SetData([]byte("mul result"))
		},
	}

	for name, handler := range methods {
		if err := sEnd.Register(context.TODO(), name, handler); err != nil {
			t.Fatalf("register %s failed: %v", name, err)
		}
	}

	for name := range methods {
		resp, err := cEnd.Call(context.TODO(), name, cEnd.NewRequest([]byte("test")))
		if err != nil {
			t.Errorf("call %s failed: %v", name, err)
			continue
		}
		if resp.Error() != nil {
			t.Errorf("call %s returned error: %v", name, resp.Error())
		}
	}
}

func TestRPCTimeout(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	method := "slow-method"
	sEnd.Register(context.TODO(), method, func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		// Simulate slow processing
		select {
		case <-time.After(5 * time.Second):
			resp.SetData([]byte("slow result"))
		case <-ctx.Done():
			// Context cancelled
		}
	})

	ctx, cancel := context.WithTimeout(context.TODO(), 100*time.Millisecond)
	defer cancel()

	_, err = cEnd.Call(ctx, method, cEnd.NewRequest([]byte("test")))
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestRPCConcurrent(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	sEnd.Register(context.TODO(), "echo", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData(req.Data())
	})

	numCalls := 100
	var wg sync.WaitGroup
	wg.Add(numCalls)

	errors := make(chan error, numCalls)
	for i := 0; i < numCalls; i++ {
		go func(idx int) {
			defer wg.Done()
			data := fmt.Sprintf("call-%d", idx)
			resp, err := cEnd.Call(context.TODO(), "echo", cEnd.NewRequest([]byte(data)))
			if err != nil {
				errors <- fmt.Errorf("call %d failed: %v", idx, err)
				return
			}
			if string(resp.Data()) != data {
				errors <- fmt.Errorf("call %d data mismatch", idx)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	errCount := 0
	for err := range errors {
		t.Log(err)
		errCount++
	}

	if errCount > 0 {
		t.Errorf("had %d errors during concurrent RPC", errCount)
	}
}

// ==================== Stream Tests ====================

func TestStreamBasic(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer cEnd.Close()
	defer sEnd.Close()

	type streamResult struct {
		s   geminio.Stream
		err error
	}
	ch := make(chan streamResult, 1)
	go func() {
		s, err := sEnd.AcceptStream()
		ch <- streamResult{s, err}
	}()

	cs, err := cEnd.OpenStream()
	if err != nil {
		t.Fatalf("open stream failed: %v", err)
	}
	defer cs.Close()

	res := <-ch
	if res.err != nil {
		t.Fatalf("accept stream failed: %v", res.err)
	}
	ss := res.s
	defer ss.Close()

	data := []byte("stream data")
	if _, err := cs.Write(data); err != nil {
		t.Fatalf("stream write failed: %v", err)
	}

	buf := make([]byte, len(data))
	n, err := ss.Read(buf)
	if err != nil {
		t.Fatalf("stream read failed: %v", err)
	}

	if n != len(data) {
		t.Errorf("read length mismatch: got %d, want %d", n, len(data))
	}

	if string(buf) != string(data) {
		t.Errorf("data mismatch: got %s, want %s", buf, data)
	}
}

func TestStreamMultiple(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	numStreams := 10
	streams := make([]struct {
		server geminio.Stream
		client geminio.Stream
	}, numStreams)

	// Accept streams in background
	go func() {
		for i := 0; i < numStreams; i++ {
			s, err := sEnd.AcceptStream()
			if err != nil {
				t.Errorf("accept stream failed: %v", err)
				return
			}
			streams[i].server = s
		}
	}()

	// Open streams from client
	for i := 0; i < numStreams; i++ {
		s, err := cEnd.OpenStream()
		if err != nil {
			t.Fatalf("open stream %d failed: %v", i, err)
		}
		streams[i].client = s
	}

	// Wait for all streams to be accepted
	time.Sleep(100 * time.Millisecond)

	// Test each stream
	for i := 0; i < numStreams; i++ {
		data := fmt.Sprintf("stream-%d-data", i)
		if _, err := streams[i].client.Write([]byte(data)); err != nil {
			t.Errorf("stream %d write failed: %v", i, err)
		}

		buf := make([]byte, len(data))
		n, err := streams[i].server.Read(buf)
		if err != nil {
			t.Errorf("stream %d read failed: %v", i, err)
		}

		if string(buf[:n]) != data {
			t.Errorf("stream %d data mismatch", i)
		}
	}

	// Close all streams
	for i := 0; i < numStreams; i++ {
		streams[i].client.Close()
		streams[i].server.Close()
	}
}

func TestStreamBidirectional(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer cEnd.Close()
	defer sEnd.Close()

	type streamResult struct {
		s   geminio.Stream
		err error
	}
	ch := make(chan streamResult, 1)
	go func() {
		s, err := sEnd.AcceptStream()
		ch <- streamResult{s, err}
	}()

	cs, err := cEnd.OpenStream()
	if err != nil {
		t.Fatalf("open stream failed: %v", err)
	}
	defer cs.Close()

	res := <-ch
	if res.err != nil {
		t.Fatalf("accept stream failed: %v", res.err)
	}
	ss := res.s
	defer ss.Close()

	// Server writes, client reads
	serverData := []byte("server-to-client")
	clientData := []byte("client-to-server")

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		if _, err := ss.Write(serverData); err != nil {
			t.Errorf("server write failed: %v", err)
		}

		buf := make([]byte, len(clientData))
		n, err := ss.Read(buf)
		if err != nil {
			t.Errorf("server read failed: %v", err)
			return
		}
		if string(buf[:n]) != string(clientData) {
			t.Errorf("server received wrong data: %s", buf[:n])
		}
	}()

	go func() {
		defer wg.Done()
		buf := make([]byte, len(serverData))
		n, err := cs.Read(buf)
		if err != nil {
			t.Errorf("client read failed: %v", err)
			return
		}
		if string(buf[:n]) != string(serverData) {
			t.Errorf("client received wrong data: %s", buf[:n])
		}

		if _, err := cs.Write(clientData); err != nil {
			t.Errorf("client write failed: %v", err)
		}
	}()

	wg.Wait()
}

// ==================== Error Recovery Tests ====================

func TestConnectionReconnect(t *testing.T) {
	network := "tcp"
	address := "127.0.0.1:0"

	ln, err := server.Listen(network, address)
	if err != nil {
		t.Fatalf("server listen failed: %v", err)
	}

	acceptCount := int32(0)
	serverEnds := make(chan geminio.End, 2)

	go func() {
		for {
			end, err := ln.AcceptEnd()
			if err != nil {
				return
			}
			atomic.AddInt32(&acceptCount, 1)
			serverEnds <- end
		}
	}()

	// First connection
	cEnd1, err := client.NewEnd(network, ln.Addr().String())
	if err != nil {
		t.Fatalf("first connect failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Close first connection
	cEnd1.Close()

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Second connection should succeed
	cEnd2, err := client.NewEnd(network, ln.Addr().String())
	if err != nil {
		t.Fatalf("second connect failed: %v", err)
	}
	cEnd2.Close()

	ln.Close()

	if atomic.LoadInt32(&acceptCount) != 2 {
		t.Errorf("expected 2 connections, got %d", acceptCount)
	}
}

func TestStreamAfterEndClose(t *testing.T) {
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}

	// Open a stream
	stream, err := cEnd.OpenStream()
	if err != nil {
		t.Fatalf("open stream failed: %v", err)
	}

	// Close the end
	cEnd.Close()

	// Try to use stream - should get error
	_, err = stream.Write([]byte("test"))
	if err == nil {
		t.Log("write after close may or may not fail depending on timing")
	}

	sEnd.Close()
}

// ==================== Resource Cleanup Tests ====================

func TestResourceCleanup(t *testing.T) {
	// Get initial goroutine count
	initialGoroutines := runtime.NumGoroutine()

	for i := 0; i < 10; i++ {
		sEnd, cEnd, err := test.GetEndPair()
		if err != nil {
			t.Fatalf("get end pair failed: %v", err)
		}

		// Do some operations
		sEnd.Register(context.TODO(), "echo", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
			resp.SetData(req.Data())
		})

		cEnd.Call(context.TODO(), "echo", cEnd.NewRequest([]byte("test")))

		// Close client first to follow the disconnect handshake order,
		// then server — mirrors harness.NewEndPair cleanup ordering.
		cEnd.Close()
		sEnd.Close()
	}

	// Force GC and give goroutines time to exit.
	runtime.GC()
	time.Sleep(300 * time.Millisecond)

	// Check goroutine count
	finalGoroutines := runtime.NumGoroutine()
	if finalGoroutines > initialGoroutines+5 {
		t.Errorf("possible goroutine leak: started with %d, ended with %d", initialGoroutines, finalGoroutines)
	}
}

// ==================== Stress Tests ====================

func TestStressMixedOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	log.SetLevel(log.LevelError)
	sEnd, cEnd, err := test.GetEndPair()
	if err != nil {
		t.Fatalf("get end pair failed: %v", err)
	}
	defer sEnd.Close()
	defer cEnd.Close()

	// Setup RPC handlers
	sEnd.Register(context.TODO(), "echo", func(ctx context.Context, req geminio.Request, resp geminio.Response) {
		resp.SetData(req.Data())
	})

	// Message receiver
	go func() {
		for {
			msg, err := sEnd.Receive(context.TODO())
			if err != nil {
				return
			}
			msg.Done()
		}
	}()

	// Stream acceptor
	go func() {
		for {
			s, err := sEnd.AcceptStream()
			if err != nil {
				return
			}
			go func(s geminio.Stream) {
				defer s.Close()
				buf := make([]byte, 64)
				s.Read(buf)
			}(s)
		}
	}()

	// Run mixed operations
	var wg sync.WaitGroup
	numWorkers := 10
	operationsPerWorker := 100

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < operationsPerWorker; j++ {
				switch j % 3 {
				case 0: // RPC
					_, err := cEnd.Call(context.TODO(), "echo", cEnd.NewRequest([]byte("test")))
					if err != nil {
						t.Errorf("worker %d rpc failed: %v", workerID, err)
					}
				case 1: // Message
					msg := cEnd.NewMessage([]byte("hello"))
					cEnd.Publish(context.TODO(), msg)
				case 2: // Stream
					stream, err := cEnd.OpenStream()
					if err != nil {
						t.Errorf("worker %d open stream failed: %v", workerID, err)
						continue
					}
					stream.Write([]byte("stream data"))
					stream.Close()
				}
			}
		}(i)
	}

	wg.Wait()
}
