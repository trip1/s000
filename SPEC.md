# s000 Specification

## 1) Product Summary
s000 is a self-hosted, S3-compatible object storage service designed to run efficiently on consumer hardware while remaining production-ready for small and medium deployments.

Primary outcomes:
- S3-compatible APIs for common client tooling (`aws-cli`, SDKs, backup tools).
- Predictable performance on commodity systems (single node first, scale-out later).
- Operational simplicity: one binary, low memory footprint, safe defaults.

## 2) Goals and Non-Goals

### Goals
- Implement the major S3 object and bucket operations required by common workloads.
- Provide strong data integrity (checksums, atomic writes, crash-safe metadata updates).
- Support multiple metadata backends: `sqlite`, `libsql`, `postgresql`, `mariadb`, and `valkey` (with explicit consistency boundaries).
- Run on Linux, macOS, and Windows; support `amd64` and `arm64`.
- Be deployable as a single binary and as a container image.
- Offer production basics from day one: auth, TLS, metrics, structured logs, backups.

### Non-Goals (Release 1)
- Full IAM policy parity with AWS IAM.
- Multi-region replication.
- Glacier-class archival tiers.
- Distributed erasure-coded cluster mode.

## 3) Release 1 Scope (Major S3 Features)

### Bucket APIs
- Create/List/Delete bucket.
- Bucket location and basic configuration APIs.
- Bucket versioning: enabled/suspended.

### Object APIs
- PUT/GET/HEAD/DELETE object.
- ListObjectsV2.
- CopyObject.
- Range reads.
- ETag and checksum headers.
- User metadata (`x-amz-meta-*`).
- Virtual folder semantics via key prefixes (`/`) with `ListObjectsV2` support for `prefix`, `delimiter`, `CommonPrefixes`, and `KeyCount`.

### Multipart Upload
- CreateMultipartUpload.
- UploadPart.
- CompleteMultipartUpload.
- AbortMultipartUpload.
- ListParts/ListMultipartUploads.

### Access and Security
- Access key + secret key authentication (SigV4-compatible request signing).
- TLS support.
- Pre-signed URLs for GET/PUT.

### Lifecycle and Data Management
- Basic lifecycle expiration by age/prefix.
- Optional object lock deferred to post-Release 1.

### Compatibility Target
- Prioritize API behavior required by:
  - `aws s3 cp/sync/ls`
  - Common SDK CRUD usage (Go, Python, JavaScript)
  - Backup/sync tools using S3-compatible endpoints

## 4) Architecture

### High-Level Components
- API Layer: HTTP server exposing S3-compatible endpoints.
- Auth Layer: SigV4 validation and credential management.
- Metadata Layer: bucket/object metadata, multipart state, version pointers.
- Data Layer: local filesystem object blobs with content checks and atomic writes.
- Background Workers: lifecycle cleanup, multipart GC, compaction/maintenance tasks.

### Storage Model (Single Node, Release 1)
- Blob storage on local filesystem under a configurable root.
- Metadata backend is configurable:
  - Relational primary backends: `sqlite`, `libsql`, `postgresql`, `mariadb`.
  - `valkey` support is for metadata cache/coordination primitives unless explicitly configured for additional roles.
- Default backend is `sqlite` for low operational overhead.
- Object keys mapped to immutable blob files; metadata references current/versioned blobs.
- Temporary uploads and multipart parts isolated in a staging directory.

### Metadata Backend Contract
- Canonical metadata behavior must be backend-agnostic for API-visible semantics.
- Relational backends must provide transactional guarantees for object commit/delete operations.
- Backend capability differences must be documented in a compatibility matrix.
- `valkey` usage must declare persistence and consistency tradeoffs before production enablement.

### Key Data Entities
- Account/Credential: access key id, secret hash, status, optional scope.
- Bucket: name, creation time, versioning state, config.
- ObjectVersion: bucket, key, version id, size, checksums, ETag, metadata, storage path, timestamps, delete marker flag.
- MultipartUpload: upload id, bucket/key, initiated time, parts map.

## 5) Data Integrity and Consistency
- All writes are checksum-verified before commit.
- Atomic metadata transactions for object create/overwrite/delete.
- Crash-safe startup recovery for unfinished multipart/staged uploads.
- Read-after-write consistency on a single node.

## 6) Performance Requirements

### Target Hardware Profiles
- Minimum: 2 cores, 4 GB RAM, SATA SSD.
- Recommended: 4+ cores, 8+ GB RAM, NVMe SSD.

### Release 1 Performance Targets
- Sustained sequential PUT/GET throughput should be disk-bound on SSD.
- P95 latency targets (recommended hardware):
  - HEAD/metadata operations: < 20 ms
  - Small object GET/PUT (< 1 MB): < 50 ms
- Memory budget: default runtime steady-state <= 512 MB under moderate load.

### Performance Techniques
- Streaming reads/writes (avoid full-object buffering).
- Bounded worker pools and backpressure for multipart assembly.
- Connection reuse and tuned HTTP server limits.
- Efficient key/index lookups with backend-specific indexing/query plans.

## 7) Security Requirements
- SigV4 request validation for authenticated API calls.
- Secrets never stored in plaintext (store derived hashes where possible).
- TLS for in-transit encryption; support custom certs.
- Optional at-rest encryption (SSE-S3 style) with a local master key in Release 1.1.
- Audit log events for auth failures, key creation/rotation, and delete operations.

## 8) Observability and Operations
- Structured logs with request ids and bucket/key context.
- Prometheus metrics for requests, latency, bytes, errors, and background jobs.
- Health/readiness endpoints.
- Graceful shutdown with in-flight request draining.
- Backup/restore guidance:
  - Quiesced or snapshot-consistent backup of metadata DB + object root.

## 9) Portability and Packaging
- Build outputs:
  - Static binaries per OS/arch where possible.
  - OCI container image.
- Runtime config via file + environment variables.
- Default mode has no external DB dependency (`sqlite`); external metadata backends are optional.

## 10) API Compatibility Strategy
- Implement strict behavior for common paths first.
- Return S3-compatible error codes/messages for client interoperability.
- Maintain an API compatibility matrix and integration tests against `aws-cli` scenarios.

### Folder Compatibility Contract
- Folders are virtual, derived from object key prefixes; no first-class folder metadata entity.
- Folder marker objects (keys ending with `/`) are supported as normal objects.
- `ListObjectsV2` with `delimiter=/` must return direct child objects in `Contents` and direct child prefixes in `CommonPrefixes`.
- `KeyCount` must reflect returned `Contents + CommonPrefixes` entries for each page.
- Continuation tokens are opaque and tied to listing parameters (`prefix`, `delimiter`).

## 11) Testing and Quality Gates
- Unit tests for signing, metadata transactions, lifecycle logic.
- Integration tests for bucket/object/multipart flows.
- Cross-backend conformance tests to verify identical API-visible behavior for all supported metadata backends.
- Fault-injection tests for crash recovery and partial write scenarios.
- Performance regression benchmarks in CI for key code paths.

Release 1 exit criteria:
- All critical API integration tests pass.
- No known data loss/corruption bugs.
- P95 latency and throughput targets met on recommended hardware.
- Upgrade and backup/restore procedures documented and tested.

## 12) Risks and Mitigations
- API edge-case drift from S3: mitigate with compatibility test corpus.
- Consumer hardware variability: mitigate with conservative defaults and tuning docs.
- Backend divergence under load/failure: mitigate with shared metadata contract tests and capability-gated features.
- `valkey` persistence/consistency mismatch for authoritative metadata: mitigate by restricting role to cache/coordination unless durability guarantees are explicitly met.

## 13) Future Milestones (Post-Release 1)
- Replication (async mirror to secondary target).
- Multi-node mode and placement strategies.
- Expanded policy system and tenancy controls.
- Advanced lifecycle transitions and object lock.
