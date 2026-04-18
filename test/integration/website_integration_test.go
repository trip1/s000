//go:build integration

package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
	"ds9labs.com/s000/internal/server"
)

func TestWebsiteRoutingAndFallbacks(t *testing.T) {
	t.Parallel()

	baseURL, _, _ := newWebsiteIntegrationFixture(t)

	root := websiteRequest(t, baseURL+"/", "site.website.local")
	if root.StatusCode != http.StatusOK {
		t.Fatalf("expected root website status 200, got %d", root.StatusCode)
	}
	if body := readWebsiteBody(t, root); body != "<h1>home</h1>" {
		t.Fatalf("expected root website body, got %q", body)
	}

	pathStyle := websiteRequest(t, baseURL+"/site/docs/", "")
	if pathStyle.StatusCode != http.StatusOK {
		t.Fatalf("expected path-style website status 200, got %d", pathStyle.StatusCode)
	}
	if body := readWebsiteBody(t, pathStyle); body != "<h1>docs</h1>" {
		t.Fatalf("expected directory index body, got %q", body)
	}

	notFound := websiteRequest(t, baseURL+"/site/missing", "")
	if notFound.StatusCode != http.StatusNotFound {
		t.Fatalf("expected missing key status 404, got %d", notFound.StatusCode)
	}
	if body := readWebsiteBody(t, notFound); body != "<h1>error</h1>" {
		t.Fatalf("expected configured error document, got %q", body)
	}
}

func TestWebsiteRedirectAllRequests(t *testing.T) {
	t.Parallel()

	baseURL, mstore, _ := newWebsiteIntegrationFixture(t)
	if err := mstore.CreateBucket(context.Background(), metadata.Bucket{Name: "redir", CreatedAt: time.Now().UTC(), VersioningStatus: "Suspended", Region: "us-east-1"}); err != nil {
		t.Fatalf("create redirect bucket failed: %v", err)
	}
	if err := mstore.PutBucketWebsite(context.Background(), metadata.BucketWebsiteConfig{Bucket: "redir", RedirectAllHost: "example.com", RedirectAllProtocol: "https", Enabled: true, PublicRead: true}); err != nil {
		t.Fatalf("put redirect website config failed: %v", err)
	}

	resp := websiteRequest(t, baseURL+"/path/file.html?x=1", "redir.website.local")
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("expected redirect status 301, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "https://example.com/path/file.html?x=1" {
		t.Fatalf("unexpected redirect location %q", loc)
	}
}

func newWebsiteIntegrationFixture(t *testing.T) (string, metadata.Store, *blob.Store) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()

	bstore, err := blob.NewStore(blob.Config{RootDir: root, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	mstore, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:" + filepath.Join(root, "meta.db")})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}

	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	if err := mstore.CreateBucket(ctx, metadata.Bucket{Name: "site", CreatedAt: now, VersioningStatus: "Suspended", Region: "us-east-1"}); err != nil {
		t.Fatalf("create site bucket failed: %v", err)
	}
	if err := mstore.PutBucketWebsite(ctx, metadata.BucketWebsiteConfig{Bucket: "site", IndexDocument: "index.html", ErrorDocument: "error.html", Enabled: true, PublicRead: true}); err != nil {
		t.Fatalf("put website config failed: %v", err)
	}

	seedWebsiteObject(t, ctx, mstore, bstore, "site", "index.html", "<h1>home</h1>", now)
	seedWebsiteObject(t, ctx, mstore, bstore, "site", "docs/index.html", "<h1>docs</h1>", now)
	seedWebsiteObject(t, ctx, mstore, bstore, "site", "error.html", "<h1>error</h1>", now)

	ts := httptest.NewServer(server.NewWebsiteHandler(mstore, bstore, "website.local"))
	t.Cleanup(ts.Close)
	return ts.URL, mstore, bstore
}

func seedWebsiteObject(t *testing.T, ctx context.Context, mstore metadata.Store, bstore *blob.Store, bucket, key, payload string, createdAt time.Time) {
	t.Helper()
	meta, err := bstore.WriteObject(ctx, blob.ObjectRef{Bucket: bucket, Key: key, VersionID: "null"}, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("write website object failed: %v", err)
	}
	err = mstore.PutObjectVersion(ctx, metadata.ObjectVersion{Bucket: bucket, Key: key, VersionID: "null", Size: meta.Size, ETag: meta.MD5Hex, ChecksumSHA256: meta.SHA256, StoragePath: meta.Path, Metadata: map[string]string{"content-type": "text/html; charset=utf-8"}, CreatedAt: createdAt})
	if err != nil {
		t.Fatalf("put website object metadata failed: %v", err)
	}
}

func websiteRequest(t *testing.T, url string, host string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request failed: %v", err)
	}
	if host != "" {
		req.Host = host
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func readWebsiteBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body failed: %v", err)
	}
	return string(b)
}
