package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
)

func TestWebsiteHandlerIndexAndErrorDocument(t *testing.T) {
	t.Parallel()

	h, _ := newWebsiteFixture(t)

	rootReq := httptest.NewRequest(http.MethodGet, "http://site.website.local/", nil)
	rootReq.Host = "site.website.local"
	rootRR := httptest.NewRecorder()
	h.ServeHTTP(rootRR, rootReq)
	if rootRR.Code != http.StatusOK {
		t.Fatalf("expected root status 200, got %d", rootRR.Code)
	}
	if body := rootRR.Body.String(); body != "<h1>home</h1>" {
		t.Fatalf("expected website index body, got %q", body)
	}

	notFoundReq := httptest.NewRequest(http.MethodGet, "http://site.website.local/missing", nil)
	notFoundReq.Host = "site.website.local"
	notFoundRR := httptest.NewRecorder()
	h.ServeHTTP(notFoundRR, notFoundReq)
	if notFoundRR.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing key, got %d", notFoundRR.Code)
	}
	if body := notFoundRR.Body.String(); body != "<h1>error</h1>" {
		t.Fatalf("expected error document body, got %q", body)
	}
}

func TestWebsiteHandlerPathStyleFallback(t *testing.T) {
	t.Parallel()

	h, _ := newWebsiteFixture(t)

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9001/site/dir/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected path-style website status 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "<h1>dir</h1>" {
		t.Fatalf("expected directory index body, got %q", body)
	}
}

func TestWebsiteHandlerRedirectsDirectoryPathToTrailingSlash(t *testing.T) {
	t.Parallel()

	h, _ := newWebsiteFixture(t)

	req := httptest.NewRequest(http.MethodGet, "http://site.website.local/dir?ref=nav", nil)
	req.Host = "site.website.local"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("expected directory redirect status 302, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/dir/?ref=nav" {
		t.Fatalf("unexpected redirect location %q", loc)
	}
}

func TestWebsiteHandlerDoesNotRedirectExistingObjectWithoutSlash(t *testing.T) {
	t.Parallel()

	h, store := newWebsiteFixture(t)
	seedWebsiteObject(t, context.Background(), store, h.(*WebsiteHandler).blob, "site", "docs", "plain object", time.Now().UTC())

	req := httptest.NewRequest(http.MethodGet, "http://site.website.local/docs", nil)
	req.Host = "site.website.local"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected existing object status 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "plain object" {
		t.Fatalf("expected existing object body, got %q", body)
	}
}

func TestWebsiteHandlerRedirectAllRequests(t *testing.T) {
	t.Parallel()

	_, store := newWebsiteFixture(t)
	if err := store.PutBucketWebsite(context.Background(), metadata.BucketWebsiteConfig{Bucket: "site", RedirectAllHost: "example.com", RedirectAllProtocol: "https", Enabled: true, PublicRead: true}); err != nil {
		t.Fatalf("put redirect website config failed: %v", err)
	}

	h := NewWebsiteHandler(store, mustBlob(t), "website.local")
	req := httptest.NewRequest(http.MethodGet, "http://site.website.local/path/file.html?x=1", nil)
	req.Host = "site.website.local"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("expected redirect status 301, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "https://example.com/path/file.html") {
		t.Fatalf("unexpected redirect location %q", loc)
	}
}

func TestWebsiteHandlerRoutingRulePrefixRedirect(t *testing.T) {
	t.Parallel()

	_, store := newWebsiteFixture(t)
	if err := store.PutBucketWebsite(context.Background(), metadata.BucketWebsiteConfig{
		Bucket:        "site",
		IndexDocument: "index.html",
		Enabled:       true,
		PublicRead:    true,
		RoutingRules: []metadata.BucketWebsiteRoutingRule{{
			Condition: metadata.BucketWebsiteRoutingCondition{KeyPrefixEquals: "docs/"},
			Redirect:  metadata.BucketWebsiteRedirect{ReplaceKeyPrefixWith: "documents/", HTTPRedirectCode: "302"},
		}},
	}); err != nil {
		t.Fatalf("put website config with routing rules failed: %v", err)
	}

	h := NewWebsiteHandler(store, mustBlob(t), "website.local")
	req := httptest.NewRequest(http.MethodGet, "http://site.website.local/docs/setup.html?lang=en", nil)
	req.Host = "site.website.local"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("expected redirect status 302, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.Contains(loc, "/documents/setup.html?lang=en") {
		t.Fatalf("unexpected redirect location %q", loc)
	}
}

func TestWebsiteHandlerRoutingRule404Redirect(t *testing.T) {
	t.Parallel()

	_, store := newWebsiteFixture(t)
	if err := store.PutBucketWebsite(context.Background(), metadata.BucketWebsiteConfig{
		Bucket:        "site",
		IndexDocument: "index.html",
		Enabled:       true,
		PublicRead:    true,
		RoutingRules: []metadata.BucketWebsiteRoutingRule{{
			Condition: metadata.BucketWebsiteRoutingCondition{HttpErrorCodeReturnedEquals: "404"},
			Redirect:  metadata.BucketWebsiteRedirect{HostName: "example.com", Protocol: "https", ReplaceKeyWith: "not-found.html"},
		}},
	}); err != nil {
		t.Fatalf("put website config with 404 routing rule failed: %v", err)
	}

	h := NewWebsiteHandler(store, mustBlob(t), "website.local")
	req := httptest.NewRequest(http.MethodGet, "http://site.website.local/missing?from=app", nil)
	req.Host = "site.website.local"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMovedPermanently {
		t.Fatalf("expected redirect status 301, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "https://example.com/not-found.html?from=app" {
		t.Fatalf("unexpected redirect location %q", loc)
	}
}

func newWebsiteFixture(t *testing.T) (http.Handler, metadata.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	bstore, err := blob.NewStore(blob.Config{RootDir: root, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	store, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:" + filepath.Join(root, "meta.db")})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	if err := store.CreateBucket(ctx, metadata.Bucket{Name: "site", CreatedAt: now, VersioningStatus: "Suspended", Region: "us-east-1"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}
	if err := store.PutBucketWebsite(ctx, metadata.BucketWebsiteConfig{Bucket: "site", IndexDocument: "index.html", ErrorDocument: "error.html", Enabled: true, PublicRead: true}); err != nil {
		t.Fatalf("put website config failed: %v", err)
	}
	seedWebsiteObject(t, ctx, store, bstore, "site", "index.html", "<h1>home</h1>", now)
	seedWebsiteObject(t, ctx, store, bstore, "site", "error.html", "<h1>error</h1>", now)
	seedWebsiteObject(t, ctx, store, bstore, "site", "dir/index.html", "<h1>dir</h1>", now)
	return NewWebsiteHandler(store, bstore, "website.local"), store
}

func seedWebsiteObject(t *testing.T, ctx context.Context, store metadata.Store, bstore *blob.Store, bucket, key, payload string, createdAt time.Time) {
	t.Helper()
	ref := blob.ObjectRef{Bucket: bucket, Key: key, VersionID: "null"}
	meta, err := bstore.WriteObject(ctx, ref, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("write website object failed: %v", err)
	}
	if err := store.PutObjectVersion(ctx, metadata.ObjectVersion{Bucket: bucket, Key: key, VersionID: "null", Size: meta.Size, ETag: meta.MD5Hex, ChecksumSHA256: meta.SHA256, StoragePath: meta.Path, Metadata: map[string]string{"content-type": "text/html; charset=utf-8"}, CreatedAt: createdAt}); err != nil {
		t.Fatalf("put website object metadata failed: %v", err)
	}
}

func mustBlob(t *testing.T) *blob.Store {
	t.Helper()
	bstore, err := blob.NewStore(blob.Config{RootDir: t.TempDir(), FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	return bstore
}
