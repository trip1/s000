package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"ds9labs.com/s000/internal/auth"
	"ds9labs.com/s000/internal/metadata"
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

		if token, ok := bearerToken(r.Header.Get("Authorization")); ok {
			var (
				subject string
				err     error
			)
			if opts.PATManager != nil {
				subject, err = opts.PATManager.Verify(token)
			} else {
				subject, err = auth.VerifyPersonalAccessToken(token, []byte(opts.PATSigningKey), time.Now().UTC())
			}
			if err != nil {
				if protector := opts.AuthFailureProtector; protector != nil {
					protector.RecordFailure(identity, time.Now().UTC())
				}
				emitAuditEvent(opts, AuditEvent{
					Action:    "auth.failure",
					Outcome:   "deny",
					RequestID: RequestIDFromContext(r.Context()),
					Principal: extractPrincipalID(r),
					Reason:    "AccessDenied",
				})
				writeS3Error(w, r, s3ErrorSpec{
					StatusCode: http.StatusForbidden,
					Code:       "AccessDenied",
					Message:    "Access Denied",
					Resource:   r.URL.Path,
				})
				return
			}
			if protector := opts.AuthFailureProtector; protector != nil {
				protector.RecordSuccess(identity)
			}
			r = r.WithContext(withPrincipalContext(r.Context(), subject))
			next.ServeHTTP(w, r)
			return
		}

		if !hasAuthMaterial(r) && anonymousPolicyAllows(r, opts) {
			r = r.WithContext(withPrincipalContext(r.Context(), "anonymous"))
			next.ServeHTTP(w, r)
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

func hasAuthMaterial(r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("Authorization")) != "" {
		return true
	}
	q := r.URL.Query()
	return q.Get("X-Amz-Signature") != "" || q.Get("X-Amz-Credential") != "" || q.Get("X-Amz-Algorithm") != ""
}

func anonymousPolicyAllows(r *http.Request, opts Options) bool {
	if opts.Metadata == nil {
		return false
	}
	bucket, key, ok := bucketAndKey(r, opts.Domain)
	if !ok || bucket == "" {
		return false
	}
	action, resource, ok := anonymousPolicyTarget(r, bucket, key)
	if !ok {
		return false
	}
	if block, err := opts.Metadata.GetBucketPublicAccessBlock(r.Context(), bucket); err == nil {
		if block.BlockPublicPolicy || block.RestrictPublicBuckets {
			return false
		}
	} else if err != nil && !errors.Is(err, metadata.ErrNotFound) {
		return false
	}
	policy, err := opts.Metadata.GetBucketPolicy(r.Context(), bucket)
	if err != nil || !policy.Enabled || strings.TrimSpace(policy.Document) == "" {
		return false
	}
	return bucketPolicyAllowsAnonymous(policy.Document, action, resource)
}

func anonymousPolicyTarget(r *http.Request, bucket, key string) (action, resource string, ok bool) {
	if key == "" {
		if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
			return "s3:ListBucket", "arn:aws:s3:::" + bucket, true
		}
		return "", "", false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return "", "", false
	}
	return "s3:GetObject", "arn:aws:s3:::" + bucket + "/" + key, true
}

func bucketPolicyAllowsAnonymous(document, action, resource string) bool {
	var policy bucketPolicyDocument
	if err := json.Unmarshal([]byte(document), &policy); err != nil {
		return false
	}
	allowed := false
	for _, stmt := range policy.Statements {
		if !stmt.matchesAnonymous(action, resource) {
			continue
		}
		if strings.EqualFold(stmt.Effect, "Deny") {
			return false
		}
		if strings.EqualFold(stmt.Effect, "Allow") {
			allowed = true
		}
	}
	return allowed
}

type bucketPolicyDocument struct {
	Statements []bucketPolicyStatement `json:"Statement"`
}

func (d *bucketPolicyDocument) UnmarshalJSON(b []byte) error {
	type rawDocument struct {
		Statement json.RawMessage `json:"Statement"`
	}
	var raw rawDocument
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	statement := strings.TrimSpace(string(raw.Statement))
	if statement == "" {
		return nil
	}
	if statement[0] == '[' {
		return json.Unmarshal(raw.Statement, &d.Statements)
	}
	var stmt bucketPolicyStatement
	if err := json.Unmarshal(raw.Statement, &stmt); err != nil {
		return err
	}
	d.Statements = []bucketPolicyStatement{stmt}
	return nil
}

type bucketPolicyStatement struct {
	Effect    string             `json:"Effect"`
	Principal bucketPolicyValues `json:"Principal"`
	Action    bucketPolicyValues `json:"Action"`
	Resource  bucketPolicyValues `json:"Resource"`
}

func (s bucketPolicyStatement) matchesAnonymous(action, resource string) bool {
	return s.Principal.contains("*") && s.Action.matches(action) && s.Resource.matches(resource)
}

type bucketPolicyValues []string

func (v *bucketPolicyValues) UnmarshalJSON(b []byte) error {
	var single string
	if err := json.Unmarshal(b, &single); err == nil {
		*v = []string{single}
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err == nil {
		*v = list
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	for _, raw := range obj {
		var nested bucketPolicyValues
		if err := json.Unmarshal(raw, &nested); err == nil {
			*v = append(*v, nested...)
		}
	}
	return nil
}

func (v bucketPolicyValues) contains(want string) bool {
	for _, got := range v {
		if got == want {
			return true
		}
	}
	return false
}

func (v bucketPolicyValues) matches(want string) bool {
	for _, pattern := range v {
		if policyPatternMatches(pattern, want) {
			return true
		}
	}
	return false
}

func policyPatternMatches(pattern, value string) bool {
	if pattern == "*" || pattern == value {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(value, strings.TrimSuffix(pattern, "*"))
	}
	return false
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

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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
	if principal := PrincipalFromContext(r.Context()); principal != "" {
		return principal
	}
	if token, ok := bearerToken(r.Header.Get("Authorization")); ok {
		if subject := auth.PersonalAccessTokenSubject(token); subject != "" {
			return subject
		}
		return "pat"
	}
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

func bearerToken(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) < 7 {
		return "", false
	}
	if !strings.EqualFold(value[:7], "Bearer ") {
		return "", false
	}
	token := strings.TrimSpace(value[7:])
	if token == "" {
		return "", false
	}
	return token, true
}
