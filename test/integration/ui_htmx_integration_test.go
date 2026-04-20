//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestHTMXUIFolderFlowMatchesS3Browsing(t *testing.T) {
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

	seedObjectForUIFolderTest(t, bstore, mstore, "folders-bucket", "photos/2026/guide.txt", "guide")

	objectsResp, err := client.Get(ts.URL + "/app/buckets/folders-bucket/objects?prefix=photos/&delimiter=/")
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
	if !strings.Contains(string(objectsBody), "photos/2026/") {
		t.Fatalf("expected created folder prefix in objects page: %s", string(objectsBody))
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
	if !strings.Contains(string(partialBody), "photos/2026/guide.txt") {
		t.Fatalf("expected file inside folder listing before delete, got %q", string(partialBody))
	}
	if strings.Contains(string(partialBody), "Delete marker") {
		t.Fatalf("expected S3-style folder view to hide marker rows, got %q", string(partialBody))
	}

	parentPartialResp, err := client.Get(ts.URL + "/app/partials/objects?bucket=folders-bucket&prefix=photos/&delimiter=/")
	if err != nil {
		t.Fatalf("get parent objects partial failed: %v", err)
	}
	parentPartialBody, _ := io.ReadAll(parentPartialResp.Body)
	_ = parentPartialResp.Body.Close()
	if !strings.Contains(string(parentPartialBody), "Delete folder") {
		t.Fatalf("expected delete folder action for common prefix, got %q", string(parentPartialBody))
	}

	deleteFolderReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/app/actions/delete-folder", strings.NewReader(url.Values{
		"_csrf":         {csrf},
		"bucket":        {"folders-bucket"},
		"prefix":        {"photos/"},
		"delimiter":     {"/"},
		"target_prefix": {"photos/2026/"},
	}.Encode()))
	deleteFolderReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	deleteFolderResp, err := client.Do(deleteFolderReq)
	if err != nil {
		t.Fatalf("delete folder action failed: %v", err)
	}
	_ = deleteFolderResp.Body.Close()

	partialResp, err = client.Get(ts.URL + "/app/partials/objects?bucket=folders-bucket&prefix=photos/2026/&delimiter=/")
	if err != nil {
		t.Fatalf("get objects partial after folder delete failed: %v", err)
	}
	partialBody, _ = io.ReadAll(partialResp.Body)
	_ = partialResp.Body.Close()
	if !strings.Contains(string(partialBody), "No objects found.") {
		t.Fatalf("expected empty folder listing after delete, got %q", string(partialBody))
	}

	parentPartialResp, err = client.Get(ts.URL + "/app/partials/objects?bucket=folders-bucket&prefix=photos/&delimiter=/")
	if err != nil {
		t.Fatalf("get parent objects partial after folder delete failed: %v", err)
	}
	parentPartialBody, _ = io.ReadAll(parentPartialResp.Body)
	_ = parentPartialResp.Body.Close()
	if strings.Contains(string(parentPartialBody), "photos/2026/") {
		t.Fatalf("expected deleted folder prefix to be absent in parent listing, got %q", string(parentPartialBody))
	}
}

func TestHTMXUIBucketDeleteRemovesBucketAndContents(t *testing.T) {
	t.Parallel()

	bstore, mstore := newUIStores(t)
	if err := mstore.CreateBucket(context.Background(), metadata.Bucket{Name: "delete-bucket", CreatedAt: time.Now().UTC(), Region: "us-east-1", VersioningStatus: "Suspended"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}
	seedObjectForUIFolderTest(t, bstore, mstore, "delete-bucket", "assets/main.js", "console.log('ok')")

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

	bucketResp, err := client.Get(ts.URL + "/app/buckets/delete-bucket")
	if err != nil {
		t.Fatalf("get bucket detail page failed: %v", err)
	}
	bucketPage, _ := io.ReadAll(bucketResp.Body)
	_ = bucketResp.Body.Close()
	csrf := extractToken(string(bucketPage))
	if csrf == "" {
		t.Fatalf("failed to find csrf token in bucket detail page: %s", string(bucketPage))
	}
	if !strings.Contains(string(bucketPage), "Delete bucket and contents") {
		t.Fatalf("expected bucket delete action in bucket detail page: %s", string(bucketPage))
	}

	deleteReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/app/actions/delete-bucket", strings.NewReader(url.Values{
		"_csrf":  {csrf},
		"bucket": {"delete-bucket"},
	}.Encode()))
	deleteReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	deleteResp, err := client.Do(deleteReq)
	if err != nil {
		t.Fatalf("delete bucket action failed: %v", err)
	}
	deleteBody, _ := io.ReadAll(deleteResp.Body)
	_ = deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("expected delete bucket final status 200, got %d body=%q", deleteResp.StatusCode, string(deleteBody))
	}
	if !strings.Contains(string(deleteBody), "bucket deleted with contents") {
		t.Fatalf("expected delete bucket flash message in buckets page, got %q", string(deleteBody))
	}

	if _, err := mstore.GetBucket(context.Background(), "delete-bucket"); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("expected deleted bucket to be absent, got err=%v", err)
	}
	if _, err := mstore.GetLatestObjectVersion(context.Background(), "delete-bucket", "assets/main.js"); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("expected deleted object metadata to be absent, got err=%v", err)
	}
}

func TestHTMXUIUploadsPageSupportsMultiFileUpload(t *testing.T) {
	t.Parallel()

	bstore, mstore := newUIStores(t)
	if err := mstore.CreateBucket(context.Background(), metadata.Bucket{Name: "uploads-bucket", CreatedAt: time.Now().UTC(), Region: "us-east-1", VersioningStatus: "Suspended"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}
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

	uploadsResp, err := client.Get(ts.URL + "/app/uploads")
	if err != nil {
		t.Fatalf("get uploads page failed: %v", err)
	}
	uploadsPage, _ := io.ReadAll(uploadsResp.Body)
	_ = uploadsResp.Body.Close()
	csrf := extractToken(string(uploadsPage))
	if csrf == "" {
		t.Fatalf("failed to find csrf token in uploads page: %s", string(uploadsPage))
	}
	if !strings.Contains(string(uploadsPage), "Drop files here") {
		t.Fatalf("expected drag and drop helper text in uploads page: %s", string(uploadsPage))
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("_csrf", csrf)
	_ = writer.WriteField("bucket", "uploads-bucket")
	_ = writer.WriteField("prefix", "batch/")
	_ = writer.WriteField("delimiter", "/")
	_ = writer.WriteField("return_to", "/app/uploads")
	fileA, _ := writer.CreateFormFile("files", "a.txt")
	_, _ = fileA.Write([]byte("alpha"))
	_ = writer.WriteField("file_key", "a.txt")
	fileB, _ := writer.CreateFormFile("files", "b.txt")
	_, _ = fileB.Write([]byte("beta"))
	_ = writer.WriteField("file_key", "b.txt")
	fileC, _ := writer.CreateFormFile("files", "c.txt")
	_, _ = fileC.Write([]byte("gamma"))
	_ = writer.WriteField("file_key", "folder/nested/c.txt")
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer failed: %v", err)
	}

	uploadReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/app/actions/upload-object", body)
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	uploadResp, err := client.Do(uploadReq)
	if err != nil {
		t.Fatalf("multi upload request failed: %v", err)
	}
	uploadRespBody, _ := io.ReadAll(uploadResp.Body)
	_ = uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusOK {
		t.Fatalf("expected upload redirect landing status 200, got %d body=%q", uploadResp.StatusCode, string(uploadRespBody))
	}
	if !strings.Contains(string(uploadRespBody), "uploaded&#43;3&#43;files") {
		t.Fatalf("expected multi upload flash message in uploads page, got %q", string(uploadRespBody))
	}

	objA, err := mstore.GetLatestObjectVersion(context.Background(), "uploads-bucket", "batch/a.txt")
	if err != nil {
		t.Fatalf("expected uploaded key batch/a.txt, got error: %v", err)
	}
	if objA.Size != 5 {
		t.Fatalf("unexpected size for batch/a.txt: %d", objA.Size)
	}
	objB, err := mstore.GetLatestObjectVersion(context.Background(), "uploads-bucket", "batch/b.txt")
	if err != nil {
		t.Fatalf("expected uploaded key batch/b.txt, got error: %v", err)
	}
	if objB.Size != 4 {
		t.Fatalf("unexpected size for batch/b.txt: %d", objB.Size)
	}
	objC, err := mstore.GetLatestObjectVersion(context.Background(), "uploads-bucket", "batch/folder/nested/c.txt")
	if err != nil {
		t.Fatalf("expected uploaded key batch/folder/nested/c.txt, got error: %v", err)
	}
	if objC.Size != 5 {
		t.Fatalf("unexpected size for batch/folder/nested/c.txt: %d", objC.Size)
	}
}

func TestHTMXUIObjectMimeTypeCanBeUpdatedAfterUpload(t *testing.T) {
	t.Parallel()

	bstore, mstore := newUIStores(t)
	if err := mstore.CreateBucket(context.Background(), metadata.Bucket{Name: "mime-bucket", CreatedAt: time.Now().UTC(), Region: "us-east-1", VersioningStatus: "Suspended"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}
	seedObjectForUIFolderTest(t, bstore, mstore, "mime-bucket", "web/composeApp.wasm", "wasm-bits")

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

	detailURL := ts.URL + "/app/buckets/mime-bucket/objects/" + url.PathEscape("web/composeApp.wasm")
	detailResp, err := client.Get(detailURL)
	if err != nil {
		t.Fatalf("get object detail page failed: %v", err)
	}
	detailBody, _ := io.ReadAll(detailResp.Body)
	_ = detailResp.Body.Close()
	csrf := extractToken(string(detailBody))
	if csrf == "" {
		t.Fatalf("failed to find csrf token in object detail page: %s", string(detailBody))
	}
	if !strings.Contains(string(detailBody), "text/plain; charset=utf-8") {
		t.Fatalf("expected existing mime type in object detail page: %s", string(detailBody))
	}

	updateReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/app/actions/update-object-mimetype", strings.NewReader(url.Values{
		"_csrf":        {csrf},
		"bucket":       {"mime-bucket"},
		"key":          {"web/composeApp.wasm"},
		"content_type": {"application/wasm"},
	}.Encode()))
	updateReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	updateResp, err := client.Do(updateReq)
	if err != nil {
		t.Fatalf("update mime type request failed: %v", err)
	}
	updateBody, _ := io.ReadAll(updateResp.Body)
	_ = updateResp.Body.Close()
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("expected update mime type final status 200, got %d body=%q", updateResp.StatusCode, string(updateBody))
	}
	if !strings.Contains(string(updateBody), "application/wasm") {
		t.Fatalf("expected updated mime type in object detail page: %q", string(updateBody))
	}

	updatedObj, err := mstore.GetLatestObjectVersion(context.Background(), "mime-bucket", "web/composeApp.wasm")
	if err != nil {
		t.Fatalf("get updated object metadata failed: %v", err)
	}
	if got := updatedObj.Metadata["content-type"]; got != "application/wasm" {
		t.Fatalf("expected content-type application/wasm, got %q", got)
	}
}

func seedObjectForUIFolderTest(t *testing.T, bstore *blob.Store, mstore metadata.Store, bucket, key, body string) {
	t.Helper()
	ref := blob.ObjectRef{Bucket: bucket, Key: key, VersionID: "null"}
	meta, err := bstore.WriteObject(context.Background(), ref, strings.NewReader(body))
	if err != nil {
		t.Fatalf("seed blob write failed: %v", err)
	}
	err = mstore.PutObjectVersion(context.Background(), metadata.ObjectVersion{
		Bucket:         bucket,
		Key:            key,
		VersionID:      "null",
		Size:           meta.Size,
		ETag:           meta.MD5Hex,
		ChecksumSHA256: meta.SHA256,
		StoragePath:    meta.Path,
		Metadata:       map[string]string{"content-type": "text/plain; charset=utf-8"},
		CreatedAt:      meta.CreatedAt,
	})
	if err != nil {
		t.Fatalf("seed metadata write failed: %v", err)
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
