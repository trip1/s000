package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
)

func TestHealthz(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	h := testHealthyHandler(t)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if got := rr.Body.String(); got != "ok\n" {
		t.Fatalf("expected body %q, got %q", "ok\\n", got)
	}
}

func TestReadyz(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()

	h := testHealthyHandler(t)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	if got := rr.Body.String(); got != "ready\n" {
		t.Fatalf("expected body %q, got %q", "ready\\n", got)
	}
}

func testHealthyHandler(t *testing.T) http.Handler {
	t.Helper()

	store, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:test.db"})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	bstore, err := blob.NewStore(blob.Config{RootDir: t.TempDir(), FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	return NewHandler(Options{
		Metadata:     store,
		Blob:         bstore,
		ReadyCheck:   func(context.Context) error { return nil },
		BucketRegion: "us-east-1",
		Verifier:     allowAllVerifier{},
	})
}
