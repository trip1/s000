package auth

import (
	"testing"
	"time"
)

func TestCreateAndVerifyPersonalAccessToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	token, err := CreatePersonalAccessToken("cli-user", []byte("signing-key"), now, time.Hour)
	if err != nil {
		t.Fatalf("create token failed: %v", err)
	}

	subject, err := VerifyPersonalAccessToken(token, []byte("signing-key"), now.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("verify token failed: %v", err)
	}
	if subject != "cli-user" {
		t.Fatalf("expected subject cli-user, got %q", subject)
	}
}

func TestVerifyPersonalAccessTokenRejectsInvalidSignature(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	token, err := CreatePersonalAccessToken("cli-user", []byte("signing-key"), now, time.Hour)
	if err != nil {
		t.Fatalf("create token failed: %v", err)
	}

	if _, err := VerifyPersonalAccessToken(token, []byte("wrong-key"), now.Add(5*time.Minute)); err != ErrInvalidPersonalAccessToken {
		t.Fatalf("expected invalid token error, got %v", err)
	}
}

func TestVerifyPersonalAccessTokenRejectsExpiredToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	token, err := CreatePersonalAccessToken("cli-user", []byte("signing-key"), now, 10*time.Minute)
	if err != nil {
		t.Fatalf("create token failed: %v", err)
	}

	if _, err := VerifyPersonalAccessToken(token, []byte("signing-key"), now.Add(11*time.Minute)); err != ErrExpiredPersonalAccessToken {
		t.Fatalf("expected expired token error, got %v", err)
	}
}

func TestPersonalAccessTokenSubject(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	token, err := CreatePersonalAccessToken("ops", []byte("signing-key"), now, time.Hour)
	if err != nil {
		t.Fatalf("create token failed: %v", err)
	}

	if got := PersonalAccessTokenSubject(token); got != "ops" {
		t.Fatalf("expected subject ops, got %q", got)
	}
	if got := PersonalAccessTokenSubject("bad-token"); got != "" {
		t.Fatalf("expected empty subject for invalid token, got %q", got)
	}
}
