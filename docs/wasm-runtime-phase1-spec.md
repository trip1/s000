# WASM Runtime Phase 1 Spec

This document defines the Phase 1 implementation boundary for the WASM edge runtime.

## Scope

Phase 1 delivers runtime foundation only:

- Runtime abstraction interfaces
- Wazero-first runtime selection with optional Wasmer
- Function runtime configuration and validation
- Basic manager lifecycle (`start`/`stop`)
- Stub compile/instantiate lifecycle for future execution wiring

Phase 1 does **not** yet execute user wasm bytecode against S3 events.

## Design Goals

- Keep runtime pluggable so additional engines can be added later.
- Default to `wazero` (pure Go) and keep `wasmer` optional.
- Keep configuration explicit and validated at startup.
- Provide predictable errors for unsupported/unimplemented operations.

## Public Types (internal package)

Package: `internal/functions`

- `type Config`
  - `Enabled bool`
  - `Dir string`
  - `Runtime string` (default `wazero`)
  - `MemoryLimitMB int` (default `64`)
  - `CPULimit time.Duration` (default `100ms`)
  - `NetworkAllow bool` (default `true`)
  - `FSAllow bool` (default `false`)

- `func ValidateConfig(Config) error`

- `type Runtime interface`
  - `Init(context.Context, RuntimeConfig) error`
  - `Compile(context.Context, []byte) (CompiledModule, error)`
  - `Instantiate(context.Context, CompiledModule, Imports) (Instance, error)`
  - `SupportsNetworking() bool`
  - `Close() error`

- `type Manager`
  - `func NewManager(Config) (*Manager, error)`
  - `func (m *Manager) Start(context.Context) error`
  - `func (m *Manager) Close() error`
  - `func (m *Manager) Runtime() Runtime`

## Runtime Selection

Runtime names accepted in Phase 1:

- `wazero` (first-class pure-Go runtime)
- `wasmer` (optional external CLI runtime)

Planned (not yet implemented):

- `wasmtime`

Unknown runtime names return startup validation errors.

## Runtime Adapter Behavior (Phase 1)

### Wazero

- `SupportsNetworking()` returns `false`
- `Init(...)` initializes wazero + WASI runtime in-process
- `Compile(...)` compiles wasm bytes in-memory
- `Instantiate(...)` creates invocation instance handles
- `Invoke(...)` executes module with stdin/stdout wiring

### Wasmer

The Wasmer adapter in this phase is a foundation adapter:

- `SupportsNetworking()` returns `true`
- `Init(...)` validates runtime config and records it
- `Compile(...)` validates non-empty module bytes and returns a compiled handle
- `Instantiate(...)` returns a runtime instance handle
- `Invoke(...)` executes module via `wasmer run` and stdin/stdout

This lets the rest of the server integrate runtime lifecycle now while execution hooks are added later.

## Configuration Source

New server env vars:

- `S000_FUNCTIONS_ENABLED` (default `false`)
- `S000_FUNCTIONS_DIR` (default `./functions`)
- `S000_FUNCTIONS_RUNTIME` (default `wazero`; supported: `wazero`, `wasmer`)
- `S000_FUNCTIONS_MEMORY_LIMIT` (default `64` MB)
- `S000_FUNCTIONS_CPU_LIMIT` (default `100ms`)
- `S000_FUNCTIONS_NETWORK_ALLOW` (default `true`)
- `S000_FUNCTIONS_FS_ALLOW` (default `false`)

Invalid values fall back to safe defaults in config loading, consistent with existing config behavior.

## Startup Integration

When `S000_FUNCTIONS_ENABLED=true`:

1. Build `functions.Config` from loaded server config.
2. Create `functions.Manager`.
3. Start manager before serving HTTP.
4. Stop manager during shutdown.

If startup validation fails, server exits with error.

## Test Plan (Phase 1)

- Config tests for new env vars and fallback behavior.
- Runtime config validation tests (`enabled` + invalid runtime, bad limits).
- Runtime factory tests (`wazero` + `wasmer` supported, unknown runtime rejected).
- Manager lifecycle tests (`Start`, `Close`, and no-op behavior when disabled).
- Wasmer adapter tests for compile/instantiate scaffold behavior and deterministic errors.
