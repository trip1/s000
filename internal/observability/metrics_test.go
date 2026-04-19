package observability

import (
	"testing"
	"time"
)

func TestCollectorSnapshotIncludesRequestTrend(t *testing.T) {
	t.Parallel()

	c := NewCollector()
	c.ObserveRequest(200, 25*time.Millisecond, 10, 20)
	c.ObserveRequest(503, 40*time.Millisecond, 0, 5)

	s := c.Snapshot()
	if s.RequestTotal != 2 {
		t.Fatalf("expected request total 2, got %d", s.RequestTotal)
	}
	if s.RequestErrorTotal != 1 {
		t.Fatalf("expected request error total 1, got %d", s.RequestErrorTotal)
	}
	if s.Request5xxTotal != 1 || s.Request4xxTotal != 0 {
		t.Fatalf("expected 4xx=0 5xx=1, got 4xx=%d 5xx=%d", s.Request4xxTotal, s.Request5xxTotal)
	}
	if got := len(s.RequestsPerMinute); got != requestTrendWindowMinutes {
		t.Fatalf("expected request trend window %d, got %d", requestTrendWindowMinutes, got)
	}
	if got := len(s.ErrorsPerMinute); got != requestTrendWindowMinutes {
		t.Fatalf("expected error trend window %d, got %d", requestTrendWindowMinutes, got)
	}
	if s.RequestsPerMinute[len(s.RequestsPerMinute)-1] == 0 {
		t.Fatalf("expected latest trend bucket to include recent requests, got %v", s.RequestsPerMinute)
	}
	if s.ErrorsPerMinute[len(s.ErrorsPerMinute)-1] == 0 {
		t.Fatalf("expected latest error trend bucket to include recent errors, got %v", s.ErrorsPerMinute)
	}
	if s.LatencyP95Seconds <= 0 {
		t.Fatalf("expected p95 latency to be positive, got %f", s.LatencyP95Seconds)
	}
}
