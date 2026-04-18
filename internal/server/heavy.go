package server

import (
	"context"
	"errors"
	"sync"
)

var ErrServerOverloaded = errors.New("server overloaded")

type heavyOpLimiter struct {
	slots       chan struct{}
	workers     chan struct{}
	queueDepth  int
	mu          sync.Mutex
	depthUpdate func(worker string, depth int)
}

func newHeavyOpLimiter(workers int, queue int, depthUpdate func(worker string, depth int)) *heavyOpLimiter {
	if workers <= 0 {
		workers = 4
	}
	if queue < 0 {
		queue = 0
	}
	return &heavyOpLimiter{
		slots:       make(chan struct{}, workers+queue),
		workers:     make(chan struct{}, workers),
		depthUpdate: depthUpdate,
	}
}

func (l *heavyOpLimiter) Acquire(ctx context.Context) error {
	select {
	case l.slots <- struct{}{}:
		l.adjustDepth(1)
	default:
		return ErrServerOverloaded
	}

	select {
	case l.workers <- struct{}{}:
		return nil
	case <-ctx.Done():
		<-l.slots
		l.adjustDepth(-1)
		return ctx.Err()
	}
}

func (l *heavyOpLimiter) Release() {
	<-l.workers
	<-l.slots
	l.adjustDepth(-1)
}

func (l *heavyOpLimiter) QueueDepth() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.queueDepth
}

func (l *heavyOpLimiter) adjustDepth(delta int) {
	l.mu.Lock()
	l.queueDepth += delta
	if l.queueDepth < 0 {
		l.queueDepth = 0
	}
	depth := l.queueDepth
	l.mu.Unlock()
	if l.depthUpdate != nil {
		l.depthUpdate("heavy_ops", depth)
	}
}
