package functions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

type wasmerRuntime struct {
	mu      sync.Mutex
	started bool
	cfg     RuntimeConfig
	binary  string
	runner  func(context.Context, string, RuntimeConfig, []byte) ([]byte, error)
}

type wasmerCompiledModule struct {
	id   string
	data []byte
}

func (m wasmerCompiledModule) ID() string {
	return m.id
}

type wasmerInstance struct {
	modulePath string
	cfg        RuntimeConfig
	runner     func(context.Context, string, RuntimeConfig, []byte) ([]byte, error)
}

func (i *wasmerInstance) Invoke(ctx context.Context, _ string, payload []byte) ([]byte, error) {
	if i.runner == nil {
		return nil, fmt.Errorf("functions: wasmer runner unavailable")
	}
	invokeCtx, cancel := context.WithTimeout(ctx, i.cfg.CPULimit)
	defer cancel()
	return i.runner(invokeCtx, i.modulePath, i.cfg, payload)
}

func (i *wasmerInstance) Close() error {
	if i.modulePath != "" {
		_ = os.Remove(i.modulePath)
		i.modulePath = ""
	}
	return nil
}

func (w *wasmerRuntime) Init(_ context.Context, cfg RuntimeConfig) error {
	if cfg.MemoryLimitMB <= 0 {
		return fmt.Errorf("functions: runtime memory limit must be > 0")
	}
	if cfg.CPULimit <= 0 {
		return fmt.Errorf("functions: runtime cpu limit must be > 0")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cfg = cfg
	w.started = true
	if w.binary == "" {
		w.binary = "wasmer"
	}
	if w.runner == nil {
		w.runner = func(ctx context.Context, modulePath string, rc RuntimeConfig, payload []byte) ([]byte, error) {
			args := []string{"run", modulePath}
			if rc.NetworkAllow {
				args = append(args, "--net")
			}
			if rc.FSAllow {
				args = append(args, "--dir", rc.WorkingDir)
			}
			cmd := exec.CommandContext(ctx, w.binary, args...)
			cmd.Stdin = bytes.NewReader(payload)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				if stderr.Len() > 0 {
					return nil, fmt.Errorf("functions: wasmer run failed: %w: %s", err, stderr.String())
				}
				return nil, fmt.Errorf("functions: wasmer run failed: %w", err)
			}
			return out, nil
		}
	}
	return nil
}

func (w *wasmerRuntime) Compile(_ context.Context, module []byte) (CompiledModule, error) {
	if len(module) == 0 {
		return nil, fmt.Errorf("functions: module bytes are required")
	}
	hash := sha256.Sum256(module)
	id := hex.EncodeToString(hash[:])
	modCopy := make([]byte, len(module))
	copy(modCopy, module)
	return wasmerCompiledModule{id: id, data: modCopy}, nil
}

func (w *wasmerRuntime) Instantiate(_ context.Context, module CompiledModule, _ Imports) (Instance, error) {
	if module == nil {
		return nil, fmt.Errorf("functions: compiled module is required")
	}
	compiled, ok := module.(wasmerCompiledModule)
	if !ok {
		return nil, fmt.Errorf("functions: incompatible module type %T", module)
	}
	if len(compiled.data) == 0 {
		return nil, fmt.Errorf("functions: compiled module data is empty")
	}
	tempDir := w.cfg.WorkingDir
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return nil, fmt.Errorf("functions: create runtime dir: %w", err)
	}
	modulePath := filepath.Join(tempDir, "fn-"+compiled.id+".wasm")
	if err := os.WriteFile(modulePath, compiled.data, 0o600); err != nil {
		return nil, fmt.Errorf("functions: write module file: %w", err)
	}
	return &wasmerInstance{modulePath: modulePath, cfg: w.cfg, runner: w.runner}, nil
}

func (w *wasmerRuntime) SupportsNetworking() bool {
	return true
}

func (w *wasmerRuntime) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.started = false
	return nil
}
