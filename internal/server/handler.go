package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/lifecycle"
	"ds9labs.com/s000/internal/metadata"
	"ds9labs.com/s000/internal/observability"
)

// RequestVerifier validates incoming authenticated S3 requests.
type RequestVerifier interface {
	VerifyRequest(*http.Request) error
}

type Options struct {
	Domain                            string
	MaxInFlight                       int
	Verifier                          RequestVerifier
	Metadata                          metadata.Store
	Blob                              *blob.Store
	Lifecycle                         *lifecycle.Worker
	Metrics                           *observability.Collector
	MetricsPath                       string
	ReadyCheck                        func(ctx context.Context) error
	Tracing                           observability.TraceHooks
	TracingOn                         bool
	HeavyOpsWorkers                   int
	HeavyOpsQueue                     int
	AuditEnabled                      bool
	Audit                             AuditSink
	AuthFailThreshold                 int
	AuthFailWindow                    time.Duration
	AuthBlockDuration                 time.Duration
	AuthFailureProtector              authFailureProtector
	UIAccessKey                       string
	UISecretKey                       string
	UITheme                           string
	BucketRegion                      string
}

// NewHandler builds the root HTTP handler and middleware stack.
func NewHandler(opts Options) http.Handler {
	if opts.MaxInFlight <= 0 {
		opts.MaxInFlight = 128
	}
	if opts.HeavyOpsWorkers <= 0 {
		opts.HeavyOpsWorkers = 4
	}
	if opts.HeavyOpsQueue < 0 {
		opts.HeavyOpsQueue = 0
	}
	if opts.AuthFailThreshold <= 0 {
		opts.AuthFailThreshold = 20
	}
	if opts.AuthFailWindow <= 0 {
		opts.AuthFailWindow = time.Minute
	}
	if opts.AuthBlockDuration <= 0 {
		opts.AuthBlockDuration = 5 * time.Minute
	}
	if opts.AuthFailureProtector == nil {
		opts.AuthFailureProtector = newAuthFailureProtector(opts.AuthFailThreshold, opts.AuthFailWindow, opts.AuthBlockDuration, nil)
	}

	mux := http.NewServeMux()
	web := webUIHandler(opts)
	mux.HandleFunc("/healthz", healthz(opts))
	mux.HandleFunc("/readyz", readyz(opts))
	mux.Handle("/app", web)
	mux.Handle("/app/", web)
	mux.Handle("/assets/", web)
	metricsPath := opts.MetricsPath
	if metricsPath == "" {
		metricsPath = "/metrics"
	}
	mux.HandleFunc(metricsPath, metricsHandler(opts))
	mux.HandleFunc("/debug/lifecycle/config", lifecycleConfigDebug(opts))
	mux.HandleFunc("/debug/lifecycle/metrics", lifecycleMetricsDebug(opts))
	mux.HandleFunc("/", s3Handler(opts))

	return withMiddleware(mux, opts)
}

func healthz(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := dependencyCheck(r.Context(), opts); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, "unhealthy: %v\n", err)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	}
}

func readyz(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := dependencyCheck(r.Context(), opts); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, "not ready: %v\n", err)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ready")
	}
}

func metricsHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if opts.Metrics != nil {
			_, _ = w.Write([]byte(opts.Metrics.RenderPrometheus()))
		}
	}
}

func dependencyCheck(ctx context.Context, opts Options) error {
	if opts.Metadata == nil {
		return fmt.Errorf("metadata store unavailable")
	}
	if opts.Blob == nil {
		return fmt.Errorf("blob store unavailable")
	}
	if _, err := opts.Metadata.ListBuckets(ctx); err != nil {
		return fmt.Errorf("metadata check failed: %w", err)
	}
	if err := opts.Blob.HealthCheck(ctx); err != nil {
		return fmt.Errorf("blob check failed: %w", err)
	}
	if opts.ReadyCheck != nil {
		if err := opts.ReadyCheck(ctx); err != nil {
			return fmt.Errorf("dependency check failed: %w", err)
		}
	}
	return nil
}

func s3Handler(opts Options) http.HandlerFunc {
	api := newS3API(opts)
	return func(w http.ResponseWriter, r *http.Request) {
		api.ServeHTTP(w, r)
	}
}

func bucketAndKey(r *http.Request, domain string) (bucket string, key string, ok bool) {
	bucket, key, ok = parseVirtualHostStyle(r, domain)
	if ok {
		return bucket, key, true
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", "", false
	}

	bucket = parts[0]
	if len(parts) > 1 {
		key = strings.Join(parts[1:], "/")
	}

	return bucket, key, true
}

func parseVirtualHostStyle(r *http.Request, domain string) (bucket string, key string, ok bool) {
	host := r.Host
	if host == "" {
		return "", "", false
	}

	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	if domain == "" {
		return "", "", false
	}

	if !strings.HasSuffix(host, "."+domain) {
		return "", "", false
	}

	bucket = strings.TrimSuffix(host, "."+domain)
	if bucket == "" || strings.Contains(bucket, ".") {
		return "", "", false
	}

	key = strings.TrimPrefix(r.URL.Path, "/")
	return bucket, key, true
}
