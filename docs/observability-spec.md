# Observability Spec (Section 9)

This document defines the Release 1 observability baseline for `s000`.

## Goals

- Provide structured request logs with enough context to debug failures quickly.
- Expose Prometheus-compatible metrics for API traffic and background workers.
- Ensure health and readiness endpoints reflect real dependency state.
- Keep tracing integration optional and disabled by default.

## Structured Logging

Every completed HTTP request log entry must include:

- `request_id`
- `principal` (access key ID when derivable from auth input)
- `bucket`
- `key`
- `method`
- `path`
- `status`
- `duration_ms`
- `bytes_in`
- `bytes_out`

## Metrics Endpoint

- Default endpoint path: `/metrics` (override with `S000_METRICS_PATH`).
- Format: Prometheus text exposition (`text/plain; version=0.0.4`).
- Metrics must include:
  - request total counter
  - request latency histogram
  - request error counter
  - request bytes in/out counters
  - worker queue depth gauge

## Health and Readiness

Endpoints:

- `GET /healthz`
- `GET /readyz`

Behavior:

- Return `200` when metadata and blob checks pass.
- Return `503` when dependency checks fail.
- Readiness includes backend connection ping checks.

## Tracing Hooks

- Feature flag: `S000_TRACING_ENABLED`.
- When enabled, request middleware calls tracing hooks.
- Current default implementation is no-op, designed to be replaced by OTel-compatible hooks later.

## Environment Variables

- `S000_METRICS_PATH` (default `/metrics`)
- `S000_TRACING_ENABLED` (default `false`)
