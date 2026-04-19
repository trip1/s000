package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"ds9labs.com/s000/internal/auth"
	"ds9labs.com/s000/internal/observability"
)

func withMiddleware(next http.Handler, opts Options) http.Handler {
	h := next
	h = withRateLimit(h, opts)
	h = withAuthGate(h, opts)
	h = withRequestID(h)
	h = withTracing(h, opts)
	h = withRecovery(h)
	h = withRequestLog(h, opts)
	return h
}

func withRequestLog(next http.Handler, opts Options) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		bucket, key, ok := bucketAndKey(r, opts.Domain)
		if !ok {
			bucket = ""
			key = ""
		}
		bytesIn := r.ContentLength
		if bytesIn < 0 {
			bytesIn = 0
		}
		latency := time.Since(start)
		if opts.Metrics != nil {
			opts.Metrics.ObserveRequest(rw.status, latency, bytesIn, rw.bytesOut)
		}
		requestID := rw.Header().Get("X-Amz-Request-Id")
		if requestID == "" {
			requestID = RequestIDFromContext(r.Context())
		}
		slog.Info("request complete",
			"request_id", requestID,
			"principal", extractPrincipalID(r),
			"bucket", bucket,
			"key", key,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", latency.Milliseconds(),
			"bytes_in", bytesIn,
			"bytes_out", rw.bytesOut,
		)
	})
}

func withTracing(next http.Handler, opts Options) http.Handler {
	if !opts.TracingOn {
		return next
	}
	hooks := opts.Tracing
	if hooks == nil {
		hooks = observability.NoopTraceHooks()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, finish := hooks.Start(r.Context(), r.Method+" "+r.URL.Path)
		defer finish()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered", "request_id", RequestIDFromContext(r.Context()), "panic", redactPanicValue(rec))
				writeS3Error(w, r, s3ErrorSpec{
					StatusCode: http.StatusInternalServerError,
					Code:       "InternalError",
					Message:    "We encountered an internal error. Please try again.",
					Resource:   r.URL.Path,
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func withAuthGate(next http.Handler, opts Options) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shouldBypassAuth(r.URL.Path, opts.MetricsPath) {
			next.ServeHTTP(w, r)
			return
		}

		identity := clientIdentityKey(r)
		if protector := opts.AuthFailureProtector; protector != nil && protector.IsBlocked(identity, time.Now().UTC()) {
			emitAuditEvent(opts, AuditEvent{
				Action:    "auth.blocked",
				Outcome:   "deny",
				RequestID: RequestIDFromContext(r.Context()),
				Principal: extractPrincipalID(r),
				Reason:    "too_many_auth_failures",
			})
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusServiceUnavailable, Code: "SlowDown", Message: "Too many authentication failures. Retry later.", Resource: r.URL.Path})
			return
		}

		verifier := opts.Verifier
		if verifier == nil {
			writeS3Error(w, r, s3ErrorSpec{
				StatusCode: http.StatusForbidden,
				Code:       "AccessDenied",
				Message:    "Access Denied",
				Resource:   r.URL.Path,
			})
			return
		}

		if err := verifier.VerifyRequest(r); err != nil {
			if protector := opts.AuthFailureProtector; protector != nil {
				protector.RecordFailure(identity, time.Now().UTC())
			}
			spec := mapAuthErrorToS3(r, err)
			emitAuditEvent(opts, AuditEvent{
				Action:    "auth.failure",
				Outcome:   "deny",
				RequestID: RequestIDFromContext(r.Context()),
				Principal: extractPrincipalID(r),
				Reason:    spec.Code,
			})
			writeS3Error(w, r, spec)
			return
		}
		if protector := opts.AuthFailureProtector; protector != nil {
			protector.RecordSuccess(identity)
		}
		r = r.WithContext(withPrincipalContext(r.Context(), extractPrincipalID(r)))

		next.ServeHTTP(w, r)
	})
}

func mapAuthErrorToS3(r *http.Request, err error) s3ErrorSpec {
	spec := s3ErrorSpec{
		StatusCode: http.StatusForbidden,
		Code:       "AccessDenied",
		Message:    "Access Denied",
		Resource:   r.URL.Path,
	}

	switch {
	case errors.Is(err, auth.ErrMissingAuthenticationToken):
		spec.Code = "MissingAuthenticationToken"
		spec.Message = "Request is missing Authentication Token"
	case errors.Is(err, auth.ErrInvalidAccessKeyID):
		spec.Code = "InvalidAccessKeyId"
		spec.Message = "The AWS Access Key Id you provided does not exist in our records."
	case errors.Is(err, auth.ErrSignatureDoesNotMatch):
		spec.Code = "SignatureDoesNotMatch"
		spec.Message = "The request signature we calculated does not match the signature you provided."
	case errors.Is(err, auth.ErrRequestTimeTooSkewed):
		spec.Code = "RequestTimeTooSkewed"
		spec.Message = "The difference between the request time and the current time is too large."
	case errors.Is(err, auth.ErrInvalidRequest):
		spec.Code = "InvalidRequest"
		spec.Message = "The request is invalid."
	}

	return spec
}

func withRateLimit(next http.Handler, opts Options) http.Handler {
	maxInFlight := opts.MaxInFlight
	sem := make(chan struct{}, maxInFlight)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shouldBypassGuards(r.URL.Path, opts.MetricsPath) {
			next.ServeHTTP(w, r)
			return
		}

		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
			next.ServeHTTP(w, r)
		default:
			writeS3Error(w, r, s3ErrorSpec{
				StatusCode: http.StatusServiceUnavailable,
				Code:       "SlowDown",
				Message:    "Please reduce your request rate.",
				Resource:   r.URL.Path,
			})
		}
	})
}

var requestCounter uint64

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Amz-Request-Id")
		if requestID == "" {
			requestID = newRequestID()
		}

		ctx := withRequestIDContext(r.Context(), requestID)
		if trace, ok := observability.ParseTraceparentHeader(r.Header.Get("Traceparent")); ok {
			ctx = observability.WithTraceContext(ctx, trace)
		}
		r = r.WithContext(ctx)
		w.Header().Set("X-Amz-Request-Id", requestID)
		next.ServeHTTP(w, r)
	})
}

func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err == nil {
		return strings.ToUpper(hex.EncodeToString(b))
	}
	n := atomic.AddUint64(&requestCounter, 1)
	return strings.ToUpper(hex.EncodeToString([]byte{
		byte(n >> 56),
		byte(n >> 48),
		byte(n >> 40),
		byte(n >> 32),
		byte(n >> 24),
		byte(n >> 16),
		byte(n >> 8),
		byte(n),
	}))
}

type statusRecorder struct {
	http.ResponseWriter
	status   int
	bytesOut int64
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytesOut += int64(n)
	return n, err
}

func shouldBypassGuards(path string, metricsPath string) bool {
	if metricsPath == "" {
		metricsPath = "/metrics"
	}
	if path == "/healthz" || path == "/readyz" || path == metricsPath {
		return true
	}
	if strings.HasPrefix(path, "/app") || strings.HasPrefix(path, "/assets/") {
		return true
	}
	return false
}

func shouldBypassAuth(path string, metricsPath string) bool {
	if shouldBypassGuards(path, metricsPath) {
		return true
	}
	return false
}

func extractPrincipalID(r *http.Request) string {
	authz := r.Header.Get("Authorization")
	if i := strings.Index(authz, "Credential="); i >= 0 {
		value := authz[i+len("Credential="):]
		if j := strings.Index(value, ","); j >= 0 {
			value = value[:j]
		}
		parts := strings.SplitN(value, "/", 2)
		if len(parts) > 0 && parts[0] != "" {
			return strings.TrimSpace(parts[0])
		}
	}
	if credential := r.URL.Query().Get("X-Amz-Credential"); credential != "" {
		parts := strings.SplitN(credential, "/", 2)
		if len(parts) > 0 && parts[0] != "" {
			return strings.TrimSpace(parts[0])
		}
	}
	return ""
}
