//go:build integration

package integration_test

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"ds9labs.com/s000/internal/auth"
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

func TestHTMXUITokenManagementCreateAndRevoke(t *testing.T) {
	t.Parallel()

	bstore, mstore := newUIStores(t)
	pat := auth.NewPersonalAccessTokenManager([]byte("pat-signing-key"), nil)
	h := server.NewHandler(server.Options{
		Metadata:      mstore,
		Blob:          bstore,
		UIAccessKey:   "admin",
		UISecretKey:   "secret",
		PATSigningKey: "pat-signing-key",
		PATManager:    pat,
	})
	ts := httptest.NewServer(h)
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	loginResp, err := client.PostForm(ts.URL+"/app/login", url.Values{"access_key": {"admin"}, "secret_key": {"secret"}})
	if err != nil {
		t.Fatalf("login request failed: %v", err)
	}
	_ = loginResp.Body.Close()

	tokensResp, err := client.Get(ts.URL + "/app/tokens")
	if err != nil {
		t.Fatalf("get tokens page failed: %v", err)
	}
	tokensPage, _ := io.ReadAll(tokensResp.Body)
	_ = tokensResp.Body.Close()
	csrf := extractToken(string(tokensPage))
	if csrf == "" {
		t.Fatalf("failed to find csrf token in tokens page: %s", string(tokensPage))
	}

	createReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/app/actions/tokens/create", strings.NewReader(url.Values{"subject": {"htmx-cli"}, "ttl": {"1h"}, "label": {"integration"}}.Encode()))
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createReq.Header.Set("HX-Request", "true")
	createReq.Header.Set("X-CSRF-Token", csrf)
	createResp, err := client.Do(createReq)
	if err != nil {
		t.Fatalf("create token action failed: %v", err)
	}
	createBody, _ := io.ReadAll(createResp.Body)
	_ = createResp.Body.Close()
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("expected create token status 200, got %d body=%q", createResp.StatusCode, string(createBody))
	}
	if trigger := createResp.Header.Get("HX-Trigger"); trigger != "tokens-changed" {
		t.Fatalf("expected HX-Trigger tokens-changed, got %q", trigger)
	}
	createdToken := extractGeneratedToken(string(createBody))
	if createdToken == "" {
		t.Fatalf("expected generated token in create response, got %q", string(createBody))
	}

	bucketReq, _ := http.NewRequest(http.MethodPut, ts.URL+"/token-bucket", strings.NewReader(""))
	bucketReq.Header.Set("Authorization", "Bearer "+createdToken)
	bucketResp, err := client.Do(bucketReq)
	if err != nil {
		t.Fatalf("token-auth bucket create request failed: %v", err)
	}
	_ = bucketResp.Body.Close()
	if bucketResp.StatusCode != http.StatusOK {
		t.Fatalf("expected token-auth bucket create status 200, got %d", bucketResp.StatusCode)
	}

	partialResp, err := client.Get(ts.URL + "/app/partials/tokens")
	if err != nil {
		t.Fatalf("get tokens partial failed: %v", err)
	}
	partialBody, _ := io.ReadAll(partialResp.Body)
	_ = partialResp.Body.Close()
	if partialResp.StatusCode != http.StatusOK {
		t.Fatalf("expected tokens partial status 200, got %d", partialResp.StatusCode)
	}
	tokenID := extractTokenID(string(partialBody))
	if tokenID == "" {
		t.Fatalf("expected token id in partial output, got %q", string(partialBody))
	}

	revokeReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/app/actions/tokens/revoke", strings.NewReader(url.Values{"token_id": {tokenID}}.Encode()))
	revokeReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	revokeReq.Header.Set("HX-Request", "true")
	revokeReq.Header.Set("X-CSRF-Token", csrf)
	revokeResp, err := client.Do(revokeReq)
	if err != nil {
		t.Fatalf("revoke token action failed: %v", err)
	}
	_ = revokeResp.Body.Close()
	if revokeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected revoke token status 200, got %d", revokeResp.StatusCode)
	}

	revokedReq, _ := http.NewRequest(http.MethodPut, ts.URL+"/revoked-bucket", strings.NewReader(""))
	revokedReq.Header.Set("Authorization", "Bearer "+createdToken)
	revokedResp, err := client.Do(revokedReq)
	if err != nil {
		t.Fatalf("revoked token request failed: %v", err)
	}
	_ = revokedResp.Body.Close()
	if revokedResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected revoked token status 403, got %d", revokedResp.StatusCode)
	}
}

func TestHTMXUIFolderMarkerFlow(t *testing.T) {
	t.Parallel()

	bstore, mstore := newUIStores(t)
	h := server.NewHandler(server.Options{Metadata: mstore, Blob: bstore, UIAccessKey: "admin", UISecretKey: "secret"})
	ts := httptest.NewServer(h)
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	loginResp, err := client.PostForm(ts.URL+"/app/login", url.Values{"access_key": {"admin"}, "secret_key": {"secret"}})
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

	createBucketReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/app/actions/create-bucket", strings.NewReader(url.Values{"bucket": {"folders-bucket"}, "_csrf": {csrf}}.Encode()))
	createBucketReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createBucketResp, err := client.Do(createBucketReq)
	if err != nil {
		t.Fatalf("create bucket action failed: %v", err)
	}
	_ = createBucketResp.Body.Close()

	createFolderReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/app/actions/create-folder", strings.NewReader(url.Values{
		"_csrf":     {csrf},
		"bucket":    {"folders-bucket"},
		"prefix":    {"photos/"},
		"delimiter": {"/"},
		"folder":    {"2026"},
	}.Encode()))
	createFolderReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createFolderResp, err := client.Do(createFolderReq)
	if err != nil {
		t.Fatalf("create folder action failed: %v", err)
	}
	_ = createFolderResp.Body.Close()

	objectsResp, err := client.Get(ts.URL + "/app/buckets/folders-bucket/objects?prefix=photos/2026/&delimiter=/")
	if err != nil {
		t.Fatalf("get objects page failed: %v", err)
	}
	objectsBody, _ := io.ReadAll(objectsResp.Body)
	_ = objectsResp.Body.Close()
	if !strings.Contains(string(objectsBody), "Folder breadcrumbs") {
		t.Fatalf("expected breadcrumbs in objects page: %s", string(objectsBody))
	}
	if !strings.Contains(string(objectsBody), "Use current folder + filename") {
		t.Fatalf("expected upload key prefill action in objects page: %s", string(objectsBody))
	}
	if !strings.Contains(string(objectsBody), "prefix=photos%2F") {
		t.Fatalf("expected parent breadcrumb link in objects page: %s", string(objectsBody))
	}

	partialResp, err := client.Get(ts.URL + "/app/partials/objects?bucket=folders-bucket&prefix=photos/2026/&delimiter=/")
	if err != nil {
		t.Fatalf("get objects partial failed: %v", err)
	}
	partialBody, _ := io.ReadAll(partialResp.Body)
	_ = partialResp.Body.Close()
	if partialResp.StatusCode != http.StatusOK {
		t.Fatalf("expected partial status 200, got %d", partialResp.StatusCode)
	}
	if !strings.Contains(string(partialBody), "photos/2026/") || !strings.Contains(string(partialBody), "Delete marker") {
		t.Fatalf("expected folder marker row in partial output, got %q", string(partialBody))
	}

	deleteFolderReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/app/actions/delete-folder-marker", strings.NewReader(url.Values{
		"_csrf":     {csrf},
		"bucket":    {"folders-bucket"},
		"key":       {"photos/2026/"},
		"prefix":    {"photos/2026/"},
		"delimiter": {"/"},
	}.Encode()))
	deleteFolderReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	deleteFolderResp, err := client.Do(deleteFolderReq)
	if err != nil {
		t.Fatalf("delete folder marker action failed: %v", err)
	}
	_ = deleteFolderResp.Body.Close()

	partialResp, err = client.Get(ts.URL + "/app/partials/objects?bucket=folders-bucket&prefix=photos/2026/&delimiter=/")
	if err != nil {
		t.Fatalf("get objects partial after delete failed: %v", err)
	}
	partialBody, _ = io.ReadAll(partialResp.Body)
	_ = partialResp.Body.Close()
	if strings.Contains(string(partialBody), "photos/2026/") {
		t.Fatalf("expected deleted folder marker to be absent, got %q", string(partialBody))
	}
}

func newUIStores(t *testing.T) (*blob.Store, metadata.Store) {
	t.Helper()
	dataRoot := t.TempDir()
	bstore, err := blob.NewStore(blob.Config{RootDir: dataRoot, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	mstore, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:" + filepath.Join(dataRoot, "meta.db")})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	return bstore, mstore
}

func extractToken(body string) string {
	marker := `name="_csrf" value="`
	idx := strings.Index(body, marker)
	if idx < 0 {
		return ""
	}
	start := idx + len(marker)
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		return ""
	}
	return body[start : start+end]
}

func extractGeneratedToken(body string) string {
	marker := `data-generated-token>`
	idx := strings.Index(body, marker)
	if idx < 0 {
		return ""
	}
	start := idx + len(marker)
	end := strings.Index(body[start:], `</code>`)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(body[start : start+end])
}

func extractTokenID(body string) string {
	marker := `name="token_id" value="`
	idx := strings.Index(body, marker)
	if idx < 0 {
		return ""
	}
	start := idx + len(marker)
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		return ""
	}
	return body[start : start+end]
}
