package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func testHandlerWithFunctions(t *testing.T, runtimeOutput []byte) (http.Handler, metadata.Store) {
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
