package server

import (
	"context"
	"testing"
	"time"
)

func TestHeavyOpLimiterCapacityAndQueue(t *testing.T) {
	t.Parallel()

	limiter := newHeavyOpLimiter(1, 1, nil)
	ctx := context.Background()

	if err := limiter.Acquire(ctx); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	queued := make(chan error, 1)
	go func() {
		queued <- limiter.Acquire(ctx)
	}()
	time.Sleep(20 * time.Millisecond)
	if err := limiter.Acquire(ctx); err != ErrServerOverloaded {
		t.Fatalf("expected overload error, got: %v", err)
	}

	if depth := limiter.QueueDepth(); depth != 2 {
		t.Fatalf("expected queue depth 2, got %d", depth)
	}

	limiter.Release()
	if err := <-queued; err != nil {
		t.Fatalf("queued acquire failed: %v", err)
	}
	limiter.Release()

	if depth := limiter.QueueDepth(); depth != 0 {
		t.Fatalf("expected queue depth 0 after release, got %d", depth)
	}
}

func TestHeavyOpLimiterRespectsContextCancellation(t *testing.T) {
	t.Parallel()

	limiter := newHeavyOpLimiter(1, 1, nil)
	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatalf("initial acquire failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := limiter.Acquire(ctx); err == nil {
		t.Fatal("expected context cancellation error")
	}

	limiter.Release()
}
