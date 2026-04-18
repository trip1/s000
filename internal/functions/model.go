package functions

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	TriggerPutObjectPre  = "onPutObjectPre"
	TriggerPutObjectPost = "onPutObjectPost"
)

type Function struct {
	Name          string    `json:"name"`
	Runtime       string    `json:"runtime"`
	Trigger       string    `json:"trigger"`
	Priority      int       `json:"priority"`
	Enabled       bool      `json:"enabled"`
	Module        []byte    `json:"-"`
	Version       int       `json:"version"`
	ActiveVersion int       `json:"active_version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type FunctionSummary struct {
	Name          string    `json:"name"`
	Runtime       string    `json:"runtime"`
	Trigger       string    `json:"trigger"`
	Priority      int       `json:"priority"`
	Enabled       bool      `json:"enabled"`
	Version       int       `json:"version"`
	ActiveVersion int       `json:"active_version"`
	SizeBytes     int       `json:"size_bytes"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type FunctionVersionSummary struct {
	Name      string    `json:"name"`
	Version   int       `json:"version"`
	Runtime   string    `json:"runtime"`
	Trigger   string    `json:"trigger"`
	Priority  int       `json:"priority"`
	Enabled   bool      `json:"enabled"`
	SizeBytes int       `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

type InvocationResult struct {
	Continue bool            `json:"continue"`
	Output   json.RawMessage `json:"output,omitempty"`
}

type Registry struct {
	mu   sync.RWMutex
	now  func() time.Time
	defs map[string]*functionRecord
}

type functionRecord struct {
	name      string
	createdAt time.Time
	updatedAt time.Time
	active    int
	versions  map[int]Function
}

func NewRegistry(now func() time.Time) *Registry {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Registry{now: now, defs: make(map[string]*functionRecord)}
}

func validateTrigger(trigger string) error {
	switch strings.TrimSpace(trigger) {
	case TriggerPutObjectPre, TriggerPutObjectPost:
		return nil
	case TriggerHTTPPre, TriggerHTTPPost:
		return nil
	case TriggerCronTick:
		return nil
	default:
		return fmt.Errorf("functions: unsupported trigger %q", trigger)
	}
}

func (r *Registry) Create(def Function) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := strings.TrimSpace(def.Name)
	if name == "" {
		return fmt.Errorf("functions: name is required")
	}
	if _, ok := r.defs[name]; ok {
		return fmt.Errorf("functions: function %q already exists", name)
	}
	if err := validateTrigger(def.Trigger); err != nil {
		return err
	}
	if len(def.Module) == 0 {
		return fmt.Errorf("functions: module is required")
	}
	if def.Priority <= 0 {
		def.Priority = 100
	}
	now := r.now()
	def.Name = name
	if def.Runtime == "" {
		def.Runtime = RuntimeWazero
	}
	def.Version = 1
	def.ActiveVersion = 1
	def.CreatedAt = now
	def.UpdatedAt = now
	r.defs[name] = &functionRecord{
		name:      name,
		createdAt: now,
		updatedAt: now,
		active:    1,
		versions:  map[int]Function{1: def},
	}
	return nil
}

func (r *Registry) Update(name string, def Function) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	name = strings.TrimSpace(name)
	rec, ok := r.defs[name]
	if !ok {
		return fmt.Errorf("functions: function %q not found", name)
	}
	cur, ok := rec.versions[rec.active]
	if !ok {
		return fmt.Errorf("functions: function %q has no active version", name)
	}
	if def.Trigger == "" {
		def.Trigger = cur.Trigger
	}
	if err := validateTrigger(def.Trigger); err != nil {
		return err
	}
	if def.Runtime == "" {
		def.Runtime = cur.Runtime
	}
	if def.Priority <= 0 {
		def.Priority = cur.Priority
	}
	if len(def.Module) == 0 {
		def.Module = cur.Module
	}
	nextVersion := rec.active + 1
	def.Name = name
	def.Version = nextVersion
	def.ActiveVersion = nextVersion
	def.CreatedAt = r.now()
	def.UpdatedAt = def.CreatedAt
	rec.versions[nextVersion] = def
	rec.active = nextVersion
	rec.updatedAt = def.UpdatedAt
	return nil
}

func (r *Registry) Delete(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	name = strings.TrimSpace(name)
	if _, ok := r.defs[name]; !ok {
		return fmt.Errorf("functions: function %q not found", name)
	}
	delete(r.defs, name)
	return nil
}

func (r *Registry) Get(name string) (Function, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	name = strings.TrimSpace(name)
	rec, ok := r.defs[name]
	if !ok {
		return Function{}, fmt.Errorf("functions: function %q not found", name)
	}
	def, ok := rec.versions[rec.active]
	if !ok {
		return Function{}, fmt.Errorf("functions: function %q has no active version", name)
	}
	def.ActiveVersion = rec.active
	def.CreatedAt = rec.createdAt
	def.UpdatedAt = rec.updatedAt
	return def, nil
}

func (r *Registry) List() []FunctionSummary {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]FunctionSummary, 0, len(r.defs))
	for _, rec := range r.defs {
		v, ok := rec.versions[rec.active]
		if !ok {
			continue
		}
		out = append(out, FunctionSummary{
			Name:          v.Name,
			Runtime:       v.Runtime,
			Trigger:       v.Trigger,
			Priority:      v.Priority,
			Enabled:       v.Enabled,
			Version:       v.Version,
			ActiveVersion: rec.active,
			SizeBytes:     len(v.Module),
			CreatedAt:     rec.createdAt,
			UpdatedAt:     rec.updatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Version < out[j].Version
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (r *Registry) ByTrigger(trigger string) []Function {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Function, 0)
	for _, rec := range r.defs {
		v, ok := rec.versions[rec.active]
		if !ok {
			continue
		}
		if v.Enabled && v.Trigger == trigger {
			v.ActiveVersion = rec.active
			v.CreatedAt = rec.createdAt
			v.UpdatedAt = rec.updatedAt
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		if out[i].Name == out[j].Name {
			return out[i].Version < out[j].Version
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (r *Registry) ListVersions(name string) ([]FunctionVersionSummary, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	name = strings.TrimSpace(name)
	rec, ok := r.defs[name]
	if !ok {
		return nil, fmt.Errorf("functions: function %q not found", name)
	}
	out := make([]FunctionVersionSummary, 0, len(rec.versions))
	for _, v := range rec.versions {
		out = append(out, FunctionVersionSummary{
			Name:      v.Name,
			Version:   v.Version,
			Runtime:   v.Runtime,
			Trigger:   v.Trigger,
			Priority:  v.Priority,
			Enabled:   v.Enabled,
			SizeBytes: len(v.Module),
			CreatedAt: v.CreatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func (r *Registry) ActivateVersion(name string, version int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	name = strings.TrimSpace(name)
	rec, ok := r.defs[name]
	if !ok {
		return fmt.Errorf("functions: function %q not found", name)
	}
	if _, ok := rec.versions[version]; !ok {
		return fmt.Errorf("functions: function %q version %d not found", name, version)
	}
	rec.active = version
	rec.updatedAt = r.now()
	return nil
}
