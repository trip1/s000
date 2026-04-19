package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ds9labs.com/s000/internal/auth"
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

func TestAuthGateAllowsPersonalAccessToken(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	token, err := auth.CreatePersonalAccessToken("cli-user", []byte("signing-key"), now, time.Hour)
	if err != nil {
		t.Fatalf("create personal access token failed: %v", err)
	}

	h := withAuthGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, PrincipalFromContext(r.Context()))
	}), Options{
		Verifier:          denyMissingAuthVerifier{},
		PATSigningKey:     "signing-key",
		AuthFailThreshold: 5,
		AuthFailWindow:    time.Minute,
		AuthBlockDuration: time.Minute,
	})

	req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if rr.Body.String() != "cli-user" {
		t.Fatalf("expected principal cli-user, got %q", rr.Body.String())
	}
}

func TestAuthGateRejectsInvalidPersonalAccessToken(t *testing.T) {
	t.Parallel()

	h := withAuthGate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("expected auth gate to reject invalid token")
	}), Options{
		Verifier:          allowAllVerifier{},
		PATSigningKey:     "signing-key",
		AuthFailThreshold: 5,
		AuthFailWindow:    time.Minute,
		AuthBlockDuration: time.Minute,
	})

	req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rr.Code)
	}
}

func TestAuthGateRejectsRevokedPersonalAccessToken(t *testing.T) {
	t.Parallel()

	manager := auth.NewPersonalAccessTokenManager([]byte("signing-key"), time.Now)
	token, issued, err := manager.Issue("cli-user", time.Hour, "test")
	if err != nil {
		t.Fatalf("issue token failed: %v", err)
	}
	if err := manager.Revoke(issued.ID); err != nil {
		t.Fatalf("revoke token failed: %v", err)
	}

	h := withAuthGate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("expected auth gate to reject revoked token")
	}), Options{
		Verifier:          allowAllVerifier{},
		PATSigningKey:     "signing-key",
		PATManager:        manager,
		AuthFailThreshold: 5,
		AuthFailWindow:    time.Minute,
		AuthBlockDuration: time.Minute,
	})

	req := httptest.NewRequest(http.MethodGet, "/bucket/key", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rr.Code)
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
