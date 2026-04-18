package auth

import (
	"testing"
	"time"
)

func TestBootstrapAdminCredential(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	store := NewCredentialStore(func() time.Time { return now })

	if err := store.BootstrapAdminCredential("admin", "secret"); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	if err := store.BootstrapAdminCredential("another", "secret2"); err == nil {
		t.Fatal("expected second bootstrap to fail")
	}

	if !store.VerifySecret("admin", "secret", now) {
		t.Fatal("expected initial admin secret to verify")
	}
}

func TestRotateCredentialOverlapWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	current := now
	store := NewCredentialStore(func() time.Time { return current })

	if err := store.CreateCredential("akid", "old-secret"); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	if err := store.RotateCredential("akid", "new-secret", 2*time.Minute); err != nil {
		t.Fatalf("rotate failed: %v", err)
	}

	if !store.VerifySecret("akid", "old-secret", current) {
		t.Fatal("expected previous secret to remain valid during overlap")
	}
	if !store.VerifySecret("akid", "new-secret", current) {
		t.Fatal("expected new secret to be valid")
	}

	current = current.Add(3 * time.Minute)
	if store.VerifySecret("akid", "old-secret", current) {
		t.Fatal("expected previous secret to expire after overlap")
	}
	if !store.VerifySecret("akid", "new-secret", current) {
		t.Fatal("expected new secret to remain valid")
	}
}

func TestDisabledCredentialCannotAuthenticate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	store := NewCredentialStore(func() time.Time { return now })

	if err := store.CreateCredential("akid", "secret"); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if err := store.SetStatus("akid", CredentialStatusDisabled); err != nil {
		t.Fatalf("disable failed: %v", err)
	}

	if store.VerifySecret("akid", "secret", now) {
		t.Fatal("expected disabled credential to fail verification")
	}
}
