package auth

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestVerifyAuthorizationHeaderIgnoresIncomingHeaderOrder(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	v, _ := testVerifier(t, now)

	req, err := http.NewRequest(http.MethodGet, "https://s000.local/photos/a.jpg?x=1&z=9", nil)
	if err != nil {
		t.Fatalf("request creation failed: %v", err)
	}
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	req.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
	req.Header.Set("X-Custom", "abc")

	signAuthorization(t, req, "AKIDEXAMPLE", "very-secret", now, "us-east-1", "s3", []string{"host", "x-amz-content-sha256", "x-amz-date", "x-custom"})

	if err := v.VerifyRequest(req); err != nil {
		t.Fatalf("expected valid authorization request, got %v", err)
	}
}

func TestVerifyPresignHandlesQueryOrdering(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	v, _ := testVerifier(t, now)

	req, err := http.NewRequest(http.MethodGet, "https://bucket.s000.local/photos/a.jpg?z=9&x=1", nil)
	if err != nil {
		t.Fatalf("request creation failed: %v", err)
	}

	signPresignedURL(t, req, "AKIDEXAMPLE", "very-secret", now, "us-east-1", "s3", 120)

	if err := v.VerifyRequest(req); err != nil {
		t.Fatalf("expected valid presigned request, got %v", err)
	}
}

func TestVerifyRejectsClockSkew(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	v, _ := testVerifier(t, now)

	req, err := http.NewRequest(http.MethodGet, "https://s000.local/bucket/key", nil)
	if err != nil {
		t.Fatalf("request creation failed: %v", err)
	}
	old := now.Add(-16 * time.Minute)
	req.Header.Set("X-Amz-Date", old.Format("20060102T150405Z"))
	req.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")

	signAuthorization(t, req, "AKIDEXAMPLE", "very-secret", old, "us-east-1", "s3", []string{"host", "x-amz-content-sha256", "x-amz-date"})

	err = v.VerifyRequest(req)
	if err == nil {
		t.Fatal("expected clock skew rejection")
	}
	if err != ErrRequestTimeTooSkewed {
		t.Fatalf("expected ErrRequestTimeTooSkewed, got %v", err)
	}
}

func TestVerifyPresignAllowsHeadRequests(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	v, _ := testVerifier(t, now)

	req, err := http.NewRequest(http.MethodHead, "https://s000.local/bucket/key", nil)
	if err != nil {
		t.Fatalf("request creation failed: %v", err)
	}

	signPresignedURL(t, req, "AKIDEXAMPLE", "very-secret", now, "us-east-1", "s3", 120)

	if err := v.VerifyRequest(req); err != nil {
		t.Fatalf("expected valid presigned HEAD request, got %v", err)
	}
}

func TestVerifyPresignRejectsExpiresOverSevenDays(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	v, _ := testVerifier(t, now)

	req, err := http.NewRequest(http.MethodGet, "https://s000.local/bucket/key", nil)
	if err != nil {
		t.Fatalf("request creation failed: %v", err)
	}

	signPresignedURL(t, req, "AKIDEXAMPLE", "very-secret", now, "us-east-1", "s3", 604801)

	err = v.VerifyRequest(req)
	if err == nil {
		t.Fatal("expected expiry validation error")
	}
	if err != ErrInvalidRequest {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func testVerifier(t *testing.T, now time.Time) (*Verifier, *CredentialStore) {
	t.Helper()

	store := NewCredentialStore(func() time.Time { return now })
	if err := store.CreateCredential("AKIDEXAMPLE", "very-secret"); err != nil {
		t.Fatalf("failed to create credential: %v", err)
	}

	verifier := NewVerifier(store, VerifierOptions{
		Now:      func() time.Time { return now },
		MaxSkew:  15 * time.Minute,
		Service:  "s3",
		Terminal: "aws4_request",
	})

	return verifier, store
}

func signAuthorization(t *testing.T, req *http.Request, accessKeyID string, secret string, now time.Time, region string, service string, signedHeaders []string) {
	t.Helper()

	date := now.UTC().Format("20060102")
	amzDate := now.UTC().Format("20060102T150405Z")

	canonicalHeaders := normalizedSignedHeaders(signedHeaders)
	canonicalRequest, err := buildCanonicalRequest(req, canonicalHeaders, false)
	if err != nil {
		t.Fatalf("canonical request failed: %v", err)
	}
	scope := date + "/" + region + "/" + service + "/aws4_request"
	requestTime, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil {
		t.Fatalf("request time parse failed: %v", err)
	}
	stringToSign := buildStringToSign(requestTime, scope, canonicalRequest)

	signature := signString(secret, date, region, service, "aws4_request", stringToSign)
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKeyID,
		scope,
		strings.Join(canonicalHeaders, ";"),
		signature,
	))
}

func signPresignedURL(t *testing.T, req *http.Request, accessKeyID string, secret string, now time.Time, region string, service string, expiresSeconds int) {
	t.Helper()

	date := now.UTC().Format("20060102")
	amzDate := now.UTC().Format("20060102T150405Z")
	scope := date + "/" + region + "/" + service + "/aws4_request"

	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", accessKeyID+"/"+scope)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", strconv.Itoa(expiresSeconds))
	q.Set("X-Amz-SignedHeaders", "host")
	q.Del("X-Amz-Signature")
	req.URL.RawQuery = q.Encode()

	canonicalRequest, err := buildCanonicalRequest(req, []string{"host"}, true)
	if err != nil {
		t.Fatalf("canonical request failed: %v", err)
	}
	requestTime, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil {
		t.Fatalf("request time parse failed: %v", err)
	}
	stringToSign := buildStringToSign(requestTime, scope, canonicalRequest)

	signature := signString(secret, date, region, service, "aws4_request", stringToSign)
	q = req.URL.Query()
	q.Set("X-Amz-Signature", signature)
	req.URL.RawQuery = q.Encode()
}

func normalizedSignedHeaders(headers []string) []string {
	out := append([]string(nil), headers...)
	for i := range out {
		out[i] = strings.ToLower(strings.TrimSpace(out[i]))
	}
	sort.Strings(out)
	return out
}
