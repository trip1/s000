package observability

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var latencyBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

const requestTrendWindowMinutes = 20

// Collector stores request and worker metrics and renders Prometheus text.
type Collector struct {
	mu                sync.Mutex
	requestTotal      uint64
	requestErrorTotal uint64
	request4xxTotal   uint64
	request5xxTotal   uint64
	requestBytesIn    uint64
	requestBytesOut   uint64
	latencyCount      uint64
	latencySum        float64
	latencyBucketCnt  []uint64
	workerQueueDepth  map[string]int
	requestByMinute   map[int64]uint64
	errorByMinute     map[int64]uint64
}

// Snapshot captures a point-in-time copy of aggregate request metrics.
type Snapshot struct {
	RequestTotal      uint64
	RequestErrorTotal uint64
	Request4xxTotal   uint64
	Request5xxTotal   uint64
	RequestBytesIn    uint64
	RequestBytesOut   uint64
	LatencyCount      uint64
	LatencySumSeconds float64
	LatencyP95Seconds float64
	RequestsPerMinute []uint64
	ErrorsPerMinute   []uint64
}

// NewCollector creates a new metrics collector.
func NewCollector() *Collector {
	return &Collector{
		latencyBucketCnt: make([]uint64, len(latencyBuckets)),
		workerQueueDepth: make(map[string]int),
		requestByMinute:  make(map[int64]uint64),
		errorByMinute:    make(map[int64]uint64),
	}
}

// ObserveRequest records request metrics.
func (c *Collector) ObserveRequest(status int, latency time.Duration, bytesIn int64, bytesOut int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.requestTotal++
	nowMinute := time.Now().UTC().Unix() / 60
	c.requestByMinute[nowMinute]++
	for minute := range c.requestByMinute {
		if minute < nowMinute-(requestTrendWindowMinutes*3) {
			delete(c.requestByMinute, minute)
		}
	}
	if status >= 400 {
		c.requestErrorTotal++
		c.errorByMinute[nowMinute]++
	}
	for minute := range c.errorByMinute {
		if minute < nowMinute-(requestTrendWindowMinutes*3) {
			delete(c.errorByMinute, minute)
		}
	}
	if status >= 400 && status < 500 {
		c.request4xxTotal++
	}
	if status >= 500 {
		c.request5xxTotal++
	}
	if bytesIn > 0 {
		c.requestBytesIn += uint64(bytesIn)
	}
	if bytesOut > 0 {
		c.requestBytesOut += uint64(bytesOut)
	}

	seconds := latency.Seconds()
	c.latencyCount++
	c.latencySum += seconds
	for i, b := range latencyBuckets {
		if seconds <= b {
			c.latencyBucketCnt[i]++
		}
	}
}

// Snapshot returns a copy of the current aggregate request metrics.
func (c *Collector) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	return Snapshot{
		RequestTotal:      c.requestTotal,
		RequestErrorTotal: c.requestErrorTotal,
		Request4xxTotal:   c.request4xxTotal,
		Request5xxTotal:   c.request5xxTotal,
		RequestBytesIn:    c.requestBytesIn,
		RequestBytesOut:   c.requestBytesOut,
		LatencyCount:      c.latencyCount,
		LatencySumSeconds: c.latencySum,
		LatencyP95Seconds: histogramQuantileFromCumulative(c.latencyBucketCnt, c.latencyCount, 0.95),
		RequestsPerMinute: requestTrend(c.requestByMinute, requestTrendWindowMinutes),
		ErrorsPerMinute:   requestTrend(c.errorByMinute, requestTrendWindowMinutes),
	}
}

func histogramQuantileFromCumulative(cumulative []uint64, total uint64, q float64) float64 {
	if total == 0 || len(cumulative) == 0 {
		return 0
	}
	target := uint64(float64(total) * q)
	if target == 0 {
		target = 1
	}
	for i, count := range cumulative {
		if count >= target {
			return latencyBuckets[i]
		}
	}
	return latencyBuckets[len(latencyBuckets)-1]
}

func requestTrend(buckets map[int64]uint64, window int64) []uint64 {
	nowMinute := time.Now().UTC().Unix() / 60
	out := make([]uint64, window)
	start := nowMinute - window + 1
	for i := range out {
		out[i] = buckets[start+int64(i)]
	}
	return out
}

// SetWorkerQueueDepth updates the latest worker queue depth.
func (c *Collector) SetWorkerQueueDepth(worker string, depth int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if depth < 0 {
		depth = 0
	}
	c.workerQueueDepth[worker] = depth
}

// RenderPrometheus returns metrics in Prometheus text exposition format.
func (c *Collector) RenderPrometheus() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	var b strings.Builder
	b.WriteString("# HELP s000_requests_total Total HTTP requests.\n")
	b.WriteString("# TYPE s000_requests_total counter\n")
	b.WriteString(fmt.Sprintf("s000_requests_total %d\n", c.requestTotal))

	b.WriteString("# HELP s000_request_errors_total Total HTTP requests with status >= 400.\n")
	b.WriteString("# TYPE s000_request_errors_total counter\n")
	b.WriteString(fmt.Sprintf("s000_request_errors_total %d\n", c.requestErrorTotal))

	b.WriteString("# HELP s000_request_bytes_in_total Total request bytes in.\n")
	b.WriteString("# TYPE s000_request_bytes_in_total counter\n")
	b.WriteString(fmt.Sprintf("s000_request_bytes_in_total %d\n", c.requestBytesIn))

	b.WriteString("# HELP s000_request_bytes_out_total Total response bytes out.\n")
	b.WriteString("# TYPE s000_request_bytes_out_total counter\n")
	b.WriteString(fmt.Sprintf("s000_request_bytes_out_total %d\n", c.requestBytesOut))

	b.WriteString("# HELP s000_request_latency_seconds Request latency histogram.\n")
	b.WriteString("# TYPE s000_request_latency_seconds histogram\n")
	for i, bound := range latencyBuckets {
		b.WriteString(fmt.Sprintf("s000_request_latency_seconds_bucket{le=\"%g\"} %d\n", bound, c.latencyBucketCnt[i]))
	}
	b.WriteString(fmt.Sprintf("s000_request_latency_seconds_bucket{le=\"+Inf\"} %d\n", c.latencyCount))
	b.WriteString(fmt.Sprintf("s000_request_latency_seconds_sum %g\n", c.latencySum))
	b.WriteString(fmt.Sprintf("s000_request_latency_seconds_count %d\n", c.latencyCount))

	b.WriteString("# HELP s000_worker_queue_depth Current worker queue depth by worker.\n")
	b.WriteString("# TYPE s000_worker_queue_depth gauge\n")
	keys := make([]string, 0, len(c.workerQueueDepth))
	for worker := range c.workerQueueDepth {
		keys = append(keys, worker)
	}
	sort.Strings(keys)
	for _, worker := range keys {
		b.WriteString(fmt.Sprintf("s000_worker_queue_depth{worker=\"%s\"} %d\n", worker, c.workerQueueDepth[worker]))
	}

	return b.String()
}
