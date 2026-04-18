package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
)

const (
	defaultInterval   = 5 * time.Minute
	defaultBatchSize  = 100
	defaultMaxRetries = 3
)

type metadataStore interface {
	ListBuckets(ctx context.Context) ([]metadata.Bucket, error)
	ListObjects(ctx context.Context, bucket string) ([]metadata.ObjectVersion, error)
	DeleteAllObjectVersions(ctx context.Context, bucket string, key string) ([]metadata.ObjectVersion, error)
}

type blobStore interface {
	DeleteObject(ctx context.Context, ref blob.ObjectRef, versioned bool) error
}

// Options configures the lifecycle evaluator worker.
type Options struct {
	Metadata     metadataStore
	Blob         blobStore
	Rules        []Rule
	Interval     time.Duration
	BatchSize    int
	MaxRetries   int
	RetryBackoff time.Duration
	DryRun       bool
	Now          func() time.Time
	Sleep        func(context.Context, time.Duration) error
	QueueDepthFn func(worker string, depth int)
}

// Report describes one lifecycle scan execution.
type Report struct {
	Scanned  int
	Eligible int
	Deleted  int
	Failed   int
	DryRun   bool
	Duration time.Duration
}

// Metrics stores cumulative lifecycle execution counters.
type Metrics struct {
	Runs            int
	ScannedTotal    int
	EligibleTotal   int
	DeletedTotal    int
	FailedTotal     int
	LastRunDuration time.Duration
	LastRunDryRun   bool
}

// Snapshot describes lifecycle worker config and cumulative metrics.
type Snapshot struct {
	Rules        []Rule
	Interval     time.Duration
	BatchSize    int
	MaxRetries   int
	RetryBackoff time.Duration
	DryRun       bool
	Metrics      Metrics
}

// Worker scans metadata and expires objects by lifecycle rules.
type Worker struct {
	metadata     metadataStore
	blob         blobStore
	rules        []Rule
	interval     time.Duration
	batchSize    int
	maxRetries   int
	retryBackoff time.Duration
	dryRun       bool
	now          func() time.Time
	sleep        func(context.Context, time.Duration) error
	queueDepthFn func(worker string, depth int)

	mu      sync.Mutex
	metrics Metrics
}

// NewWorker builds a lifecycle worker.
func NewWorker(opts Options) (*Worker, error) {
	if opts.Metadata == nil {
		return nil, fmt.Errorf("metadata store is required")
	}
	if !opts.DryRun && opts.Blob == nil {
		return nil, fmt.Errorf("blob store is required when dry-run is disabled")
	}
	if len(opts.Rules) == 0 {
		return nil, fmt.Errorf("at least one lifecycle rule is required")
	}
	if opts.Interval <= 0 {
		opts.Interval = defaultInterval
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaultBatchSize
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = 0
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepWithContext
	}

	for i, rule := range opts.Rules {
		if rule.ExpireAfter <= 0 {
			return nil, fmt.Errorf("lifecycle rule %d has non-positive age", i+1)
		}
	}

	return &Worker{
		metadata:     opts.Metadata,
		blob:         opts.Blob,
		rules:        append([]Rule(nil), opts.Rules...),
		interval:     opts.Interval,
		batchSize:    opts.BatchSize,
		maxRetries:   opts.MaxRetries,
		retryBackoff: opts.RetryBackoff,
		dryRun:       opts.DryRun,
		now:          opts.Now,
		sleep:        opts.Sleep,
		queueDepthFn: opts.QueueDepthFn,
	}, nil
}

// Run executes one scan immediately, then repeats until context cancellation.
func (w *Worker) Run(ctx context.Context) error {
	if _, err := w.RunOnce(ctx); err != nil {
		return err
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := w.RunOnce(ctx); err != nil {
				return err
			}
		}
	}
}

// RunOnce scans objects and applies lifecycle expiration for matching rules.
func (w *Worker) RunOnce(ctx context.Context) (Report, error) {
	started := w.now()
	report := Report{DryRun: w.dryRun}

	buckets, err := w.metadata.ListBuckets(ctx)
	if err != nil {
		return report, fmt.Errorf("list buckets: %w", err)
	}

	candidates := make([]metadata.ObjectVersion, 0)
	now := w.now()
	for _, bucket := range buckets {
		objects, listErr := w.metadata.ListObjects(ctx, bucket.Name)
		if listErr != nil {
			report.Failed++
			continue
		}
		for _, obj := range objects {
			report.Scanned++
			age := now.Sub(obj.CreatedAt)
			if age < 0 {
				continue
			}
			if matchesAnyRule(w.rules, obj.Key, age) {
				report.Eligible++
				candidates = append(candidates, obj)
			}
		}
	}
	w.setQueueDepth(len(candidates))
	processed := 0

	for start := 0; start < len(candidates); start += w.batchSize {
		end := start + w.batchSize
		if end > len(candidates) {
			end = len(candidates)
		}
		for _, candidate := range candidates[start:end] {
			if ctx.Err() != nil {
				report.Duration = w.now().Sub(started)
				w.record(report)
				return report, ctx.Err()
			}
			if w.dryRun {
				processed++
				w.setQueueDepth(len(candidates) - processed)
				continue
			}
			deleted, delErr := w.deleteWithRetry(ctx, candidate)
			if delErr != nil {
				report.Failed++
				processed++
				w.setQueueDepth(len(candidates) - processed)
				continue
			}
			if deleted {
				report.Deleted++
			}
			processed++
			w.setQueueDepth(len(candidates) - processed)
		}
	}
	w.setQueueDepth(0)

	report.Duration = w.now().Sub(started)
	w.record(report)
	return report, nil
}

// MetricsSnapshot returns cumulative metrics from all completed runs.
func (w *Worker) MetricsSnapshot() Metrics {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.metrics
}

// Snapshot returns a point-in-time view of worker config and metrics.
func (w *Worker) Snapshot() Snapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	rules := append([]Rule(nil), w.rules...)
	return Snapshot{
		Rules:        rules,
		Interval:     w.interval,
		BatchSize:    w.batchSize,
		MaxRetries:   w.maxRetries,
		RetryBackoff: w.retryBackoff,
		DryRun:       w.dryRun,
		Metrics:      w.metrics,
	}
}

func (w *Worker) record(report Report) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.metrics.Runs++
	w.metrics.ScannedTotal += report.Scanned
	w.metrics.EligibleTotal += report.Eligible
	w.metrics.DeletedTotal += report.Deleted
	w.metrics.FailedTotal += report.Failed
	w.metrics.LastRunDuration = report.Duration
	w.metrics.LastRunDryRun = report.DryRun
}

func (w *Worker) deleteWithRetry(ctx context.Context, candidate metadata.ObjectVersion) (bool, error) {
	attempts := w.maxRetries + 1
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		deleted, err := w.deleteOne(ctx, candidate)
		if err == nil {
			if lastErr != nil && !deleted {
				return false, lastErr
			}
			return deleted, nil
		}
		lastErr = err
		if attempt == attempts-1 {
			break
		}
		if w.retryBackoff <= 0 {
			continue
		}
		backoff := w.retryBackoff * time.Duration(1<<attempt)
		if sleepErr := w.sleep(ctx, backoff); sleepErr != nil {
			return false, sleepErr
		}
	}
	return false, fmt.Errorf("delete lifecycle candidate %s/%s failed after %d attempts: %w", candidate.Bucket, candidate.Key, attempts, lastErr)
}

func (w *Worker) deleteOne(ctx context.Context, candidate metadata.ObjectVersion) (bool, error) {
	versions, err := w.metadata.DeleteAllObjectVersions(ctx, candidate.Bucket, candidate.Key)
	if err != nil {
		if errors.Is(err, metadata.ErrNotFound) {
			return false, nil
		}
		return false, err
	}

	for _, version := range versions {
		if err := w.blob.DeleteObject(ctx, blob.ObjectRef{
			Bucket:    version.Bucket,
			Key:       version.Key,
			VersionID: version.VersionID,
		}, true); err != nil {
			return false, err
		}
	}

	return len(versions) > 0, nil
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (w *Worker) setQueueDepth(depth int) {
	if w.queueDepthFn == nil {
		return
	}
	w.queueDepthFn("lifecycle", depth)
}
