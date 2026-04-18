package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthFailureProtectorBlocksRepeatedFailures(t *testing.T) {
	t.Parallel()

	verifier := denyMissingAuthVerifier{}
	audit := &testAuditSink{}
	h := withAuthGate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), Options{
		Verifier:             verifier,
		AuthFailThreshold:    1,
		AuthFailWindow:       time.Minute,
		AuthBlockDuration:    time.Minute,
		AuditEnabled:         true,
		Audit:                audit,
		AuthFailureProtector: newAuthFailureProtector(1, time.Minute, time.Minute, nil),
	})

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/bucket/key", nil))
	if first.Code != http.StatusForbidden {
		t.Fatalf("expected first failure status %d, got %d", http.StatusForbidden, first.Code)
	}

	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/bucket/key", nil))
	if second.Code != http.StatusForbidden {
		t.Fatalf("expected second failure status %d, got %d", http.StatusForbidden, second.Code)
	}

	third := httptest.NewRecorder()
	h.ServeHTTP(third, httptest.NewRequest(http.MethodGet, "/bucket/key", nil))
	if third.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected blocked status %d, got %d", http.StatusServiceUnavailable, third.Code)
	}

	if len(audit.events) < 2 {
		t.Fatalf("expected audit events for failures, got %d", len(audit.events))
	}
}

func TestAuthGateFunctionsHTTPPublicBypass(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/fn/hello", nil)

	blocked := withAuthGate(next, Options{Verifier: denyMissingAuthVerifier{}})
	blockedRR := httptest.NewRecorder()
	blocked.ServeHTTP(blockedRR, req)
	if blockedRR.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden when public gateway disabled, got %d", blockedRR.Code)
	}

	public := withAuthGate(next, Options{Verifier: denyMissingAuthVerifier{}, FunctionsHTTPPublic: true})
	publicRR := httptest.NewRecorder()
	public.ServeHTTP(publicRR, req)
	if publicRR.Code != http.StatusNoContent {
		t.Fatalf("expected auth bypass for public gateway, got %d", publicRR.Code)
	}
}

func TestAuditEventsForDestructiveOperations(t *testing.T) {
	t.Parallel()

	audit := &testAuditSink{}
	h := newS3TestHandlerWithOptions(t, Options{AuditEnabled: true, Audit: audit})

	_ = execute(t, h, http.MethodPut, "/photos", "")
	_ = execute(t, h, http.MethodPut, "/photos/file.txt", "x")
	_ = execute(t, h, http.MethodDelete, "/photos/file.txt", "")

	foundDelete := false
	for _, e := range audit.events {
		if e.Action == "object.delete" {
			foundDelete = true
			break
		}
	}
	if !foundDelete {
		t.Fatal("expected object.delete audit event")
	}
}

func TestRedactSensitiveString(t *testing.T) {
	t.Parallel()

	in := "Authorization=AWS4-HMAC-SHA256 Credential=AKIA/20260101/us-east-1/s3/aws4_request, Signature=abc123 SECRET_KEY=topsecret"
	out := redactSensitiveString(in)
	if out == in {
		t.Fatal("expected input to be redacted")
	}
	if containsAny(out, []string{"abc123", "topsecret"}) {
		t.Fatalf("expected secrets removed from output, got %q", out)
	}
}

func TestRedactPanicValue(t *testing.T) {
	t.Parallel()

	err := errors.New("panic with secret=abc123")
	out := redactPanicValue(err)
	if containsAny(out, []string{"abc123"}) {
		t.Fatalf("expected panic value redacted, got %q", out)
	}
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if n != "" && len(s) >= len(n) && (s == n || contains(s, n)) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	return len(sub) <= len(s) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

type testAuditSink struct {
	events []AuditEvent
}

func (s *testAuditSink) Emit(event AuditEvent) {
	s.events = append(s.events, event)
}
