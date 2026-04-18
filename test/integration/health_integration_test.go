//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
	"ds9labs.com/s000/internal/server"
)

func TestHealthEndpoint(t *testing.T) {
	t.Parallel()

	dataRoot := t.TempDir()
	bstore, err := blob.NewStore(blob.Config{RootDir: dataRoot, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	mstore, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:" + filepath.Join(dataRoot, "meta.db")})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}

	ts := httptest.NewServer(server.NewHandler(server.Options{
		Metadata: mstore,
		Blob:     bstore,
		ReadyCheck: func(context.Context) error {
			return nil
		},
		BucketRegion: "us-east-1",
		MaxInFlight:  16,
	}))
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	t.Cleanup(func() {
		_ = resp.Body.Close()
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	readyResp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz failed: %v", err)
	}
	t.Cleanup(func() { _ = readyResp.Body.Close() })
	if readyResp.StatusCode != http.StatusOK {
		t.Fatalf("expected ready status %d, got %d", http.StatusOK, readyResp.StatusCode)
	}
}
