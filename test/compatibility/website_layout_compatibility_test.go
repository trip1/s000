package compatibility_test

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

func TestWebsiteLayoutCompatibilityDocumentationSite(t *testing.T) {
	t.Parallel()

	h, _ := newWebsiteCompatFixture(t, metadata.BucketWebsiteConfig{
		Bucket:        "site",
		IndexDocument: "index.html",
		ErrorDocument: "error.html",
		Enabled:       true,
		PublicRead:    true,
	})

	seedCompatObject(t, h.ctx, h.store, h.blob, "site", "index.html", "<h1>home</h1>", "text/html; charset=utf-8", h.now)
	seedCompatObject(t, h.ctx, h.store, h.blob, "site", "docs/index.html", "<h1>docs</h1>", "text/html; charset=utf-8", h.now)
	seedCompatObject(t, h.ctx, h.store, h.blob, "site", "docs/getting-started.html", "<h1>getting started</h1>", "text/html; charset=utf-8", h.now)
	seedCompatObject(t, h.ctx, h.store, h.blob, "site", "assets/app.js", "console.log('ok')", "application/javascript", h.now)
	seedCompatObject(t, h.ctx, h.store, h.blob, "site", "error.html", "<h1>error</h1>", "text/html; charset=utf-8", h.now)

	assertCompatResponse(t, websiteCompatRequest(t, h.handler, http.MethodGet, "/", "site.website.local"), http.StatusOK, "<h1>home</h1>")
	assertCompatResponse(t, websiteCompatRequest(t, h.handler, http.MethodGet, "/docs/", "site.website.local"), http.StatusOK, "<h1>docs</h1>")
	assertCompatResponse(t, websiteCompatRequest(t, h.handler, http.MethodGet, "/docs/getting-started.html", "site.website.local"), http.StatusOK, "<h1>getting started</h1>")

	asset := websiteCompatRequest(t, h.handler, http.MethodGet, "/assets/app.js", "site.website.local")
	assertCompatResponse(t, asset, http.StatusOK, "console.log('ok')")
	if got := asset.Header.Get("Content-Type"); got != "application/javascript" {
		t.Fatalf("expected javascript content-type, got %q", got)
	}
}

func TestWebsiteLayoutCompatibilitySPAFallbackPattern(t *testing.T) {
	t.Parallel()

	h, _ := newWebsiteCompatFixture(t, metadata.BucketWebsiteConfig{
		Bucket:        "site",
		IndexDocument: "index.html",
		ErrorDocument: "index.html",
		Enabled:       true,
		PublicRead:    true,
	})

	seedCompatObject(t, h.ctx, h.store, h.blob, "site", "index.html", "<h1>spa shell</h1>", "text/html; charset=utf-8", h.now)

	missingRoute := websiteCompatRequest(t, h.handler, http.MethodGet, "/dashboard/settings", "site.website.local")
	assertCompatResponse(t, missingRoute, http.StatusNotFound, "<h1>spa shell</h1>")
	if got := missingRoute.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("expected html content-type for spa shell fallback, got %q", got)
	}
}

type websiteCompatHarness struct {
	handler http.Handler
	store   metadata.Store
	blob    *blob.Store
	ctx     context.Context
	now     time.Time
}

func newWebsiteCompatFixture(t *testing.T, cfg metadata.BucketWebsiteConfig) (websiteCompatHarness, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)

	bstore, err := blob.NewStore(blob.Config{RootDir: root, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	mstore, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:" + filepath.Join(root, "meta.db")})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	if err := mstore.CreateBucket(ctx, metadata.Bucket{Name: "site", CreatedAt: now, VersioningStatus: "Suspended", Region: "us-east-1"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}
	if err := mstore.PutBucketWebsite(ctx, cfg); err != nil {
		t.Fatalf("put website config failed: %v", err)
	}

	return websiteCompatHarness{
		handler: server.NewWebsiteHandler(mstore, bstore, "website.local"),
		store:   mstore,
		blob:    bstore,
		ctx:     ctx,
		now:     now,
	}, root
}

func seedCompatObject(t *testing.T, ctx context.Context, mstore metadata.Store, bstore *blob.Store, bucket, key, payload, contentType string, createdAt time.Time) {
	t.Helper()
	meta, err := bstore.WriteObject(ctx, blob.ObjectRef{Bucket: bucket, Key: key, VersionID: "null"}, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("write website object failed: %v", err)
	}
	err = mstore.PutObjectVersion(ctx, metadata.ObjectVersion{
		Bucket:         bucket,
		Key:            key,
		VersionID:      "null",
		Size:           meta.Size,
		ETag:           meta.MD5Hex,
		ChecksumSHA256: meta.SHA256,
		StoragePath:    meta.Path,
		Metadata:       map[string]string{"content-type": contentType},
		CreatedAt:      createdAt,
	})
	if err != nil {
		t.Fatalf("put website object metadata failed: %v", err)
	}
}

func websiteCompatRequest(t *testing.T, h http.Handler, method, target, host string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if host != "" {
		req.Host = host
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Result()
}

func assertCompatResponse(t *testing.T, resp *http.Response, wantStatus int, wantBody string) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != wantStatus {
		t.Fatalf("expected status %d, got %d", wantStatus, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body failed: %v", err)
	}
	if got := string(body); got != wantBody {
		t.Fatalf("expected response body %q, got %q", wantBody, got)
	}
}
