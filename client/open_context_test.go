package client

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOpenRetryWaitCancellationDoesNotCancelSharedAttempt(t *testing.T) {
	shared := &openRetry{done: make(chan struct{})}
	re := &RetryEnd{openRetry: shared}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := re.waitOpenRetry(ctx, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if re.openRetry != shared {
		t.Fatal("canceled caller changed shared reconnect")
	}
	select {
	case <-shared.done:
		t.Fatal("caller canceled shared reconnect")
	default:
	}
	shared.err = errors.New("reconnect failed")
	close(shared.done)
	if err := re.waitOpenRetry(context.Background(), nil); err != shared.err {
		t.Fatal(err)
	}
}
