package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ds9labs.com/s000/internal/auth"
	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/lifecycle"
	"ds9labs.com/s000/internal/metadata"
	"ds9labs.com/s000/internal/observability"
)

func TestAuthGateBlocksUnauthenticatedS3Requests(t *testing.T) {
	t.Parallel()

	h := NewHandler(Options{Verifier: denyMissingAuthVerifier{}})
	req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rr.Code)
	}

	var resp s3ErrorResponse
	if err := xml.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode xml error: %v", err)
	}
	if resp.Code != "MissingAuthenticationToken" {
		t.Fatalf("expected MissingAuthenticationToken, got %q", resp.Code)
	}
}

func TestS3PathStyleRouting(t *testing.T) {
	t.Parallel()

	h := NewHandler(Options{Verifier: allowAllVerifier{}})
	req := httptest.NewRequest(http.MethodGet, "/photos/a.jpg", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rr.Code)
	}

	var resp s3ErrorResponse
	if err := xml.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode xml error: %v", err)
	}
	if resp.Code != "ServiceUnavailable" {
		t.Fatalf("expected ServiceUnavailable code, got %q", resp.Code)
	}
}

func TestS3VirtualHostStyleRouting(t *testing.T) {
	t.Parallel()

	h := NewHandler(Options{Domain: "s000.local", Verifier: allowAllVerifier{}})
	req := httptest.NewRequest(http.MethodGet, "/a.jpg", nil)
	req.Host = "photos.s000.local"
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rr.Code)
	}

	var resp s3ErrorResponse
	if err := xml.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode xml error: %v", err)
	}
	if resp.Code != "ServiceUnavailable" {
		t.Fatalf("expected ServiceUnavailable code, got %q", resp.Code)
	}
}

func TestRequestIDHeaderAndErrorPayload(t *testing.T) {
	t.Parallel()

	h := NewHandler(Options{Verifier: allowAllVerifier{}})
	req := httptest.NewRequest(http.MethodGet, "/bucket", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	requestID := rr.Header().Get("X-Amz-Request-Id")
	if requestID == "" {
		t.Fatal("expected X-Amz-Request-Id header")
	}

	var resp s3ErrorResponse
	if err := xml.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode xml error: %v", err)
	}
	if resp.RequestID == "" {
		t.Fatal("expected RequestId in XML error")
	}
	if resp.RequestID != requestID {
		t.Fatalf("expected RequestId %q, got %q", requestID, resp.RequestID)
	}
}

func TestRateLimitReturnsSlowDown(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	base := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-gate
		w.WriteHeader(http.StatusOK)
	})

	h := withRateLimit(withRequestID(base), Options{MaxInFlight: 1})

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/bucket/key1", nil)
		h.ServeHTTP(rr, req)
	}()

	time.Sleep(25 * time.Millisecond)

	req2 := httptest.NewRequest(http.MethodGet, "/bucket/key2", nil)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)

	close(gate)
	<-firstDone

	if rr2.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rr2.Code)
	}

	var resp s3ErrorResponse
	if err := xml.Unmarshal(rr2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode xml error: %v", err)
	}
	if resp.Code != "SlowDown" {
		t.Fatalf("expected SlowDown, got %q", resp.Code)
	}
}

func TestPanicRecoveryReturnsStructuredError(t *testing.T) {
	t.Parallel()

	panicHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})
	h := withRecovery(withRequestID(panicHandler))

	req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rr.Code)
	}

	var resp s3ErrorResponse
	if err := xml.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode xml error: %v", err)
	}
	if resp.Code != "InternalError" {
		t.Fatalf("expected InternalError, got %q", resp.Code)
	}
}

func TestNewHTTPServerAppliesDefaults(t *testing.T) {
	t.Parallel()

	h := http.NewServeMux()
	srv := NewHTTPServer(":9000", h)

	if srv.Addr != ":9000" {
		t.Fatalf("expected addr :9000, got %q", srv.Addr)
	}
	if srv.Handler != h {
		t.Fatal("expected handler to match")
	}
	if srv.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("expected ReadHeaderTimeout 5s, got %s", srv.ReadHeaderTimeout)
	}
	if srv.IdleTimeout != 60*time.Second {
		t.Fatalf("expected IdleTimeout 60s, got %s", srv.IdleTimeout)
	}
	if srv.MaxHeaderBytes != 1<<20 {
		t.Fatalf("expected MaxHeaderBytes %d, got %d", 1<<20, srv.MaxHeaderBytes)
	}
}

func TestNewHTTPServerWithOptionsOverrides(t *testing.T) {
	t.Parallel()

	h := http.NewServeMux()
	srv := NewHTTPServerWithOptions(":9000", h, HTTPServerOptions{
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       4 * time.Second,
		WriteTimeout:      8 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    4096,
		DisableKeepAlive:  true,
		EnableTLS:         true,
	})

	if srv.ReadHeaderTimeout != 2*time.Second {
		t.Fatalf("expected ReadHeaderTimeout 2s, got %s", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 4*time.Second {
		t.Fatalf("expected ReadTimeout 4s, got %s", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 8*time.Second {
		t.Fatalf("expected WriteTimeout 8s, got %s", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 30*time.Second {
		t.Fatalf("expected IdleTimeout 30s, got %s", srv.IdleTimeout)
	}
	if srv.MaxHeaderBytes != 4096 {
		t.Fatalf("expected MaxHeaderBytes 4096, got %d", srv.MaxHeaderBytes)
	}
	if srv.TLSConfig == nil {
		t.Fatal("expected TLS config when TLS is enabled")
	}
	if srv.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("expected minimum TLS 1.2, got %x", srv.TLSConfig.MinVersion)
	}
}

func TestOverloadedHeavyOperationReturnsSlowDown(t *testing.T) {
	t.Parallel()

	h := newS3TestHandlerWithOptions(t, Options{MaxInFlight: 64, HeavyOpsWorkers: 1, HeavyOpsQueue: 0})

	if execute(t, h, http.MethodPut, "/photos", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}

	gate := make(chan struct{})
	started := make(chan struct{})
	go func() {
		defer close(started)
		req := httptest.NewRequest(http.MethodPut, "/photos/blocked.bin", &blockingReader{gate: gate, payload: []byte("payload")})
		req.Header.Set("Authorization", "test")
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, req)
	}()
	time.Sleep(20 * time.Millisecond)

	resp := execute(t, h, http.MethodPut, "/photos/second.bin", "payload")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected overload status %d, got %d", http.StatusServiceUnavailable, resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "SlowDown") {
		t.Fatalf("expected SlowDown response, got %s", body)
	}

	close(gate)
	<-started
}

func TestLifecycleDebugEndpointsUnconfigured(t *testing.T) {
	t.Parallel()

	h := NewHandler(Options{Verifier: allowAllVerifier{}})

	for _, path := range []string{"/debug/lifecycle/config", "/debug/lifecycle/metrics"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "test")
		rr := httptest.NewRecorder()

		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected status %d for %s, got %d", http.StatusServiceUnavailable, path, rr.Code)
		}
		if rr.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("expected json content type for %s", path)
		}
	}
}

func TestLifecycleDebugEndpointsConfigured(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)

	mstore, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:test.db"})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	if err := mstore.CreateBucket(ctx, metadata.Bucket{Name: "photos", CreatedAt: now, VersioningStatus: "Suspended"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}
	bstore, err := blob.NewStore(blob.Config{RootDir: t.TempDir(), FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	meta, err := bstore.WriteObject(ctx, blob.ObjectRef{Bucket: "photos", Key: "logs/old.txt", VersionID: "v1"}, strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("write object failed: %v", err)
	}
	if err := mstore.PutObjectVersion(ctx, metadata.ObjectVersion{
		Bucket:         "photos",
		Key:            "logs/old.txt",
		VersionID:      "v1",
		Size:           meta.Size,
		ETag:           meta.MD5Hex,
		ChecksumSHA256: meta.SHA256,
		StoragePath:    meta.Path,
		CreatedAt:      now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("put object version failed: %v", err)
	}

	worker, err := lifecycle.NewWorker(lifecycle.Options{
		Metadata:     mstore,
		Blob:         bstore,
		Rules:        []lifecycle.Rule{{Prefix: "logs/", ExpireAfter: time.Hour}},
		DryRun:       true,
		Now:          func() time.Time { return now },
		BatchSize:    10,
		MaxRetries:   1,
		RetryBackoff: 0,
	})
	if err != nil {
		t.Fatalf("new lifecycle worker failed: %v", err)
	}
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("run once failed: %v", err)
	}

	h := NewHandler(Options{Verifier: allowAllVerifier{}, Lifecycle: worker})

	configReq := httptest.NewRequest(http.MethodGet, "/debug/lifecycle/config", nil)
	configReq.Header.Set("Authorization", "test")
	configRR := httptest.NewRecorder()
	h.ServeHTTP(configRR, configReq)
	if configRR.Code != http.StatusOK {
		t.Fatalf("expected config endpoint status %d, got %d", http.StatusOK, configRR.Code)
	}
	var configBody map[string]any
	if err := json.Unmarshal(configRR.Body.Bytes(), &configBody); err != nil {
		t.Fatalf("decode config json failed: %v", err)
	}
	if got, ok := configBody["dry_run"].(bool); !ok || !got {
		t.Fatalf("expected dry_run=true, got %#v", configBody["dry_run"])
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/debug/lifecycle/metrics", nil)
	metricsReq.Header.Set("Authorization", "test")
	metricsRR := httptest.NewRecorder()
	h.ServeHTTP(metricsRR, metricsReq)
	if metricsRR.Code != http.StatusOK {
		t.Fatalf("expected metrics endpoint status %d, got %d", http.StatusOK, metricsRR.Code)
	}
	var metricsBody map[string]any
	if err := json.Unmarshal(metricsRR.Body.Bytes(), &metricsBody); err != nil {
		t.Fatalf("decode metrics json failed: %v", err)
	}
	if got, ok := metricsBody["runs"].(float64); !ok || got < 1 {
		t.Fatalf("expected runs >= 1, got %#v", metricsBody["runs"])
	}
}

func TestMetricsEndpointExposesPrometheusText(t *testing.T) {
	t.Parallel()

	collector := observability.NewCollector()
	h := NewHandler(Options{Verifier: allowAllVerifier{}, Metrics: collector})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("expected prometheus text content type, got %q", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "s000_requests_total") {
		t.Fatalf("expected requests metric in body: %s", body)
	}
	if !strings.Contains(body, "s000_worker_queue_depth") {
		t.Fatalf("expected queue depth metric in body: %s", body)
	}
}

func TestDashboardShowsBucketInventoryAndAPIStats(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mstore, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:" + filepath.Join(root, "meta.db")})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	bstore, err := blob.NewStore(blob.Config{RootDir: root, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	now := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	if err := mstore.CreateBucket(ctx, metadata.Bucket{Name: "photos", CreatedAt: now, Region: "us-east-1", VersioningStatus: "Suspended"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}
	if err := mstore.PutObjectVersion(ctx, metadata.ObjectVersion{Bucket: "photos", Key: "img/a.jpg", VersionID: "null", Size: 512, ETag: "etag", ChecksumSHA256: "sha256", StoragePath: filepath.Join(root, "a.jpg"), CreatedAt: now}); err != nil {
		t.Fatalf("put object version failed: %v", err)
	}

	collector := observability.NewCollector()
	h := NewHandler(Options{Metadata: mstore, Blob: bstore, UIAccessKey: "admin", UISecretKey: "secret", Metrics: collector})

	loginForm := url.Values{"access_key": {"admin"}, "secret_key": {"secret"}}
	loginReq := httptest.NewRequest(http.MethodPost, "/app/login", strings.NewReader(loginForm.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRR := httptest.NewRecorder()
	h.ServeHTTP(loginRR, loginReq)
	if loginRR.Code != http.StatusSeeOther {
		t.Fatalf("expected login status %d, got %d", http.StatusSeeOther, loginRR.Code)
	}

	var sessionCookie *http.Cookie
	for _, c := range loginRR.Result().Cookies() {
		if c.Name == uiSessionCookie {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie after login")
	}

	dashboardReq := httptest.NewRequest(http.MethodGet, "/app", nil)
	dashboardReq.AddCookie(sessionCookie)
	dashboardRR := httptest.NewRecorder()
	h.ServeHTTP(dashboardRR, dashboardReq)

	if dashboardRR.Code != http.StatusOK {
		t.Fatalf("expected dashboard status %d, got %d", http.StatusOK, dashboardRR.Code)
	}
	body := dashboardRR.Body.String()
	if !strings.Contains(body, "Bucket inventory") {
		t.Fatalf("expected bucket inventory section, got %q", body)
	}
	if !strings.Contains(body, "photos") || !strings.Contains(body, ">1<") || !strings.Contains(body, ">512<") {
		t.Fatalf("expected bucket-level object totals in dashboard, got %q", body)
	}
	if !strings.Contains(body, "API request stats") {
		t.Fatalf("expected API request stats section, got %q", body)
	}
	if !strings.Contains(body, "Total requests</dt><dd>1</dd>") {
		t.Fatalf("expected request totals in dashboard, got %q", body)
	}
}

func TestDashboardStatsSSEStreamsHTMXEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mstore, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:" + filepath.Join(root, "meta.db")})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	bstore, err := blob.NewStore(blob.Config{RootDir: root, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	if err := mstore.CreateBucket(ctx, metadata.Bucket{Name: "ops", CreatedAt: time.Now().UTC(), Region: "us-east-1", VersioningStatus: "Suspended"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}

	collector := observability.NewCollector()
	collector.ObserveRequest(http.StatusNotFound, 12*time.Millisecond, 3, 9)
	h := NewHandler(Options{Metadata: mstore, Blob: bstore, UIAccessKey: "admin", UISecretKey: "secret", Metrics: collector})

	loginForm := url.Values{"access_key": {"admin"}, "secret_key": {"secret"}}
	loginReq := httptest.NewRequest(http.MethodPost, "/app/login", strings.NewReader(loginForm.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRR := httptest.NewRecorder()
	h.ServeHTTP(loginRR, loginReq)

	var sessionCookie *http.Cookie
	for _, c := range loginRR.Result().Cookies() {
		if c.Name == uiSessionCookie {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie after login")
	}

	streamCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	streamReq := httptest.NewRequest(http.MethodGet, "/app/events/dashboard-stats", nil).WithContext(streamCtx)
	streamReq.AddCookie(sessionCookie)
	streamRR := httptest.NewRecorder()
	h.ServeHTTP(streamRR, streamReq)

	if ct := streamRR.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected SSE content type, got %q", ct)
	}
	body := streamRR.Body.String()
	if !strings.Contains(body, "event: dashboard-stats") {
		t.Fatalf("expected dashboard-stats event in stream, got %q", body)
	}
	if !strings.Contains(body, "P95 latency") || !strings.Contains(body, "4xx requests") {
		t.Fatalf("expected stats fragment in SSE payload, got %q", body)
	}
}

func TestBucketTableSSEStreamsHTMXEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mstore, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:" + filepath.Join(root, "meta.db")})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	bstore, err := blob.NewStore(blob.Config{RootDir: root, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	if err := mstore.CreateBucket(ctx, metadata.Bucket{Name: "live-bucket", CreatedAt: time.Now().UTC(), Region: "us-east-1", VersioningStatus: "Suspended"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}

	h := NewHandler(Options{Metadata: mstore, Blob: bstore, UIAccessKey: "admin", UISecretKey: "secret"})

	loginForm := url.Values{"access_key": {"admin"}, "secret_key": {"secret"}}
	loginReq := httptest.NewRequest(http.MethodPost, "/app/login", strings.NewReader(loginForm.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRR := httptest.NewRecorder()
	h.ServeHTTP(loginRR, loginReq)

	var sessionCookie *http.Cookie
	for _, c := range loginRR.Result().Cookies() {
		if c.Name == uiSessionCookie {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie after login")
	}

	streamCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	streamReq := httptest.NewRequest(http.MethodGet, "/app/events/buckets", nil).WithContext(streamCtx)
	streamReq.AddCookie(sessionCookie)
	streamRR := httptest.NewRecorder()
	h.ServeHTTP(streamRR, streamReq)

	if ct := streamRR.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected SSE content type, got %q", ct)
	}
	body := streamRR.Body.String()
	if !strings.Contains(body, "event: buckets-updated") {
		t.Fatalf("expected buckets-updated event in stream, got %q", body)
	}
	if !strings.Contains(body, "live-bucket") {
		t.Fatalf("expected bucket table payload in stream, got %q", body)
	}
}

func TestHealthAndReadyDependencyChecks(t *testing.T) {
	t.Parallel()

	h := NewHandler(Options{Verifier: allowAllVerifier{}})
	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected %s to return %d, got %d", path, http.StatusServiceUnavailable, rr.Code)
		}
	}
}

type allowAllVerifier struct{}

func (allowAllVerifier) VerifyRequest(*http.Request) error {
	return nil
}

type denyMissingAuthVerifier struct{}

func (denyMissingAuthVerifier) VerifyRequest(*http.Request) error {
	return auth.ErrMissingAuthenticationToken
}
