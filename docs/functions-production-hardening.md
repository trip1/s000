# Functions Production Hardening

This guide defines the production hardening posture for the s000 functions runtime.

## Security Review Baseline

- Run with `S000_FUNCTIONS_RUNTIME=wazero` by default for in-process, no-external-binary execution.
- Keep `S000_FUNCTIONS_FS_ALLOW=false` unless a function requires filesystem access.
- Keep `S000_FUNCTIONS_NETWORK_ALLOW=false` unless a function requires outbound network access.
- Use least-privilege trigger design: separate read-only and mutating function sets.
- Require explicit review for any function that can return `continue=false` on pre-hooks.

## Scaling Strategy

- Start with deterministic single-process dispatch and enforce per-function controls:
  - `S000_FUNCTIONS_MAX_CONCURRENT`
  - `S000_FUNCTIONS_RATE_LIMIT_PER_MINUTE`
  - `S000_FUNCTIONS_DAILY_QUOTA`
- Scale horizontally at the service tier (multiple s000 instances) and partition traffic by bucket/tenant.
- Keep function modules small to reduce compile/instantiate latency.

## Monitoring and Alerts

- Use `GET /functions/metrics` to monitor:
  - invocations
  - errors
  - rate-limited denials
  - quota denials
  - concurrent denials
- Use `GET /functions/alerts` for active warnings/critical alerts.
- Configure `S000_FUNCTIONS_ALERT_ERROR_COUNT_THRESHOLD` to tune error alert sensitivity.
- Use `GET /functions/logs` for recent execution and deny events.

## Deployment Patterns

Recommended rollout flow:

1. Register a new version (`PUT /functions/{name}` creates immutable versions).
2. Validate behavior using `POST /functions/{name}/invoke`.
3. Switch active revision with `POST /functions/{name}/activate`.
4. Observe metrics/logs/alerts before wider traffic rollout.
5. Roll back by re-activating a previous stable version.

For config-driven deployments, enable hot reload and store manifests in `S000_FUNCTIONS_DIR`.
