# Release 1 TODO (s000)

This checklist tracks work required to ship the first production-ready release.

## 0. Project Foundation
- [x] Define semantic versioning and Release 1 scope freeze criteria.
- [x] Create repository layout (`cmd/`, `internal/`, `pkg/`, `test/`, `docs/`).
- [x] Add Makefile/task runner targets (`build`, `test`, `lint`, `bench`, `integration`).
- [x] Add CI workflow for lint + unit + integration + build matrix.
- [x] Add contributor docs for local dev setup and test execution.
- [x] Add initial TDD baseline tests for config, handlers, and integration health checks.

## 1. Core API Server
- [x] Implement HTTP server bootstrap with config loading and graceful shutdown.
- [x] Add routing for S3-compatible endpoint patterns (path-style and virtual-host style where possible).
- [x] Implement request id generation and context propagation.
- [x] Implement structured error responses compatible with S3 XML error format.
- [x] Add global middleware: logging, panic recovery, auth gate, basic rate limiting.

## 2. Authentication and Credentials
- [x] Implement SigV4 canonical request parsing and signature verification.
- [x] Build credential store (access key id, secret hash, status, created/rotated at).
- [x] Add admin bootstrap flow for initial credential creation.
- [x] Implement pre-signed URL verification for GET and PUT.
- [x] Add key rotation flow with overlap window support.
- [x] Add tests for signature edge cases (header order, query params, clock skew).

## 3. Metadata Layer (Multi-Backend)
- [x] Define backend interface and capability matrix for `sqlite`, `libsql`, `postgresql`, `mariadb`, and `valkey`.
- [x] Define canonical metadata model for buckets, object_versions, multipart_uploads, multipart_parts, credentials.
- [x] Implement backend selection/config (`S000_METADATA_BACKEND`, DSN/settings per backend) with clear startup validation.
- [x] Implement relational schema + migrations for `sqlite`, `libsql`, `postgresql`, and `mariadb` with a shared migration contract.
- [x] Implement backend adapters for `sqlite`, `libsql`, `postgresql`, and `mariadb` covering full metadata CRUD + transactions.
- [x] Implement `valkey` adapter strategy (metadata cache and coordination primitives; define persistence/consistency boundaries explicitly).
- [x] Add indexing/optimization plan per backend for common access patterns (bucket+key, bucket+prefix, upload_id).
- [x] Implement transactional object commit/delete semantics with backend-specific guarantees documented and tested.
- [x] Add cross-backend compatibility tests to ensure identical API-visible behavior across all supported metadata backends.
- [x] Add metadata consistency checks and backend-specific repair/validation commands.
- [x] Wire production networked drivers/connections for `libsql`, `postgresql`, `mariadb`, and `valkey` (current adapters are compatibility-layer implementations).
- [x] Add backend-specific integration tests against live services in CI (containers/services matrix).

## 4. Blob Storage Engine
- [x] Implement object root layout strategy and deterministic path mapping.
- [x] Implement streaming write pipeline to temp file + checksum + atomic rename.
- [x] Implement streaming read pipeline with Range support.
- [x] Implement delete semantics with versioning awareness.
- [x] Enforce fsync policy (configurable strict/safe/fast mode with documented tradeoffs).
- [x] Add startup recovery for orphaned temp files and partial multipart states.

## 5. Bucket API
- [x] CreateBucket.
- [x] ListBuckets.
- [x] DeleteBucket (only when empty).
- [x] GetBucketLocation.
- [x] PutBucketVersioning / GetBucketVersioning.
- [x] ListObjectsV2 with prefix, delimiter, continuation token, max keys.

## 6. Object API
- [x] PutObject (single-part upload).
- [x] GetObject.
- [x] HeadObject.
- [x] DeleteObject (versioned + non-versioned behavior).
- [x] CopyObject with metadata directive handling.
- [x] Put/Get object user metadata headers.
- [x] Return correct ETag/checksum headers and validate `Content-MD5` when provided.

## 7. Multipart Upload API
- [x] CreateMultipartUpload.
- [x] UploadPart.
- [x] ListParts.
- [x] CompleteMultipartUpload with part ordering and checksum validation.
- [x] AbortMultipartUpload.
- [x] ListMultipartUploads.
- [x] Multipart garbage collection job for abandoned uploads.

## 8. Lifecycle Management
- [x] Define lifecycle rule model (prefix + age-based expiration).
- [x] Implement lifecycle evaluator worker.
- [x] Implement safe delete execution with batching/backoff.
- [x] Emit metrics for scanned objects, deletions, failures.
- [x] Add dry-run mode for lifecycle validation.

## 9. Observability
- [x] Structured logs with request id, principal, bucket, key, status, latency, bytes.
- [x] Prometheus metrics endpoint.
- [x] Core metrics: request total, latency histogram, error counts, bytes in/out, worker queue depth.
- [x] Health and readiness endpoints with dependency checks.
- [x] Add optional tracing hooks (OTel) behind feature flag.

## 10. Performance and Resource Controls
- [x] Tune net/http server settings (timeouts, max header size, keepalive behavior).
- [x] Add bounded worker pools and queue limits for heavy operations.
- [x] Implement backpressure responses when server is overloaded.
- [x] Add benchmark suite for common operations (1 KB, 1 MB, 100 MB objects).
- [x] Add memory/CPU profiling harness and baseline reports.

## 11. Portability and Packaging
- [x] Build matrix for Linux/FreeBSD/macOS x amd64/arm64.
- [x] Produce reproducible release artifacts with checksums.
- [x] Build minimal container image and document persistent volume needs.
- [x] Add systemd unit example and Docker Compose example.
- [x] Validate operation on low-resource hardware profile (2 cores / 4 GB RAM).

## 12. Reliability, Backup, and Recovery
- [x] Implement graceful shutdown with in-flight request draining.
- [x] Document and test cold backup and snapshot-consistent backup procedures.
- [x] Create restore validation command.
- [x] Add chaos/failure tests (kill during upload, disk full, fsync errors).
- [x] Define and test upgrade path between pre-1.0 schema versions.

## 13. Compatibility Validation
- [x] Build compatibility matrix covering each Release 1 API.
- [x] Add end-to-end test scripts using `aws-cli` (`ls`, `cp`, `sync`, multipart flows).
- [x] Add SDK smoke tests (Go, Python, JS).
- [x] Verify S3-style error code parity for common failure paths.
- [x] Track and close top interoperability bugs before RC.

## 14. Security Hardening
- [x] Enforce TLS configuration and document secure defaults.
- [x] Add secret redaction in logs and panic output.
- [x] Add brute-force and abusive pattern protections.
- [x] Add audit events for auth failures and destructive operations.
- [x] Complete threat model and security review checklist.

## 15. Implement CLI Tooling
- [x] Define CLI command surface and UX conventions.
- [x] Implement core admin/ops subcommands for backup, restore validation, and health inspection.
- [x] Add shell completion and help text coverage tests.
- [x] Add integration tests for CLI workflows against local server.
- [x] Document CLI usage examples and troubleshooting.

## 16. Documentation and Release Readiness
- [x] Write quickstart for single-node deployment.
- [x] Write operations guide (config, monitoring, backups, upgrades).
- [x] Document tuning guide for consumer hardware.
- [x] Publish known limitations and non-goals for Release 1.
- [x] Create release checklist and rollback procedure.
- [ ] Run release candidate soak test and sign-off.

## 17. Embedded HTML Client (Go + htmx)
- [x] Define frontend scope and UX flows (bucket list, object browse, upload, download, delete, multipart visibility).
- [x] Add server-side HTML rendering layer in Go (`html/template`) and route group for web UI.
- [x] Serve static assets (CSS/JS/icons) from the same binary with cache-safe versioning strategy.
- [x] Add htmx-powered partial endpoints for table/list refresh, object metadata panel, and action results.
- [x] Implement progressive enhancement fallback for core actions when JavaScript/htmx is unavailable.
- [x] Add UI auth strategy for admin/operator use (credential input/session handling) aligned with security hardening.
- [x] Add CSRF protection and safe method constraints for HTML form actions.
- [x] Implement upload/download UX with progress indicators and robust error surfacing.
- [x] Add pagination, prefix filtering, and delimiter navigation parity with ListObjectsV2 behavior.
- [x] Add integration tests for HTML routes and htmx partial responses.
- [x] Add accessibility checks (keyboard navigation, labels, ARIA/live regions for async updates).
- [x] Add responsive layout verification for desktop/mobile breakpoints.
- [x] Document web client configuration, security considerations, and operational limitations.

## 18. Bucket Website Hosting (S3-style MVP)
- [x] Define MVP scope and compatibility goals for website hosting behavior (index/error/redirect baseline).
- [x] Add website endpoint config (`S000_WEBSITE_ENABLED`, `S000_WEBSITE_ADDR`, `S000_WEBSITE_DOMAIN`).
- [x] Add bucket website config model + metadata persistence (`index_document`, `error_document`, optional redirect rules).
- [x] Add API surface for website config management (`PutBucketWebsite`, `GetBucketWebsite`, `DeleteBucketWebsite`).
- [x] Implement dedicated website HTTP handler (separate from SigV4 API auth path).
- [x] Implement host/path bucket resolution for website requests (virtual-host style first, path-style fallback for local dev).
- [x] Implement anonymous `GET`/`HEAD` website reads with bucket-level website enable/public guard.
- [x] Implement object resolution rules: exact key, `/path/ -> /path/index.html`, root `/ -> /index.html`.
- [x] Implement website error behavior: object 404 -> configured `error_document` fallback, else plain 404.
- [x] Set correct response headers for website content (`Content-Type`, `Content-Length`, `ETag`, cache-safe defaults).
- [x] Implement redirect behavior MVP (bucket-level redirect-all-requests-to OR key-level redirect metadata).
- [x] Add integration tests for website routing, index fallback, error document fallback, and redirects.
- [x] Add compatibility tests for common static-site layouts (single-page app fallback optional toggle).
- [x] Add docs for local static-site deployment with example bucket layout and endpoint/domain setup.

## Suggested Milestones
- [x] M1 (Foundation): Sections 0-3 complete.
- [x] M2 (Core S3 CRUD): Sections 4-7 complete with passing integration tests.
- [x] M3 (Production Basics): Sections 8-12 complete.
- [ ] M4 (Release Candidate): Sections 13-18 complete, all exit criteria met.

## 19. Historical Notes

Function/WASM roadmap items were removed from active scope. s000 now targets S3 storage and related operational surfaces only.
