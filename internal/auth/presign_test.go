package auth

import (
	"net/http"
	"testing"
	"time"
)

func TestPresignRequestRoundTripVerifier(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	verifier, _ := testVerifier(t, now)

	req, err := http.NewRequest(http.MethodDelete, "https://s000.local/photos/album/a.txt", nil)
	if err != nil {
		t.Fatalf("request creation failed: %v", err)
	}

	err = PresignRequest(req, "AKIDEXAMPLE", "very-secret", PresignOptions{
		Now:     func() time.Time { return now },
		Region:  "us-east-1",
		Service: "s3",
		Expires: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("presign request failed: %v", err)
	}

	if err := verifier.VerifyRequest(req); err != nil {
		t.Fatalf("expected valid presigned request, got %v", err)
	}
}

func TestPresignRequestRejectsInvalidExpiry(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequest(http.MethodGet, "https://s000.local/photos/album/a.txt", nil)
	if err != nil {
		t.Fatalf("request creation failed: %v", err)
	}

	err = PresignRequest(req, "AKIDEXAMPLE", "very-secret", PresignOptions{Expires: 8 * 24 * time.Hour})
	if err == nil {
		t.Fatal("expected expiry validation failure")
	}
}
