package functions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type wazeroRuntime struct {
	mu      sync.Mutex
	started bool
	cfg     RuntimeConfig
	rt      wazero.Runtime
}

type wazeroCompiledModule struct {
	id  string
	mod wazero.CompiledModule
}

func (m wazeroCompiledModule) ID() string {
	return m.id
}

type wazeroInstance struct {
	runtime *wazeroRuntime
	module  wazeroCompiledModule
	imports Imports
}

func (w *wazeroRuntime) Init(ctx context.Context, cfg RuntimeConfig) error {
	if cfg.MemoryLimitMB <= 0 {
		return fmt.Errorf("functions: runtime memory limit must be > 0")
	}
	if cfg.CPULimit <= 0 {
		return fmt.Errorf("functions: runtime cpu limit must be > 0")
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		return nil
	}
	memoryLimitBytes := int64(cfg.MemoryLimitMB) * 1024 * 1024
	memoryLimitPages := uint32(math.Ceil(float64(memoryLimitBytes) / 65536.0))
	if memoryLimitPages == 0 {
		memoryLimitPages = 1
	}
	runtimeCfg := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(memoryLimitPages).
		WithCloseOnContextDone(true)
	rt := wazero.NewRuntimeWithConfig(ctx, runtimeCfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return fmt.Errorf("functions: initialize wasi for wazero: %w", err)
	}
	w.rt = rt
	w.cfg = cfg
	w.started = true
	return nil
}

func (w *wazeroRuntime) Compile(ctx context.Context, module []byte) (CompiledModule, error) {
	if len(module) == 0 {
		return nil, fmt.Errorf("functions: module bytes are required")
	}

	w.mu.Lock()
	rt := w.rt
	w.mu.Unlock()
	if rt == nil {
		return nil, fmt.Errorf("functions: wazero runtime is not initialized")
	}

	compiled, err := rt.CompileModule(ctx, module)
	if err != nil {
		return nil, fmt.Errorf("functions: compile wazero module: %w", err)
	}
	hash := sha256.Sum256(module)
	return wazeroCompiledModule{id: hex.EncodeToString(hash[:]), mod: compiled}, nil
}

func (w *wazeroRuntime) Instantiate(_ context.Context, module CompiledModule, imports Imports) (Instance, error) {
	compiled, ok := module.(wazeroCompiledModule)
	if !ok {
		return nil, fmt.Errorf("functions: incompatible module type %T", module)
	}
	if compiled.mod == nil {
		return nil, fmt.Errorf("functions: compiled module is nil")
	}
	return &wazeroInstance{runtime: w, module: compiled, imports: imports}, nil
}

func (w *wazeroRuntime) SupportsNetworking() bool {
	return false
}

func (w *wazeroRuntime) Close() error {
	w.mu.Lock()
	rt := w.rt
	w.rt = nil
	w.started = false
	w.mu.Unlock()
	if rt != nil {
		if err := rt.Close(context.Background()); err != nil {
			return err
		}
	}
	return nil
}

func (i *wazeroInstance) Invoke(ctx context.Context, _ string, payload []byte) ([]byte, error) {
	if i.runtime == nil {
		return nil, fmt.Errorf("functions: wazero instance runtime is nil")
	}
	i.runtime.mu.Lock()
	rt := i.runtime.rt
	cfg := i.runtime.cfg
	i.runtime.mu.Unlock()
	if rt == nil {
		return nil, fmt.Errorf("functions: wazero runtime is not initialized")
	}

	invokeCtx, cancel := context.WithTimeout(ctx, cfg.CPULimit)
	defer cancel()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	moduleCfg := wazero.NewModuleConfig().
		WithStdin(bytes.NewReader(payload)).
		WithStdout(stdout).
		WithStderr(stderr)
	for k, v := range i.imports.Environment {
		moduleCfg = moduleCfg.WithEnv(k, v)
	}
	if cfg.FSAllow && cfg.WorkingDir != "" {
		moduleCfg = moduleCfg.WithFSConfig(wazero.NewFSConfig().WithDirMount(cfg.WorkingDir, "/"))
	}

	mod, err := rt.InstantiateModule(invokeCtx, i.module.mod, moduleCfg)
	if err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("functions: wazero instantiate failed: %w: %s", err, stderr.String())
		}
		return nil, fmt.Errorf("functions: wazero instantiate failed: %w", err)
	}
	defer func() { _ = mod.Close(invokeCtx) }()

	return bytes.TrimSpace(stdout.Bytes()), nil
}

func (i *wazeroInstance) Close() error {
	return nil
}
