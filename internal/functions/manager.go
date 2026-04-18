package functions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Manager struct {
	cfg            Config
	runtime        Runtime
	registry       *Registry
	runtimeFactory func(string) (Runtime, error)
	reloadCancel   context.CancelFunc
}

func NewManager(cfg Config) (*Manager, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	return &Manager{cfg: cfg, registry: NewRegistry(nil), runtimeFactory: NewRuntime}, nil
}

func (m *Manager) Start(ctx context.Context) error {
	if !m.cfg.Enabled {
		return nil
	}
	if m.runtime != nil {
		return nil
	}
	rt, err := m.runtimeFactory(m.cfg.Runtime)
	if err != nil {
		return err
	}
	if err := rt.Init(ctx, RuntimeConfig{
		MemoryLimitMB: m.cfg.MemoryLimitMB,
		CPULimit:      m.cfg.CPULimit,
		NetworkAllow:  m.cfg.NetworkAllow,
		FSAllow:       m.cfg.FSAllow,
		WorkingDir:    m.cfg.Dir,
	}); err != nil {
		return err
	}
	m.runtime = rt
	if m.cfg.HotReload {
		if err := m.ReloadFromDir(); err != nil {
			return err
		}
		reloadCtx, cancel := context.WithCancel(context.Background())
		m.reloadCancel = cancel
		go m.reloadLoop(reloadCtx)
	}
	return nil
}

func (m *Manager) reloadLoop(ctx context.Context) {
	ticker := time.NewTicker(m.cfg.ReloadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = m.ReloadFromDir()
		}
	}
}

func (m *Manager) Runtime() Runtime {
	return m.runtime
}

// SetRuntimeForTesting injects a runtime instance for tests.
func (m *Manager) SetRuntimeForTesting(rt Runtime) {
	m.runtime = rt
}

func (m *Manager) Enabled() bool {
	return m.cfg.Enabled
}

func (m *Manager) CreateFunction(def Function) error {
	if !m.cfg.Enabled {
		return fmt.Errorf("functions: runtime is disabled")
	}
	return m.registry.Create(def)
}

func (m *Manager) UpdateFunction(name string, def Function) error {
	if !m.cfg.Enabled {
		return fmt.Errorf("functions: runtime is disabled")
	}
	return m.registry.Update(name, def)
}

func (m *Manager) ActivateFunctionVersion(name string, version int) error {
	if !m.cfg.Enabled {
		return fmt.Errorf("functions: runtime is disabled")
	}
	return m.registry.ActivateVersion(name, version)
}

func (m *Manager) ListFunctionVersions(name string) ([]FunctionVersionSummary, error) {
	if !m.cfg.Enabled {
		return nil, fmt.Errorf("functions: runtime is disabled")
	}
	return m.registry.ListVersions(name)
}

func (m *Manager) DeleteFunction(name string) error {
	if !m.cfg.Enabled {
		return fmt.Errorf("functions: runtime is disabled")
	}
	return m.registry.Delete(name)
}

func (m *Manager) GetFunction(name string) (Function, error) {
	if !m.cfg.Enabled {
		return Function{}, fmt.Errorf("functions: runtime is disabled")
	}
	return m.registry.Get(name)
}

func (m *Manager) ListFunctions() []FunctionSummary {
	if !m.cfg.Enabled {
		return nil
	}
	return m.registry.List()
}

func (m *Manager) Trigger(ctx context.Context, trigger string, payload any) (InvocationResult, error) {
	if !m.cfg.Enabled {
		return InvocationResult{Continue: true}, nil
	}
	defs := m.registry.ByTrigger(trigger)
	if len(defs) == 0 {
		return InvocationResult{Continue: true}, nil
	}
	rt := m.runtime
	if rt == nil {
		return InvocationResult{}, fmt.Errorf("functions: runtime is not started")
	}
	input, err := json.Marshal(payload)
	if err != nil {
		return InvocationResult{}, fmt.Errorf("functions: marshal payload: %w", err)
	}
	result := InvocationResult{Continue: true}
	for _, def := range defs {
		compiled, err := rt.Compile(ctx, def.Module)
		if err != nil {
			return InvocationResult{}, fmt.Errorf("functions: compile %s: %w", def.Name, err)
		}
		inst, err := rt.Instantiate(ctx, compiled, Imports{Environment: map[string]string{"S000_FUNCTION_NAME": def.Name}})
		if err != nil {
			return InvocationResult{}, fmt.Errorf("functions: instantiate %s: %w", def.Name, err)
		}
		out, invokeErr := inst.Invoke(ctx, "handle", input)
		_ = inst.Close()
		if invokeErr != nil {
			return InvocationResult{}, fmt.Errorf("functions: invoke %s: %w", def.Name, invokeErr)
		}
		if len(out) == 0 {
			continue
		}
		var parsed InvocationResult
		if err := json.Unmarshal(out, &parsed); err == nil {
			if parsed.Output != nil {
				result.Output = parsed.Output
			}
			if !parsed.Continue {
				result.Continue = false
				return result, nil
			}
			continue
		}
		result.Output = append([]byte(nil), out...)
	}
	return result, nil
}

func (m *Manager) Close() error {
	if m.reloadCancel != nil {
		m.reloadCancel()
		m.reloadCancel = nil
	}
	if m.runtime == nil {
		return nil
	}
	err := m.runtime.Close()
	m.runtime = nil
	return err
}

type fileFunctionSpec struct {
	Name         string `json:"name"`
	Runtime      string `json:"runtime"`
	Trigger      string `json:"trigger"`
	Enabled      bool   `json:"enabled"`
	ModulePath   string `json:"module_path"`
	ModuleBase64 string `json:"module_base64"`
}

func (m *Manager) ReloadFromDir() error {
	if !m.cfg.Enabled {
		return nil
	}
	pattern := filepath.Join(m.cfg.Dir, "*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("functions: glob manifest files: %w", err)
	}
	for _, manifestPath := range files {
		if err := m.reloadManifest(manifestPath); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) reloadManifest(manifestPath string) error {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("functions: read manifest %s: %w", manifestPath, err)
	}
	var spec fileFunctionSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("functions: parse manifest %s: %w", manifestPath, err)
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return fmt.Errorf("functions: manifest %s missing name", manifestPath)
	}
	module, err := decodeManifestModule(spec, manifestPath)
	if err != nil {
		return err
	}
	if spec.Runtime == "" {
		spec.Runtime = m.cfg.Runtime
	}
	cur, getErr := m.registry.Get(name)
	if getErr != nil {
		return m.registry.Create(Function{
			Name:    name,
			Runtime: spec.Runtime,
			Trigger: spec.Trigger,
			Enabled: spec.Enabled,
			Module:  module,
		})
	}
	if cur.Runtime == spec.Runtime && cur.Trigger == spec.Trigger && cur.Enabled == spec.Enabled && bytesEqual(cur.Module, module) {
		return nil
	}
	return m.registry.Update(name, Function{
		Name:    name,
		Runtime: spec.Runtime,
		Trigger: spec.Trigger,
		Enabled: spec.Enabled,
		Module:  module,
	})
}

func decodeManifestModule(spec fileFunctionSpec, manifestPath string) ([]byte, error) {
	if strings.TrimSpace(spec.ModuleBase64) != "" {
		decoded, err := decodeBase64(spec.ModuleBase64)
		if err != nil {
			return nil, fmt.Errorf("functions: decode module_base64 in %s: %w", manifestPath, err)
		}
		return decoded, nil
	}
	if strings.TrimSpace(spec.ModulePath) == "" {
		return nil, fmt.Errorf("functions: manifest %s missing module_path or module_base64", manifestPath)
	}
	modulePath := spec.ModulePath
	if !filepath.IsAbs(modulePath) {
		modulePath = filepath.Join(filepath.Dir(manifestPath), modulePath)
	}
	mod, err := os.ReadFile(modulePath)
	if err != nil {
		return nil, fmt.Errorf("functions: read module file %s: %w", modulePath, err)
	}
	return mod, nil
}

func decodeBase64(v string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(v)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
