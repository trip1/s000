package functions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	cfg            Config
	runtime        Runtime
	registry       *Registry
	runtimeFactory func(string) (Runtime, error)
	reloadCancel   context.CancelFunc
	mu             sync.Mutex
	metrics        map[string]*FunctionMetric
	logs           []FunctionLogEntry
	guards         map[string]*functionGuard
}

type FunctionMetric struct {
	Function         string        `json:"function"`
	Invocations      uint64        `json:"invocations"`
	Errors           uint64        `json:"errors"`
	RateLimited      uint64        `json:"rate_limited"`
	QuotaExceeded    uint64        `json:"quota_exceeded"`
	ConcurrentDenied uint64        `json:"concurrent_denied"`
	LastDuration     time.Duration `json:"last_duration"`
	TotalDuration    time.Duration `json:"total_duration"`
	LastInvokedAt    time.Time     `json:"last_invoked_at,omitempty"`
	LastError        string        `json:"last_error,omitempty"`
	LastStackTrace   string        `json:"last_stack_trace,omitempty"`
}

type FunctionLogEntry struct {
	Timestamp  time.Time     `json:"timestamp"`
	Function   string        `json:"function"`
	Trigger    string        `json:"trigger"`
	Outcome    string        `json:"outcome"`
	Duration   time.Duration `json:"duration"`
	Error      string        `json:"error,omitempty"`
	StackTrace string        `json:"stack_trace,omitempty"`
}

type FunctionAlert struct {
	Function  string    `json:"function"`
	Severity  string    `json:"severity"`
	Reason    string    `json:"reason"`
	Triggered time.Time `json:"triggered"`
}

type functionGuard struct {
	minuteWindowStart time.Time
	minuteCount       int
	dayWindowStart    time.Time
	dayCount          int
	inflight          int
}

func NewManager(cfg Config) (*Manager, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	return &Manager{cfg: cfg, registry: NewRegistry(nil), runtimeFactory: NewRuntime, metrics: make(map[string]*FunctionMetric), guards: make(map[string]*functionGuard)}, nil
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
		out, invokeErr := m.invokeOne(ctx, rt, trigger, def, input)
		if invokeErr != nil {
			return InvocationResult{}, invokeErr
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

func (m *Manager) InvokeFunction(ctx context.Context, name string, payload json.RawMessage) (InvocationResult, error) {
	if !m.cfg.Enabled {
		return InvocationResult{}, fmt.Errorf("functions: runtime is disabled")
	}
	def, err := m.registry.Get(name)
	if err != nil {
		return InvocationResult{}, err
	}
	rt := m.runtime
	if rt == nil {
		return InvocationResult{}, fmt.Errorf("functions: runtime is not started")
	}
	out, err := m.invokeOne(ctx, rt, "manual", def, payload)
	if err != nil {
		return InvocationResult{}, err
	}
	if len(out) == 0 {
		return InvocationResult{Continue: true}, nil
	}
	var parsed InvocationResult
	if err := json.Unmarshal(out, &parsed); err == nil {
		return parsed, nil
	}
	return InvocationResult{Continue: true, Output: append([]byte(nil), out...)}, nil
}

func (m *Manager) invokeOne(ctx context.Context, rt Runtime, trigger string, def Function, input []byte) (out []byte, err error) {
	if guardErr := m.acquireInvocationSlot(def.Name); guardErr != nil {
		m.recordDenied(def.Name, trigger, guardErr)
		return nil, fmt.Errorf("functions: invoke %s: %w", def.Name, guardErr)
	}
	defer m.releaseInvocationSlot(def.Name)

	start := time.Now()
	var stack string
	defer func() {
		dur := time.Since(start)
		if rec := recover(); rec != nil {
			stack = string(debug.Stack())
			err = fmt.Errorf("functions: panic in %s: %v", def.Name, rec)
		}
		m.recordInvocation(def.Name, trigger, dur, err, stack)
		if err != nil {
			slog.Warn("function invocation failed", "function", def.Name, "trigger", trigger, "error", err)
		}
	}()

	compiled, err := rt.Compile(ctx, def.Module)
	if err != nil {
		return nil, fmt.Errorf("functions: compile %s: %w", def.Name, err)
	}
	inst, err := rt.Instantiate(ctx, compiled, Imports{Environment: map[string]string{"S000_FUNCTION_NAME": def.Name, "S000_FUNCTION_TRIGGER": trigger}})
	if err != nil {
		return nil, fmt.Errorf("functions: instantiate %s: %w", def.Name, err)
	}
	defer func() { _ = inst.Close() }()
	out, err = inst.Invoke(ctx, "handle", input)
	if err != nil {
		return nil, fmt.Errorf("functions: invoke %s: %w", def.Name, err)
	}
	return out, nil
}

func (m *Manager) acquireInvocationSlot(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	g, ok := m.guards[name]
	if !ok {
		g = &functionGuard{minuteWindowStart: now, dayWindowStart: now}
		m.guards[name] = g
	}
	if m.cfg.MaxConcurrent > 0 && g.inflight >= m.cfg.MaxConcurrent {
		return ErrConcurrentLimit
	}
	if m.cfg.RateLimitPerMinute > 0 {
		if now.Sub(g.minuteWindowStart) >= time.Minute {
			g.minuteWindowStart = now
			g.minuteCount = 0
		}
		if g.minuteCount >= m.cfg.RateLimitPerMinute {
			return ErrRateLimited
		}
		g.minuteCount++
	}
	if m.cfg.DailyInvocationQuota > 0 {
		if now.Sub(g.dayWindowStart) >= 24*time.Hour {
			g.dayWindowStart = now
			g.dayCount = 0
		}
		if g.dayCount >= m.cfg.DailyInvocationQuota {
			return ErrQuotaExceeded
		}
		g.dayCount++
	}
	g.inflight++
	return nil
}

func (m *Manager) releaseInvocationSlot(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.guards[name]
	if !ok {
		return
	}
	if g.inflight > 0 {
		g.inflight--
	}
}

func (m *Manager) recordInvocation(name string, trigger string, duration time.Duration, err error, stack string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	metric, ok := m.metrics[name]
	if !ok {
		metric = &FunctionMetric{Function: name}
		m.metrics[name] = metric
	}
	metric.Invocations++
	metric.LastDuration = duration
	metric.TotalDuration += duration
	metric.LastInvokedAt = time.Now().UTC()
	outcome := "ok"
	errText := ""
	if err != nil {
		metric.Errors++
		metric.LastError = err.Error()
		metric.LastStackTrace = stack
		outcome = "error"
		errText = err.Error()
	}
	m.logs = append(m.logs, FunctionLogEntry{
		Timestamp:  metric.LastInvokedAt,
		Function:   name,
		Trigger:    trigger,
		Outcome:    outcome,
		Duration:   duration,
		Error:      errText,
		StackTrace: stack,
	})
	if len(m.logs) > 200 {
		m.logs = m.logs[len(m.logs)-200:]
	}
}

func (m *Manager) Metrics() []FunctionMetric {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]FunctionMetric, 0, len(m.metrics))
	for _, v := range m.metrics {
		out = append(out, *v)
	}
	return out
}

func (m *Manager) Alerts() []FunctionAlert {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	threshold := m.cfg.AlertErrorCountThreshold
	if threshold == 0 {
		threshold = 10
	}
	alerts := make([]FunctionAlert, 0)
	for _, metric := range m.metrics {
		if metric.Errors >= threshold {
			alerts = append(alerts, FunctionAlert{Function: metric.Function, Severity: "warning", Reason: "error_count_threshold", Triggered: now})
		}
		if metric.RateLimited > 0 {
			alerts = append(alerts, FunctionAlert{Function: metric.Function, Severity: "info", Reason: "rate_limited", Triggered: now})
		}
		if metric.QuotaExceeded > 0 {
			alerts = append(alerts, FunctionAlert{Function: metric.Function, Severity: "critical", Reason: "quota_exceeded", Triggered: now})
		}
	}
	return alerts
}

func (m *Manager) RecentLogs(limit int) []FunctionLogEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > len(m.logs) {
		limit = len(m.logs)
	}
	start := len(m.logs) - limit
	out := make([]FunctionLogEntry, limit)
	copy(out, m.logs[start:])
	return out
}

func (m *Manager) recordDenied(name string, trigger string, denyErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	metric, ok := m.metrics[name]
	if !ok {
		metric = &FunctionMetric{Function: name}
		m.metrics[name] = metric
	}
	switch denyErr {
	case ErrRateLimited:
		metric.RateLimited++
	case ErrQuotaExceeded:
		metric.QuotaExceeded++
	case ErrConcurrentLimit:
		metric.ConcurrentDenied++
	}
	m.logs = append(m.logs, FunctionLogEntry{
		Timestamp: time.Now().UTC(),
		Function:  name,
		Trigger:   trigger,
		Outcome:   "denied",
		Duration:  0,
		Error:     denyErr.Error(),
	})
	if len(m.logs) > 200 {
		m.logs = m.logs[len(m.logs)-200:]
	}
}

func (m *Manager) TriggerS3(ctx context.Context, trigger string, event S3Event) (InvocationResult, error) {
	if event.Type == "" {
		event.Type = EventTypeS3
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	return m.Trigger(ctx, trigger, event)
}

func (m *Manager) TriggerHTTP(ctx context.Context, trigger string, event HTTPEvent) (InvocationResult, error) {
	if event.Type == "" {
		event.Type = EventTypeHTTP
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	return m.Trigger(ctx, trigger, event)
}

func (m *Manager) TriggerCron(ctx context.Context, trigger string, event CronEvent) (InvocationResult, error) {
	if event.Type == "" {
		event.Type = EventTypeCron
	}
	if event.Scheduled.IsZero() {
		event.Scheduled = time.Now().UTC()
	}
	return m.Trigger(ctx, trigger, event)
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
	Priority     int    `json:"priority"`
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
			Name:     name,
			Runtime:  spec.Runtime,
			Trigger:  spec.Trigger,
			Priority: spec.Priority,
			Enabled:  spec.Enabled,
			Module:   module,
		})
	}
	if cur.Runtime == spec.Runtime && cur.Trigger == spec.Trigger && cur.Priority == spec.Priority && cur.Enabled == spec.Enabled && bytesEqual(cur.Module, module) {
		return nil
	}
	return m.registry.Update(name, Function{
		Name:     name,
		Runtime:  spec.Runtime,
		Trigger:  spec.Trigger,
		Priority: spec.Priority,
		Enabled:  spec.Enabled,
		Module:   module,
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
