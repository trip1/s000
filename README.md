# s000

s000 is a self-hosted, S3-compatible object storage service in Go.

Current status: Release 1 implementation is substantially complete, including API, CLI tooling, and embedded HTML client scaffolding.

## Why s000
- Run S3-compatible storage on a single node with simple local operations.
- Keep deployment choices flexible with pluggable metadata backends (`sqlite`, `libsql`, `postgresql`, `mariadb`, `valkey`).
- Operate with explicit controls: SigV4 auth, health/readiness probes, metrics, backup/restore workflows, and a built-in admin UI.

## Who this is for
- Teams that want a self-hosted object store for dev environments, labs, edge nodes, or internal services.
- Operators who prefer simple deployment and direct control over storage/data layout.

## Quickstart

Install the latest release on a Linux or FreeBSD machine:

```bash
curl -fsSL https://raw.githubusercontent.com/trip1/s000/master/install.sh | sudo bash -s -- \
  --access-key admin \
  --secret-key 'change-me-before-production'
```

The installer downloads the matching release artifact, verifies `checksums.txt`, installs `s000`, creates the service user/data directory, and configures a service when `systemd` or OpenRC is available.

Verify the service:

```bash
curl -fsS http://127.0.0.1:9000/healthz
curl -fsS http://127.0.0.1:9000/readyz
```

Open the web UI and log in with the access key and secret key used during install:

```text
http://127.0.0.1:9000/app/login
```

For production, replace the example credentials with strong secrets before first start. Full deployment and production setup: `docs/quickstart.md`. Installer options and init-system details: `install.sh --help`.

## Project Status and Scope
- Stability: pre-`v1`; APIs may evolve as compatibility and operational hardening continue.
- Non-health routes are protected by SigV4 verification middleware.
- Primary scope is single-node operation and S3 compatibility for common workflows.
- Known gaps and constraints are tracked in `docs/limitations.md`.

## Security and Responsible Disclosure
- Security policy and private reporting workflow: `SECURITY.md`.
- Security hardening, threat model, and review artifacts: `docs/security-hardening-spec.md`, `docs/threat-model.md`, `docs/security-review-checklist.md`.

## Contributing
- Contribution workflow and local validation commands: `CONTRIBUTING.md`.
- Additional contributor notes: `docs/contributing.md`.

## Development Commands
- Set local admin bootstrap credentials (used by API auth and UI login):
  - `export S000_ADMIN_ACCESS_KEY=admin`
  - `export S000_ADMIN_SECRET_KEY=secret`
- Start local server: `go run ./cmd/s000`
- Optional import from existing directories: `go run ./cmd/s000 --import-directory ./seed-data`
- Validate service health: `go run ./cmd/s000ctl health-inspect --endpoint http://127.0.0.1:9000`
- Open UI login: `http://127.0.0.1:9000/app/login`
- Run unit tests: `make test`
- Run integration tests: `make integration`
- Run lint checks: `make lint`
- Run benchmarks: `make bench`
- Generate CPU/memory profiles: `make profile`
- Cross-build binaries: `make cross-build`
- Build release tarballs + checksums: `make release-artifacts`
- Create local backup snapshot: `make backup DATA_DIR=./data METADATA_DSN='file:./data/s000-metadata.db' OUT=./backup`
- Validate backup restore layout: `make restore-validate BACKUP=./backup`
- Inspect service health from CLI: `make cli-health-inspect ENDPOINT=http://127.0.0.1:9000`
- Print shell completion snippet: `make cli-completion SHELL=bash`
- Run aws-cli compatibility flow: `make compatibility-awscli`
- Run SDK compatibility smokes: `make compatibility-sdk-go`, `make compatibility-sdk-python`, `make compatibility-sdk-js`
- Validate low-resource profile (2 CPU / 4GB): `make validate-low-resource`
- Run dev watch mode: `make watch` (requires `air`)
- Full deployment + ops + tuning walkthrough: `docs/quickstart.md`, `docs/operations-guide.md`, `docs/tuning-guide.md`

The server currently exposes:
- `GET /healthz`
- `GET /readyz`
- embedded HTML client routes under `/app/*`

Current core API behavior:
- S3-style routes are wired for path-style and optional virtual-host style routing (`S000_DOMAIN`).
- Non-health endpoints use SigV4 verification.
- Personal access token authentication is also available via `Authorization: Bearer <token>` when PAT signing is configured.
- Bucket APIs include create/list/delete (empty bucket), location, versioning, and ListObjectsV2.
- Object APIs include put/get/head/delete, copy, range reads, user metadata headers, and checksum/ETag headers.
- Multipart APIs include create/upload-part/list-parts/complete/abort and list multipart uploads.
- Metadata backend selection is wired (`S000_METADATA_BACKEND`) with compatibility-layer adapters for `sqlite`, `libsql`, `postgresql`, `mariadb`, and `valkey`.
- Embedded web client is available at `/app/*` with a configurable UI theme.
- Website endpoint support is available via `S000_WEBSITE_ENABLED` and `S000_WEBSITE_DOMAIN` with index/error documents, redirect-all, and routing rules.

Selected environment variables:
- `S000_ADDR` (default `:9000`)
- `S000_DOMAIN` (optional virtual-host suffix, example `s000.local`)
- `S000_IMPORT_DIRECTORY` (optional path to import at startup; top-level folders become buckets)
- `S000_MAX_INFLIGHT` (default `128`)
- `S000_SHUTDOWN_TIMEOUT` (default `10s`)
- `S000_AUTH_MAX_SKEW` (default `15m`)
- `S000_ADMIN_ACCESS_KEY` + `S000_ADMIN_SECRET_KEY` (initial admin bootstrap credential)
- `S000_METADATA_BACKEND` (default `sqlite`)
- `S000_METADATA_DSN` (default follows `S000_DATA_DIR` as `file:<data-dir>/s000-metadata.db`; metadata catalog snapshots reload on startup for the selected backend)
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
- `S000_TLS_ENABLED` (default `false`)
- `S000_TLS_CERT_FILE` (required when TLS enabled)
- `S000_TLS_KEY_FILE` (required when TLS enabled)
- `S000_TLS_MIN_VERSION` (default `1.2`, supported: `1.2`, `1.3`)
- `S000_AUTH_FAIL_THRESHOLD` (default `20`)
- `S000_AUTH_FAIL_WINDOW` (default `1m`)
- `S000_AUTH_BLOCK_DURATION` (default `5m`)
- `S000_PAT_SIGNING_KEY` (optional PAT signing key; defaults to `S000_ADMIN_SECRET_KEY` when unset)
- `S000_UI_THEME` (default `sysadmin90`; supported: `sysadmin90`)
- `S000_UI_SSE_DASHBOARD_STATS_INTERVAL` (default `2s`; dashboard API stats refresh cadence)
- `S000_UI_SSE_BUCKETS_INTERVAL` (default `10s`; buckets table refresh cadence)
- `S000_UI_SSE_TOKENS_INTERVAL` (default `10s`; tokens table refresh cadence)
- `S000_UI_SSE_OBJECTS_INTERVAL` (default `10s`; object list refresh cadence)
- `S000_UI_SSE_OBJECT_METADATA_INTERVAL` (default `10s`; object metadata refresh cadence)
- `S000_WEBSITE_ENABLED` (default `false`)
- `S000_WEBSITE_ADDR` (default `:9001`)
- `S000_WEBSITE_DOMAIN` (optional virtual-host suffix for website endpoint)

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
See `docs/deployment-examples.md` for Docker/systemd examples and persistent volume guidance.
See `docs/reliability-spec.md` for shutdown/backup/restore and failure-testing guidance.
See `docs/compatibility-spec.md`, `docs/compatibility-matrix.md`, and `docs/interoperability-bugs.md` for compatibility validation status.
See `docs/security-hardening-spec.md`, `docs/threat-model.md`, and `docs/security-review-checklist.md` for security hardening details.
See `docs/cli-tooling.md` for `s000ctl` commands, UX conventions, and troubleshooting.
See `docs/htmx-client.md` for embedded HTML client route and template scaffolding.
See `docs/website-hosting.md` for S3-style bucket website hosting MVP behavior and local setup.

## Validated Local Bring-Up

The following sequence is currently verified to pass in this repository:

```bash
make lint
go test ./...
make integration

export S000_ADMIN_ACCESS_KEY=admin
export S000_ADMIN_SECRET_KEY=secret
go run ./cmd/s000

# in a second terminal
go run ./cmd/s000ctl health-inspect --endpoint http://127.0.0.1:9000
curl -fsS http://127.0.0.1:9000/healthz
curl -fsS http://127.0.0.1:9000/readyz
```
