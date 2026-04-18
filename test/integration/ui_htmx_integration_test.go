//go:build integration

package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
	"ds9labs.com/s000/internal/server"
)

func TestHTMXUIFlowLoginAndPartials(t *testing.T) {
	t.Parallel()

	bstore, mstore := newUIStores(t)
	h := server.NewHandler(server.Options{Metadata: mstore, Blob: bstore, UIAccessKey: "admin", UISecretKey: "secret"})
	ts := httptest.NewServer(h)
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	loginForm := url.Values{"access_key": {"admin"}, "secret_key": {"secret"}}
	loginResp, err := client.PostForm(ts.URL+"/app/login", loginForm)
	if err != nil {
		t.Fatalf("login request failed: %v", err)
	}
	_ = loginResp.Body.Close()

	bucketsResp, err := client.Get(ts.URL + "/app/buckets")
	if err != nil {
		t.Fatalf("get buckets page failed: %v", err)
	}
	bucketsPage, _ := io.ReadAll(bucketsResp.Body)
	_ = bucketsResp.Body.Close()
	csrf := extractToken(string(bucketsPage))
	if csrf == "" {
		t.Fatalf("failed to find csrf token in buckets page: %s", string(bucketsPage))
	}

	createReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/app/actions/create-bucket", strings.NewReader(url.Values{"bucket": {"htmx-bucket"}}.Encode()))
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createReq.Header.Set("HX-Request", "true")
	createReq.Header.Set("X-CSRF-Token", csrf)
	createResp, err := client.Do(createReq)
	if err != nil {
		t.Fatalf("create bucket action failed: %v", err)
	}
	_ = createResp.Body.Close()

	partialResp, err := client.Get(ts.URL + "/app/partials/buckets")
	if err != nil {
		t.Fatalf("get bucket partial failed: %v", err)
	}
	body, _ := io.ReadAll(partialResp.Body)
	_ = partialResp.Body.Close()
	if partialResp.StatusCode != http.StatusOK {
		t.Fatalf("expected partial status 200, got %d", partialResp.StatusCode)
	}
	if !strings.Contains(string(body), "htmx-bucket") {
		t.Fatalf("expected htmx-created bucket in partial output, got %q", string(body))
	}
}

func newUIStores(t *testing.T) (*blob.Store, metadata.Store) {
	t.Helper()
	root := t.TempDir()
	bstore, err := blob.NewStore(blob.Config{RootDir: root, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	mstore, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:" + filepath.Join(root, "meta.db")})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	if err := mstore.CreateBucket(context.Background(), metadata.Bucket{Name: "photos", CreatedAt: time.Now().UTC(), VersioningStatus: "Suspended", Region: "us-east-1"}); err != nil {
		t.Fatalf("seed bucket failed: %v", err)
	}
	return bstore, mstore
}

func extractToken(page string) string {
	needle := "name=\"_csrf\" value=\""
	i := strings.Index(page, needle)
	if i < 0 {
		return ""
	}
	start := i + len(needle)
	end := strings.Index(page[start:], "\"")
	if end < 0 {
		return ""
	}
	return page[start : start+end]
}
