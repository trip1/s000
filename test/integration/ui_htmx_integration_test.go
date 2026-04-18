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
	"ds9labs.com/s000/internal/functions"
	"ds9labs.com/s000/internal/metadata"
	"ds9labs.com/s000/internal/server"
)

type fakeCompiled struct{}

func (fakeCompiled) ID() string { return "fake" }

type fakeInstance struct{ output []byte }

func (f fakeInstance) Invoke(context.Context, string, []byte) ([]byte, error) { return f.output, nil }
func (f fakeInstance) Close() error                                           { return nil }

type fakeRuntime struct{ output []byte }

func (f fakeRuntime) Init(context.Context, functions.RuntimeConfig) error { return nil }
func (f fakeRuntime) Compile(context.Context, []byte) (functions.CompiledModule, error) {
	return fakeCompiled{}, nil
}
func (f fakeRuntime) Instantiate(context.Context, functions.CompiledModule, functions.Imports) (functions.Instance, error) {
	return fakeInstance{output: f.output}, nil
}
func (f fakeRuntime) SupportsNetworking() bool { return true }
func (f fakeRuntime) Close() error             { return nil }

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

func TestHTMXUIFunctionsFlowCreateInvokeActivateDelete(t *testing.T) {
	t.Parallel()

	bstore, mstore := newUIStores(t)
	mgr, err := functions.NewManager(functions.Config{Enabled: true, Dir: t.TempDir(), Runtime: functions.RuntimeWazero, MemoryLimitMB: 64, CPULimit: 100 * time.Millisecond, ReloadInterval: 2 * time.Second})
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	mgr.SetRuntimeForTesting(fakeRuntime{output: []byte(`{"continue":true,"output":{"ok":true}}`)})
	h := server.NewHandler(server.Options{Metadata: mstore, Blob: bstore, UIAccessKey: "admin", UISecretKey: "secret", Functions: mgr})
	ts := httptest.NewServer(h)
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	loginResp, err := client.PostForm(ts.URL+"/app/login", url.Values{"access_key": {"admin"}, "secret_key": {"secret"}})
	if err != nil {
		t.Fatalf("login request failed: %v", err)
	}
	_ = loginResp.Body.Close()

	pageResp, err := client.Get(ts.URL + "/app/functions")
	if err != nil {
		t.Fatalf("get functions page failed: %v", err)
	}
	pageBody, _ := io.ReadAll(pageResp.Body)
	_ = pageResp.Body.Close()
	csrf := extractToken(string(pageBody))
	if csrf == "" {
		t.Fatalf("expected csrf token on functions page, got %q", string(pageBody))
	}

	create := url.Values{"_csrf": {csrf}, "name": {"int-fn"}, "runtime": {"wazero"}, "trigger": {"onPutObjectPre"}, "priority": {"100"}, "enabled": {"on"}, "module_base64": {"bW9kdWxl"}}
	createResp, err := client.PostForm(ts.URL+"/app/actions/functions/create", create)
	if err != nil {
		t.Fatalf("create function action failed: %v", err)
	}
	_ = createResp.Body.Close()

	invoke := url.Values{"_csrf": {csrf}, "name": {"int-fn"}, "payload": {`{"bucket":"photos"}`}}
	invokeResp, err := client.PostForm(ts.URL+"/app/actions/functions/invoke", invoke)
	if err != nil {
		t.Fatalf("invoke function action failed: %v", err)
	}
	_ = invokeResp.Body.Close()

	update := url.Values{"_csrf": {csrf}, "name": {"int-fn"}, "runtime": {"wazero"}, "trigger": {"onPutObjectPre"}, "priority": {"90"}, "enabled": {"on"}, "module_base64": {"djI="}}
	updateResp, err := client.PostForm(ts.URL+"/app/actions/functions/update", update)
	if err != nil {
		t.Fatalf("update function action failed: %v", err)
	}
	_ = updateResp.Body.Close()

	versionsResp, err := client.Get(ts.URL + "/app/partials/function-versions?name=int-fn")
	if err != nil {
		t.Fatalf("get versions partial failed: %v", err)
	}
	versionsBody, _ := io.ReadAll(versionsResp.Body)
	_ = versionsResp.Body.Close()
	if !strings.Contains(string(versionsBody), ">2<") {
		t.Fatalf("expected version 2 in versions partial, got %q", string(versionsBody))
	}

	activate := url.Values{"_csrf": {csrf}, "name": {"int-fn"}, "version": {"1"}}
	activateResp, err := client.PostForm(ts.URL+"/app/actions/functions/activate", activate)
	if err != nil {
		t.Fatalf("activate function version failed: %v", err)
	}
	_ = activateResp.Body.Close()

	delResp, err := client.PostForm(ts.URL+"/app/actions/functions/delete", url.Values{"_csrf": {csrf}, "name": {"int-fn"}})
	if err != nil {
		t.Fatalf("delete function action failed: %v", err)
	}
	_ = delResp.Body.Close()

	listResp, err := client.Get(ts.URL + "/app/partials/functions")
	if err != nil {
		t.Fatalf("get functions partial failed: %v", err)
	}
	listBody, _ := io.ReadAll(listResp.Body)
	_ = listResp.Body.Close()
	if strings.Contains(string(listBody), "int-fn") {
		t.Fatalf("expected function to be deleted, got %q", string(listBody))
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
