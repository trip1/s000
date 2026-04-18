package functions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	RuntimeWasmer = "wasmer"
	RuntimeWazero = "wazero"
)

var ErrNotImplemented = errors.New("functions: not implemented")

type Config struct {
	Enabled        bool
	Dir            string
	Runtime        string
	MemoryLimitMB  int
	CPULimit       time.Duration
	NetworkAllow   bool
	FSAllow        bool
	HotReload      bool
	ReloadInterval time.Duration
}

func DefaultConfig() Config {
	return Config{
		Enabled:        false,
		Dir:            "./functions",
		Runtime:        RuntimeWazero,
		MemoryLimitMB:  64,
		CPULimit:       100 * time.Millisecond,
		NetworkAllow:   true,
		FSAllow:        false,
		HotReload:      false,
		ReloadInterval: 2 * time.Second,
	}
}

func ValidateConfig(cfg Config) error {
	if !cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(cfg.Dir) == "" {
		return fmt.Errorf("functions: directory is required")
	}
	if strings.TrimSpace(cfg.Runtime) == "" {
		return fmt.Errorf("functions: runtime is required")
	}
	if cfg.MemoryLimitMB <= 0 {
		return fmt.Errorf("functions: memory limit must be > 0")
	}
	if cfg.CPULimit <= 0 {
		return fmt.Errorf("functions: cpu limit must be > 0")
	}
	if cfg.ReloadInterval <= 0 {
		return fmt.Errorf("functions: reload interval must be > 0")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Runtime)) {
	case RuntimeWasmer, RuntimeWazero:
		return nil
	default:
		return fmt.Errorf("functions: unsupported runtime %q", cfg.Runtime)
	}
}

type RuntimeConfig struct {
	MemoryLimitMB int
	CPULimit      time.Duration
	NetworkAllow  bool
	FSAllow       bool
	WorkingDir    string
}

type Imports struct {
	Environment map[string]string
}

type CompiledModule interface {
	ID() string
}

type Instance interface {
	Invoke(ctx context.Context, function string, payload []byte) ([]byte, error)
	Close() error
}

type Runtime interface {
	Init(ctx context.Context, cfg RuntimeConfig) error
	Compile(ctx context.Context, module []byte) (CompiledModule, error)
	Instantiate(ctx context.Context, module CompiledModule, imports Imports) (Instance, error)
	SupportsNetworking() bool
	Close() error
}
