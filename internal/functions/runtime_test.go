package functions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	if cfg.Enabled {
		t.Fatal("expected functions disabled by default")
	}
	if cfg.Dir != "./functions" {
		t.Fatalf("expected default dir ./functions, got %q", cfg.Dir)
	}
	if cfg.Runtime != RuntimeWazero {
		t.Fatalf("expected default runtime %q, got %q", RuntimeWazero, cfg.Runtime)
	}
	if cfg.MemoryLimitMB != 64 {
		t.Fatalf("expected default memory limit 64MB, got %d", cfg.MemoryLimitMB)
	}
	if cfg.CPULimit != 100*time.Millisecond {
		t.Fatalf("expected default cpu limit 100ms, got %s", cfg.CPULimit)
	}
	if !cfg.NetworkAllow {
		t.Fatal("expected default network allow true")
	}
	if cfg.FSAllow {
		t.Fatal("expected default fs allow false")
	}
	if cfg.HotReload {
		t.Fatal("expected default hot reload false")
	}
	if cfg.ReloadInterval != 2*time.Second {
		t.Fatalf("expected default reload interval 2s, got %s", cfg.ReloadInterval)
	}
}

func TestValidateConfig(t *testing.T) {
	t.Parallel()

	if err := ValidateConfig(DefaultConfig()); err != nil {
		t.Fatalf("expected default config valid, got %v", err)
	}

	disabled := DefaultConfig()
	disabled.Runtime = "unknown-runtime"
	if err := ValidateConfig(disabled); err != nil {
		t.Fatalf("expected disabled config to skip runtime validation, got %v", err)
	}

	invalidRuntime := DefaultConfig()
	invalidRuntime.Enabled = true
	invalidRuntime.Runtime = "unknown-runtime"
	if err := ValidateConfig(invalidRuntime); err == nil {
		t.Fatal("expected error for unknown runtime")
	}

	invalidMem := DefaultConfig()
	invalidMem.Enabled = true
	invalidMem.MemoryLimitMB = 0
	if err := ValidateConfig(invalidMem); err == nil {
		t.Fatal("expected error for zero memory limit")
	}

	invalidCPU := DefaultConfig()
	invalidCPU.Enabled = true
	invalidCPU.CPULimit = 0
	if err := ValidateConfig(invalidCPU); err == nil {
		t.Fatal("expected error for zero cpu limit")
	}

	invalidReloadInterval := DefaultConfig()
	invalidReloadInterval.Enabled = true
	invalidReloadInterval.ReloadInterval = 0
	if err := ValidateConfig(invalidReloadInterval); err == nil {
		t.Fatal("expected error for zero reload interval")
	}
}

func TestNewRuntime(t *testing.T) {
	t.Parallel()

	rt, err := NewRuntime(RuntimeWasmer)
	if err != nil {
		t.Fatalf("expected wasmer runtime success, got %v", err)
	}
	if !rt.SupportsNetworking() {
		t.Fatal("expected wasmer runtime to report networking support")
	}

	rt, err = NewRuntime(RuntimeWazero)
	if err != nil {
		t.Fatalf("expected wazero runtime success, got %v", err)
	}
	if rt.SupportsNetworking() {
		t.Fatal("expected wazero runtime networking support to be false")
	}

	_, err = NewRuntime("invalid")
	if err == nil {
		t.Fatal("expected unknown runtime error")
	}
}

func TestManagerLifecycleDisabled(t *testing.T) {
	t.Parallel()

	mgr, err := NewManager(DefaultConfig())
	if err != nil {
		t.Fatalf("expected manager create success, got %v", err)
	}
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("expected start success when disabled, got %v", err)
	}
	if mgr.Runtime() != nil {
		t.Fatal("expected no runtime created when disabled")
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("expected close success, got %v", err)
	}
}

func TestManagerLifecycleEnabled(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Enabled = true
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("expected manager create success, got %v", err)
	}
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("expected start success, got %v", err)
	}
	if mgr.Runtime() == nil {
		t.Fatal("expected runtime to be initialized")
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("expected close success, got %v", err)
	}
}

func TestWasmerCompileInstantiateInvoke(t *testing.T) {
	t.Parallel()

	rt := &wasmerRuntime{binary: "wasmer"}
	rt.runner = func(_ context.Context, _ string, _ RuntimeConfig, payload []byte) ([]byte, error) {
		return payload, nil
	}
	if err := rt.Init(context.Background(), RuntimeConfig{MemoryLimitMB: 64, CPULimit: 100 * time.Millisecond, NetworkAllow: true, WorkingDir: t.TempDir()}); err != nil {
		t.Fatalf("runtime init failed: %v", err)
	}

	if _, err := rt.Compile(context.Background(), nil); err == nil {
		t.Fatal("expected compile error for empty wasm module")
	}

	mod, err := rt.Compile(context.Background(), []byte("wasm-bytes"))
	if err != nil {
		t.Fatalf("runtime compile failed: %v", err)
	}
	inst, err := rt.Instantiate(context.Background(), mod, Imports{})
	if err != nil {
		t.Fatalf("runtime instantiate failed: %v", err)
	}
	out, err := inst.Invoke(context.Background(), "handle", []byte(`{"ok":true}`))
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}
	if string(out) != `{"ok":true}` {
		t.Fatalf("expected echoed payload, got %q", out)
	}
	if err := inst.Close(); err != nil {
		t.Fatalf("instance close failed: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("runtime close failed: %v", err)
	}
}

func TestWazeroCompileInstantiateInvoke(t *testing.T) {
	t.Parallel()

	rt, err := NewRuntime(RuntimeWazero)
	if err != nil {
		t.Fatalf("new runtime failed: %v", err)
	}
	if err := rt.Init(context.Background(), RuntimeConfig{MemoryLimitMB: 16, CPULimit: 100 * time.Millisecond, NetworkAllow: false, WorkingDir: t.TempDir()}); err != nil {
		t.Fatalf("runtime init failed: %v", err)
	}
	defer func() { _ = rt.Close() }()

	if _, err := rt.Compile(context.Background(), nil); err == nil {
		t.Fatal("expected compile error for empty wasm module")
	}

	// Minimal valid wasm binary with no imports/exports.
	mod, err := rt.Compile(context.Background(), []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})
	if err != nil {
		t.Fatalf("runtime compile failed: %v", err)
	}
	inst, err := rt.Instantiate(context.Background(), mod, Imports{})
	if err != nil {
		t.Fatalf("runtime instantiate failed: %v", err)
	}
	out, err := inst.Invoke(context.Background(), "handle", []byte(`{"ok":true}`))
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty stdout for minimal module, got %q", out)
	}
	if err := inst.Close(); err != nil {
		t.Fatalf("instance close failed: %v", err)
	}
}

func TestManagerFunctionCRUDAndTrigger(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Dir = t.TempDir()
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	mgr.runtimeFactory = func(_ string) (Runtime, error) {
		rt := &wasmerRuntime{binary: "wasmer"}
		rt.runner = func(_ context.Context, _ string, _ RuntimeConfig, payload []byte) ([]byte, error) {
			return []byte(`{"continue":true,"output":` + string(payload) + `}`), nil
		}
		return rt, nil
	}
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatalf("start manager failed: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	err = mgr.CreateFunction(Function{Name: "put-pre", Runtime: RuntimeWasmer, Trigger: TriggerPutObjectPre, Enabled: true, Module: []byte("abc")})
	if err != nil {
		t.Fatalf("create function failed: %v", err)
	}

	defs := mgr.ListFunctions()
	if len(defs) != 1 || defs[0].Name != "put-pre" {
		t.Fatalf("expected one function summary, got %#v", defs)
	}

	res, err := mgr.Trigger(context.Background(), TriggerPutObjectPre, map[string]any{"bucket": "photos", "key": "a.txt"})
	if err != nil {
		t.Fatalf("trigger failed: %v", err)
	}
	if !res.Continue {
		t.Fatal("expected continue true")
	}
	if len(res.Output) == 0 {
		t.Fatal("expected output payload")
	}
	var parsed map[string]any
	if err := json.Unmarshal(res.Output, &parsed); err != nil {
		t.Fatalf("unmarshal output failed: %v", err)
	}
	if parsed["bucket"] != "photos" {
		t.Fatalf("expected output bucket photos, got %#v", parsed["bucket"])
	}
}

func TestManagerVersioningAndActivate(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.Enabled = true
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}

	if err := mgr.CreateFunction(Function{Name: "fn", Runtime: RuntimeWazero, Trigger: TriggerPutObjectPre, Enabled: true, Module: []byte("v1")}); err != nil {
		t.Fatalf("create function failed: %v", err)
	}
	if err := mgr.UpdateFunction("fn", Function{Runtime: RuntimeWazero, Trigger: TriggerPutObjectPre, Enabled: true, Module: []byte("v2")}); err != nil {
		t.Fatalf("update function failed: %v", err)
	}

	def, err := mgr.GetFunction("fn")
	if err != nil {
		t.Fatalf("get function failed: %v", err)
	}
	if def.Version != 2 || def.ActiveVersion != 2 {
		t.Fatalf("expected active version 2, got version=%d active=%d", def.Version, def.ActiveVersion)
	}

	versions, err := mgr.ListFunctionVersions("fn")
	if err != nil {
		t.Fatalf("list versions failed: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}

	if err := mgr.ActivateFunctionVersion("fn", 1); err != nil {
		t.Fatalf("activate v1 failed: %v", err)
	}
	def, err = mgr.GetFunction("fn")
	if err != nil {
		t.Fatalf("get function failed: %v", err)
	}
	if def.ActiveVersion != 1 || string(def.Module) != "v1" {
		t.Fatalf("expected active v1 module, got active=%d module=%q", def.ActiveVersion, string(def.Module))
	}
}

func TestManagerReloadFromDirCreatesAndVersions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	modulePath := filepath.Join(dir, "hello.wasm")
	if err := os.WriteFile(modulePath, []byte("v1"), 0o600); err != nil {
		t.Fatalf("write module file failed: %v", err)
	}
	manifestPath := filepath.Join(dir, "hello.json")
	manifest := `{"name":"hello","runtime":"wazero","trigger":"onPutObjectPre","enabled":true,"module_path":"hello.wasm"}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest failed: %v", err)
	}

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Dir = dir
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}

	if err := mgr.ReloadFromDir(); err != nil {
		t.Fatalf("reload from dir failed: %v", err)
	}
	def, err := mgr.GetFunction("hello")
	if err != nil {
		t.Fatalf("get function failed: %v", err)
	}
	if def.Version != 1 || def.ActiveVersion != 1 {
		t.Fatalf("expected v1 active, got version=%d active=%d", def.Version, def.ActiveVersion)
	}

	if err := os.WriteFile(modulePath, []byte("v2"), 0o600); err != nil {
		t.Fatalf("write module file v2 failed: %v", err)
	}
	if err := mgr.ReloadFromDir(); err != nil {
		t.Fatalf("reload from dir second pass failed: %v", err)
	}
	def, err = mgr.GetFunction("hello")
	if err != nil {
		t.Fatalf("get function failed: %v", err)
	}
	if def.Version != 2 || def.ActiveVersion != 2 || string(def.Module) != "v2" {
		t.Fatalf("expected v2 active with module v2, got version=%d active=%d module=%q", def.Version, def.ActiveVersion, string(def.Module))
	}
}
