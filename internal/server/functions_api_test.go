package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/functions"
	"ds9labs.com/s000/internal/metadata"
)

type fakeCompiled struct{}

func (fakeCompiled) ID() string { return "fake" }

type fakeInstance struct {
	output []byte
}

func (f fakeInstance) Invoke(context.Context, string, []byte) ([]byte, error) { return f.output, nil }
func (f fakeInstance) Close() error                                           { return nil }

type fakeRuntime struct {
	output []byte
}

func (f fakeRuntime) Init(context.Context, functions.RuntimeConfig) error { return nil }
func (f fakeRuntime) Compile(context.Context, []byte) (functions.CompiledModule, error) {
	return fakeCompiled{}, nil
}
func (f fakeRuntime) Instantiate(context.Context, functions.CompiledModule, functions.Imports) (functions.Instance, error) {
	return fakeInstance{output: f.output}, nil
}
func (f fakeRuntime) SupportsNetworking() bool { return true }
func (f fakeRuntime) Close() error             { return nil }

type captureRuntime struct {
	mu          sync.Mutex
	output      []byte
	lastPayload []byte
}

type captureInstance struct {
	rt *captureRuntime
}

func (c *captureRuntime) Init(context.Context, functions.RuntimeConfig) error { return nil }
func (c *captureRuntime) Compile(context.Context, []byte) (functions.CompiledModule, error) {
	return fakeCompiled{}, nil
}
func (c *captureRuntime) Instantiate(context.Context, functions.CompiledModule, functions.Imports) (functions.Instance, error) {
	return &captureInstance{rt: c}, nil
}
func (c *captureRuntime) SupportsNetworking() bool { return true }
func (c *captureRuntime) Close() error             { return nil }

func (c *captureRuntime) payload() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.lastPayload...)
}

func (i *captureInstance) Invoke(_ context.Context, _ string, payload []byte) ([]byte, error) {
	i.rt.mu.Lock()
	i.rt.lastPayload = append([]byte(nil), payload...)
	out := append([]byte(nil), i.rt.output...)
	i.rt.mu.Unlock()
	return out, nil
}

func (i *captureInstance) Close() error { return nil }

func TestFunctionsCRUDAPI(t *testing.T) {
	t.Parallel()

	h, _ := testHandlerWithFunctions(t, nil)
	moduleB64 := base64.StdEncoding.EncodeToString([]byte("wasm-module"))
	body := `{"name":"fn-a","runtime":"wasmer","trigger":"onPutObjectPre","enabled":true,"module_base64":"` + moduleB64 + `"}`
	req := httptest.NewRequest(http.MethodPost, "/functions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d body=%s", rr.Code, rr.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/functions", nil)
	listRR := httptest.NewRecorder()
	h.ServeHTTP(listRR, listReq)
	if listRR.Code != http.StatusOK {
		t.Fatalf("expected list status 200, got %d", listRR.Code)
	}
	var listResp struct {
		Functions []map[string]any `json:"functions"`
	}
	if err := json.Unmarshal(listRR.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Functions) != 1 {
		t.Fatalf("expected one function, got %d", len(listResp.Functions))
	}

	getReq := httptest.NewRequest(http.MethodGet, "/functions/fn-a", nil)
	getRR := httptest.NewRecorder()
	h.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("expected get status 200, got %d", getRR.Code)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/functions/fn-a", nil)
	delRR := httptest.NewRecorder()
	h.ServeHTTP(delRR, delReq)
	if delRR.Code != http.StatusNoContent {
		t.Fatalf("expected delete status 204, got %d", delRR.Code)
	}
}

func TestFunctionsVersionsAndActivateAPI(t *testing.T) {
	t.Parallel()

	h, _ := testHandlerWithFunctions(t, nil)
	moduleV1 := base64.StdEncoding.EncodeToString([]byte("v1"))
	body := `{"name":"fn-v","runtime":"wazero","trigger":"onPutObjectPre","enabled":true,"module_base64":"` + moduleV1 + `"}`
	createReq := httptest.NewRequest(http.MethodPost, "/functions", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createRR := httptest.NewRecorder()
	h.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d body=%s", createRR.Code, createRR.Body.String())
	}

	moduleV2 := base64.StdEncoding.EncodeToString([]byte("v2"))
	updateBody := `{"runtime":"wazero","trigger":"onPutObjectPre","enabled":true,"module_base64":"` + moduleV2 + `"}`
	updateReq := httptest.NewRequest(http.MethodPut, "/functions/fn-v", bytes.NewBufferString(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRR := httptest.NewRecorder()
	h.ServeHTTP(updateRR, updateReq)
	if updateRR.Code != http.StatusOK {
		t.Fatalf("expected update status 200, got %d body=%s", updateRR.Code, updateRR.Body.String())
	}

	versionsReq := httptest.NewRequest(http.MethodGet, "/functions/fn-v/versions", nil)
	versionsRR := httptest.NewRecorder()
	h.ServeHTTP(versionsRR, versionsReq)
	if versionsRR.Code != http.StatusOK {
		t.Fatalf("expected versions status 200, got %d", versionsRR.Code)
	}
	var versionsResp struct {
		Versions []map[string]any `json:"versions"`
	}
	if err := json.Unmarshal(versionsRR.Body.Bytes(), &versionsResp); err != nil {
		t.Fatalf("decode versions response: %v", err)
	}
	if len(versionsResp.Versions) != 2 {
		t.Fatalf("expected two versions, got %d", len(versionsResp.Versions))
	}

	activateReq := httptest.NewRequest(http.MethodPost, "/functions/fn-v/activate", bytes.NewBufferString(`{"version":1}`))
	activateReq.Header.Set("Content-Type", "application/json")
	activateRR := httptest.NewRecorder()
	h.ServeHTTP(activateRR, activateReq)
	if activateRR.Code != http.StatusOK {
		t.Fatalf("expected activate status 200, got %d body=%s", activateRR.Code, activateRR.Body.String())
	}
	var activateResp struct {
		Function map[string]any `json:"function"`
	}
	if err := json.Unmarshal(activateRR.Body.Bytes(), &activateResp); err != nil {
		t.Fatalf("decode activate response: %v", err)
	}
	if got := activateResp.Function["active_version"]; got != float64(1) {
		t.Fatalf("expected active_version 1, got %#v", got)
	}
}

func TestFunctionsInvokeMetricsLogsAndTemplatesAPI(t *testing.T) {
	t.Parallel()

	h, _ := testHandlerWithFunctions(t, []byte(`{"continue":true,"output":{"ok":true}}`))
	moduleV1 := base64.StdEncoding.EncodeToString([]byte("v1"))
	body := `{"name":"fn-i","runtime":"wazero","trigger":"onPutObjectPre","enabled":true,"module_base64":"` + moduleV1 + `"}`
	createReq := httptest.NewRequest(http.MethodPost, "/functions", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createRR := httptest.NewRecorder()
	h.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d body=%s", createRR.Code, createRR.Body.String())
	}

	invokeReq := httptest.NewRequest(http.MethodPost, "/functions/fn-i/invoke", bytes.NewBufferString(`{"payload":{"k":"v"}}`))
	invokeReq.Header.Set("Content-Type", "application/json")
	invokeRR := httptest.NewRecorder()
	h.ServeHTTP(invokeRR, invokeReq)
	if invokeRR.Code != http.StatusOK {
		t.Fatalf("expected invoke status 200, got %d body=%s", invokeRR.Code, invokeRR.Body.String())
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/functions/metrics", nil)
	metricsRR := httptest.NewRecorder()
	h.ServeHTTP(metricsRR, metricsReq)
	if metricsRR.Code != http.StatusOK {
		t.Fatalf("expected metrics status 200, got %d", metricsRR.Code)
	}
	if !bytes.Contains(metricsRR.Body.Bytes(), []byte("fn-i")) {
		t.Fatalf("expected metrics body to include function name, got %s", metricsRR.Body.String())
	}

	logsReq := httptest.NewRequest(http.MethodGet, "/functions/logs?limit=10", nil)
	logsRR := httptest.NewRecorder()
	h.ServeHTTP(logsRR, logsReq)
	if logsRR.Code != http.StatusOK {
		t.Fatalf("expected logs status 200, got %d", logsRR.Code)
	}
	if !bytes.Contains(logsRR.Body.Bytes(), []byte("manual")) {
		t.Fatalf("expected logs body to include trigger manual, got %s", logsRR.Body.String())
	}

	alertsReq := httptest.NewRequest(http.MethodGet, "/functions/alerts", nil)
	alertsRR := httptest.NewRecorder()
	h.ServeHTTP(alertsRR, alertsReq)
	if alertsRR.Code != http.StatusOK {
		t.Fatalf("expected alerts status 200, got %d", alertsRR.Code)
	}

	templatesReq := httptest.NewRequest(http.MethodGet, "/functions/templates", nil)
	templatesRR := httptest.NewRecorder()
	h.ServeHTTP(templatesRR, templatesReq)
	if templatesRR.Code != http.StatusOK {
		t.Fatalf("expected templates status 200, got %d", templatesRR.Code)
	}
	if !bytes.Contains(templatesRR.Body.Bytes(), []byte("hello-json")) {
		t.Fatalf("expected templates body to include hello-json, got %s", templatesRR.Body.String())
	}
}

func TestPutObjectPreHookBlocksWrite(t *testing.T) {
	t.Parallel()

	h, mstore := testHandlerWithFunctions(t, []byte(`{"continue":false}`))

	if err := mstore.CreateBucket(context.Background(), metadata.Bucket{Name: "photos", CreatedAt: time.Now().UTC(), Region: "us-east-1", VersioningStatus: "Suspended"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}

	putReq := httptest.NewRequest(http.MethodPut, "/photos/a.txt", bytes.NewBufferString("hello"))
	putRR := httptest.NewRecorder()
	h.ServeHTTP(putRR, putReq)
	if putRR.Code != http.StatusForbidden {
		t.Fatalf("expected blocked put status 403, got %d body=%s", putRR.Code, putRR.Body.String())
	}
}

func TestPutObjectRateLimitFlowAndAlerts(t *testing.T) {
	t.Parallel()

	h, mstore := testHandlerWithFunctionsConfig(t, []byte(`{"continue":true}`), functions.Config{
		Enabled:                  true,
		Dir:                      t.TempDir(),
		Runtime:                  functions.RuntimeWazero,
		MemoryLimitMB:            64,
		CPULimit:                 100 * time.Millisecond,
		NetworkAllow:             true,
		FSAllow:                  false,
		ReloadInterval:           2 * time.Second,
		RateLimitPerMinute:       1,
		AlertErrorCountThreshold: 1,
	})

	if err := mstore.CreateBucket(context.Background(), metadata.Bucket{Name: "photos", CreatedAt: time.Now().UTC(), Region: "us-east-1", VersioningStatus: "Suspended"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}

	module := base64.StdEncoding.EncodeToString([]byte("module"))
	createReq := httptest.NewRequest(http.MethodPost, "/functions", bytes.NewBufferString(`{"name":"rl","runtime":"wazero","trigger":"onPutObjectPre","enabled":true,"module_base64":"`+module+`"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRR := httptest.NewRecorder()
	h.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d body=%s", createRR.Code, createRR.Body.String())
	}

	firstPut := httptest.NewRequest(http.MethodPut, "/photos/one.txt", bytes.NewBufferString("hello"))
	firstRR := httptest.NewRecorder()
	h.ServeHTTP(firstRR, firstPut)
	if firstRR.Code != http.StatusOK {
		t.Fatalf("expected first put status 200, got %d body=%s", firstRR.Code, firstRR.Body.String())
	}

	secondPut := httptest.NewRequest(http.MethodPut, "/photos/two.txt", bytes.NewBufferString("hello"))
	secondRR := httptest.NewRecorder()
	h.ServeHTTP(secondRR, secondPut)
	if secondRR.Code != http.StatusInternalServerError {
		t.Fatalf("expected second put status 500 from rate limit deny, got %d body=%s", secondRR.Code, secondRR.Body.String())
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/functions/metrics", nil)
	metricsRR := httptest.NewRecorder()
	h.ServeHTTP(metricsRR, metricsReq)
	if metricsRR.Code != http.StatusOK {
		t.Fatalf("expected metrics status 200, got %d", metricsRR.Code)
	}
	if !bytes.Contains(metricsRR.Body.Bytes(), []byte("rate_limited")) {
		t.Fatalf("expected metrics to include rate_limited counter, got %s", metricsRR.Body.String())
	}

	alertsReq := httptest.NewRequest(http.MethodGet, "/functions/alerts", nil)
	alertsRR := httptest.NewRecorder()
	h.ServeHTTP(alertsRR, alertsReq)
	if alertsRR.Code != http.StatusOK {
		t.Fatalf("expected alerts status 200, got %d", alertsRR.Code)
	}
	if !bytes.Contains(alertsRR.Body.Bytes(), []byte("rate_limited")) {
		t.Fatalf("expected alerts to include rate_limited reason, got %s", alertsRR.Body.String())
	}
}

func TestFunctionsHTTPPassthroughInvokeAPI(t *testing.T) {
	t.Parallel()

	rt := &captureRuntime{output: []byte(`{"continue":true,"output":{"status":201,"headers":{"X-Test":"ok","Content-Type":"application/json"},"body":{"message":"hello world"}}}`)}
	h, _ := testHandlerWithCustomRuntime(t, rt)

	moduleB64 := base64.StdEncoding.EncodeToString([]byte("wasm-module"))
	body := `{"name":"hello-world-js","runtime":"wazero","trigger":"onHTTPPre","enabled":true,"module_base64":"` + moduleB64 + `"}`
	createReq := httptest.NewRequest(http.MethodPost, "/functions", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createRR := httptest.NewRecorder()
	h.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d body=%s", createRR.Code, createRR.Body.String())
	}

	invokeReq := httptest.NewRequest(http.MethodPost, "/fn/hello-world-js/demo/path?mode=test", bytes.NewBufferString(`{"name":"trip"}`))
	invokeReq.Header.Set("Content-Type", "application/json")
	invokeReq.Header.Set("X-Client", "browser")
	invokeRR := httptest.NewRecorder()
	h.ServeHTTP(invokeRR, invokeReq)
	if invokeRR.Code != http.StatusCreated {
		t.Fatalf("expected invoke status 201, got %d body=%s", invokeRR.Code, invokeRR.Body.String())
	}
	if got := invokeRR.Header().Get("X-Test"); got != "ok" {
		t.Fatalf("expected X-Test header ok, got %q", got)
	}
	if !bytes.Contains(invokeRR.Body.Bytes(), []byte("hello world")) {
		t.Fatalf("expected invoke body to include hello world, got %s", invokeRR.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rt.payload(), &payload); err != nil {
		t.Fatalf("decode captured payload failed: %v", err)
	}
	if got, _ := payload["method"].(string); got != http.MethodPost {
		t.Fatalf("expected payload method POST, got %q", got)
	}
	if got, _ := payload["path"].(string); got != "/demo/path" {
		t.Fatalf("expected payload path /demo/path, got %q", got)
	}
	if got, _ := payload["raw_query"].(string); got != "mode=test" {
		t.Fatalf("expected payload raw_query mode=test, got %q", got)
	}
	if got, _ := payload["body"].(string); got != `{"name":"trip"}` {
		t.Fatalf("expected payload body to match request, got %q", got)
	}
}

func TestFunctionsHTTPPassthroughInvokeNotFound(t *testing.T) {
	t.Parallel()

	h, _ := testHandlerWithFunctions(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/fn/missing", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 for missing function, got %d", rr.Code)
	}
}

func TestFunctionsHTTPPassthroughCORSPreflight(t *testing.T) {
	t.Parallel()

	rt := &captureRuntime{output: []byte(`{"continue":true,"output":{"status":200,"body":{"ok":true}}}`)}
	h, _ := testHandlerWithCustomRuntimeAndOptions(t, rt, Options{
		FunctionsHTTPPublic:               true,
		FunctionsHTTPCORSAllowOrigin:      "*",
		FunctionsHTTPCORSAllowMethods:     "GET,POST,OPTIONS",
		FunctionsHTTPCORSAllowHeaders:     "Content-Type,X-Request-Id",
		FunctionsHTTPCORSMaxAge:           1200,
		FunctionsHTTPCORSAllowCredentials: false,
	})

	moduleB64 := base64.StdEncoding.EncodeToString([]byte("wasm-module"))
	body := `{"name":"hello-world-js","runtime":"wazero","trigger":"onHTTPPre","enabled":true,"module_base64":"` + moduleB64 + `"}`
	createReq := httptest.NewRequest(http.MethodPost, "/functions", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createRR := httptest.NewRecorder()
	h.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d body=%s", createRR.Code, createRR.Body.String())
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/fn/hello-world-js/demo", nil)
	preflight.Header.Set("Origin", "https://app.example.com")
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	preflight.Header.Set("Access-Control-Request-Headers", "Content-Type,X-Request-Id")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, preflight)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected preflight status 204, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected allow-origin '*', got %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got != "GET,POST,OPTIONS" {
		t.Fatalf("expected allow-methods header, got %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type,X-Request-Id" {
		t.Fatalf("expected allow-headers header, got %q", got)
	}
	if got := rr.Header().Get("Access-Control-Max-Age"); got != "1200" {
		t.Fatalf("expected max-age 1200, got %q", got)
	}
	if payload := rt.payload(); len(payload) != 0 {
		t.Fatalf("expected preflight not to invoke function, payload=%s", string(payload))
	}
}

func TestFunctionsHTTPPassthroughCORSInvokeHeaders(t *testing.T) {
	t.Parallel()

	rt := &captureRuntime{output: []byte(`{"continue":true,"output":{"status":200,"body":{"message":"ok"}}}`)}
	h, _ := testHandlerWithCustomRuntimeAndOptions(t, rt, Options{
		FunctionsHTTPPublic:               true,
		FunctionsHTTPCORSAllowOrigin:      "https://app.example.com,https://www.example.com",
		FunctionsHTTPCORSExposeHeaders:    "X-Trace-Id",
		FunctionsHTTPCORSAllowCredentials: true,
	})

	moduleB64 := base64.StdEncoding.EncodeToString([]byte("wasm-module"))
	body := `{"name":"hello-world-js","runtime":"wazero","trigger":"onHTTPPre","enabled":true,"module_base64":"` + moduleB64 + `"}`
	createReq := httptest.NewRequest(http.MethodPost, "/functions", bytes.NewBufferString(body))
	createReq.Header.Set("Content-Type", "application/json")
	createRR := httptest.NewRecorder()
	h.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d body=%s", createRR.Code, createRR.Body.String())
	}

	invokeReq := httptest.NewRequest(http.MethodGet, "/fn/hello-world-js", nil)
	invokeReq.Header.Set("Origin", "https://app.example.com")
	invokeRR := httptest.NewRecorder()
	h.ServeHTTP(invokeRR, invokeReq)
	if invokeRR.Code != http.StatusOK {
		t.Fatalf("expected invoke status 200, got %d body=%s", invokeRR.Code, invokeRR.Body.String())
	}
	if got := invokeRR.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("expected allow-origin to echo origin, got %q", got)
	}
	if got := invokeRR.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("expected allow-credentials true, got %q", got)
	}
	if got := invokeRR.Header().Get("Access-Control-Expose-Headers"); got != "X-Trace-Id" {
		t.Fatalf("expected expose-headers X-Trace-Id, got %q", got)
	}
}

func testHandlerWithFunctions(t *testing.T, runtimeOutput []byte) (http.Handler, metadata.Store) {
	t.Helper()
	return testHandlerWithFunctionsConfig(t, runtimeOutput, functions.Config{
		Enabled:        true,
		Dir:            t.TempDir(),
		Runtime:        functions.RuntimeWazero,
		MemoryLimitMB:  64,
		CPULimit:       100 * time.Millisecond,
		NetworkAllow:   true,
		FSAllow:        false,
		ReloadInterval: 2 * time.Second,
	})
}

func testHandlerWithFunctionsConfig(t *testing.T, runtimeOutput []byte, fnCfg functions.Config) (http.Handler, metadata.Store) {
	t.Helper()

	store, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:test.db"})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	bstore, err := blob.NewStore(blob.Config{RootDir: t.TempDir(), FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}

	mgr, err := functions.NewManager(fnCfg)
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	mgr.SetRuntimeForTesting(fakeRuntime{output: runtimeOutput})

	if runtimeOutput != nil {
		if err := mgr.CreateFunction(functions.Function{
			Name:    "block-put",
			Runtime: functions.RuntimeWasmer,
			Trigger: functions.TriggerPutObjectPre,
			Enabled: true,
			Module:  []byte("module"),
		}); err != nil {
			t.Fatalf("create function failed: %v", err)
		}
	}

	return NewHandler(Options{
		Metadata:     store,
		Blob:         bstore,
		ReadyCheck:   func(context.Context) error { return nil },
		BucketRegion: "us-east-1",
		Verifier:     allowAllVerifier{},
		Functions:    mgr,
	}), store
}

func testHandlerWithCustomRuntime(t *testing.T, rt functions.Runtime) (http.Handler, metadata.Store) {
	t.Helper()
	return testHandlerWithCustomRuntimeAndOptions(t, rt, Options{})
}

func testHandlerWithCustomRuntimeAndOptions(t *testing.T, rt functions.Runtime, o Options) (http.Handler, metadata.Store) {
	t.Helper()

	store, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:test.db"})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	bstore, err := blob.NewStore(blob.Config{RootDir: t.TempDir(), FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}

	mgr, err := functions.NewManager(functions.Config{
		Enabled:        true,
		Dir:            t.TempDir(),
		Runtime:        functions.RuntimeWazero,
		MemoryLimitMB:  64,
		CPULimit:       100 * time.Millisecond,
		NetworkAllow:   true,
		FSAllow:        false,
		ReloadInterval: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	mgr.SetRuntimeForTesting(rt)

	return NewHandler(Options{
		Metadata:                          store,
		Blob:                              bstore,
		ReadyCheck:                        func(context.Context) error { return nil },
		BucketRegion:                      "us-east-1",
		Verifier:                          allowAllVerifier{},
		Functions:                         mgr,
		FunctionsHTTPPublic:               o.FunctionsHTTPPublic,
		FunctionsHTTPCORSAllowOrigin:      o.FunctionsHTTPCORSAllowOrigin,
		FunctionsHTTPCORSAllowMethods:     o.FunctionsHTTPCORSAllowMethods,
		FunctionsHTTPCORSAllowHeaders:     o.FunctionsHTTPCORSAllowHeaders,
		FunctionsHTTPCORSExposeHeaders:    o.FunctionsHTTPCORSExposeHeaders,
		FunctionsHTTPCORSMaxAge:           o.FunctionsHTTPCORSMaxAge,
		FunctionsHTTPCORSAllowCredentials: o.FunctionsHTTPCORSAllowCredentials,
	}), store
}
