# s000

s000 is a self-hosted, S3-compatible object storage service in Go.

Current status: project foundation and initial test scaffolding.

## Quickstart
- Run unit tests: `make test`
- Run integration tests: `make integration`
- Run lint checks: `make lint`
- Run benchmarks: `make bench`
- Generate CPU/memory profiles: `make profile`
- Cross-build binaries: `make cross-build`
- Build release tarballs + checksums: `make release-artifacts`
- Run dev watch mode: `make watch` (requires `air`)
- Start local server: `go run ./cmd/s000`

The server currently exposes:
- `GET /healthz`
- `GET /readyz`

Current core API behavior:
- S3-style routes are wired for path-style and optional virtual-host style routing (`S000_DOMAIN`).
- Non-health endpoints use SigV4 verification.
- Bucket APIs include create/list/delete (empty bucket), location, versioning, and ListObjectsV2.
- Object APIs include put/get/head/delete, copy, range reads, user metadata headers, and checksum/ETag headers.
- Multipart APIs include create/upload-part/list-parts/complete/abort and list multipart uploads.
- Metadata backend selection is wired (`S000_METADATA_BACKEND`) with compatibility-layer adapters for `sqlite`, `libsql`, `postgresql`, `mariadb`, and `valkey`.

Selected environment variables:
- `S000_ADDR` (default `:9000`)
- `S000_DOMAIN` (optional virtual-host suffix, example `s000.local`)
- `S000_MAX_INFLIGHT` (default `128`)
- `S000_SHUTDOWN_TIMEOUT` (default `10s`)
- `S000_AUTH_MAX_SKEW` (default `15m`)
- `S000_ADMIN_ACCESS_KEY` + `S000_ADMIN_SECRET_KEY` (initial admin bootstrap credential)
- `S000_METADATA_BACKEND` (default `sqlite`)
- `S000_METADATA_DSN` (default `file:./data/s000-metadata.db`)
- `S000_VALKEY_ADDR` (default `127.0.0.1:6379`)
- `S000_METADATA_CONNECT_TIMEOUT` (default `3s`)
- `S000_LIFECYCLE_RULES` (optional lifecycle rules; format: `prefix=<prefix>,age=<duration>[;prefix=<prefix>,age=<duration>]`)
- `S000_LIFECYCLE_INTERVAL` (default `5m`)
- `S000_LIFECYCLE_BATCH_SIZE` (default `100`)
- `S000_LIFECYCLE_MAX_RETRIES` (default `3`)
- `S000_LIFECYCLE_BACKOFF` (default `200ms`)
- `S000_LIFECYCLE_DRY_RUN` (default `false`)
- `S000_METRICS_PATH` (default `/metrics`)
- `S000_TRACING_ENABLED` (default `false`)
- `S000_HTTP_READ_HEADER_TIMEOUT` (default `5s`)
- `S000_HTTP_READ_TIMEOUT` (default `30s`)
- `S000_HTTP_WRITE_TIMEOUT` (default `30s`)
- `S000_HTTP_IDLE_TIMEOUT` (default `60s`)
- `S000_HTTP_MAX_HEADER_BYTES` (default `1048576`)
- `S000_HTTP_DISABLE_KEEPALIVE` (default `false`)
- `S000_HEAVY_OPS_WORKERS` (default `4`)
- `S000_HEAVY_OPS_QUEUE` (default `64`)

## Debug Endpoints

When lifecycle rules are configured, authenticated admin/debug endpoints are available:

- `GET /debug/lifecycle/config`
  - Returns lifecycle worker configuration and parsed rules.
- `GET /debug/lifecycle/metrics`
  - Returns cumulative lifecycle run counters (`runs`, scanned/eligible/deleted/failed totals, and last-run info).

If the lifecycle worker is not configured, these endpoints return `503` with a JSON error payload.

## Metrics Endpoint

- `GET /metrics` (or custom path via `S000_METRICS_PATH`)
  - Prometheus metrics for request totals, latency, errors, bytes in/out, and worker queue depth.

See `SPEC.md` for project scope and `todo.md` for the release checklist.
See `docs/observability-spec.md` and `docs/performance-baseline.md` for production operations details.
See `docs/release-artifacts.md` for reproducible packaging details.
