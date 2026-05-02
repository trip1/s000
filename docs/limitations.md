# Known Limitations and Non-Goals

This document tracks practical gaps for running `s000` as a self-hosted, S3-compatible object store. The current goal is homelab and small single-node deployments: common client compatibility, predictable operations, and simple recovery are prioritized over full AWS feature parity.

## Implemented S3 Surface

The following S3-compatible features are implemented and covered by unit/integration-style tests unless noted otherwise:

- Core bucket/object APIs: create/list/delete bucket, bucket location, `ListObjectsV2`, put/get/head/delete object, copy object, range reads, user metadata, and multi-object delete.
- Multipart upload: create/upload/list/complete/abort, list multipart uploads, 10,000 part limit, 5 MiB non-final-part enforcement, multipart ETag semantics, and multipart checksums.
- Versioning: enable/suspend, list object versions, delete markers, specific `versionId` delete, pagination markers, and null-version overwrite/delete behavior.
- Request compatibility: SigV4 auth, common S3 XML errors, conditional object/copy requests, checksum validation/headers, and path-style plus optional virtual-host routing.
- Bucket subresources: CORS, bucket policy persistence, public access block, website configuration, lifecycle configuration, notification configuration, replication configuration, ACL compatibility, and object tagging.
- Data management: lifecycle XML expiration rules, SSE-S3 with a local master key, object lock retention/legal hold, webhook notifications, and simple async HTTP replication.
- Website hosting: optional separate website listener with index/error documents, redirect-all, and routing rules.

## Deliberate Non-Goals

These are intentionally out of scope for the current single-node/homelab target:

- Full AWS IAM, STS, roles, and complete bucket-policy evaluation.
- Distributed clustering, distributed erasure coding, automatic failover, or multi-node quorum behavior.
- Glacier/deep-archive storage classes and lifecycle transitions to cold storage.
- SSE-KMS or external key-manager integration.
- S3 Select, Object Lambda, Inventory, Batch Operations, Access Points, and Object Ownership controls.
- AWS-compatible notification destinations beyond simple HTTP(S) webhook-style delivery.

## Known Limitations

### Access Control

- Bucket policies are persisted and returned, but full IAM-style policy enforcement is not implemented.
- ACL APIs are compatibility-oriented: `GET ?acl` returns owner full-control XML and `PUT ?acl` accepts common canned ACLs as no-ops.
- Public access block configuration is stored, but full AWS public-access policy semantics should not be assumed.
- Personal access tokens are available for the built-in UI/API workflows, but they are not an AWS IAM replacement.

### Encryption and Object Lock

- SSE-S3 requires `S000_SSE_MASTER_KEY`, a base64-encoded 32-byte local master key.
- There is no key rotation workflow yet for already-encrypted objects.
- SSE-KMS, SSE-C, and external KMS integrations are not supported.
- Object lock retention/legal hold is enforced by `s000`, but bucket-level AWS object-lock enablement and governance bypass semantics are intentionally minimal.

### Replication and Notifications

- Notifications are asynchronous best-effort webhook deliveries. Delivery state is not durably queued.
- Replication is asynchronous best-effort HTTP `PUT` to a configured S3-compatible endpoint. There is no durable retry spool, replication status field, or delete replication yet.
- Failed notification/replication deliveries are logged/observable through process logs rather than a built-in dead-letter queue.

### Lifecycle

- Lifecycle execution focuses on expiration-style rules that are useful for homelabs.
- Storage class transitions, noncurrent-version transition classes, and advanced AWS lifecycle actions are not implemented.
- Multipart cleanup exists as local staging GC, but full lifecycle XML parity for abandoned multipart upload rules is not complete.

### S3 API Compatibility

- Virtual-host style routing requires `S000_DOMAIN`; path-style works by default.
- Pre-signed URL support is focused on GET and PUT with SigV4 expiration limits.
- `CopyObject` supports common condition headers, but not every AWS copy option or metadata directive nuance should be assumed.
- Real-client validation is incomplete for `rclone`, `restic`, MinIO `mc`, and `s3cmd`.

### Metadata Backends

- SQLite, libSQL, PostgreSQL, and MariaDB use native row-level metadata stores.
- Valkey is capability-gated for cache/coordination use and is not an authoritative metadata backend.
- SQLite can still hit database-level locking under high write concurrency; PostgreSQL/MariaDB are better choices for heavier concurrent use.
- There is no automatic failover for networked metadata backends.

### Operations

- Single-node only: durability depends on the local filesystem, metadata DB durability, and your backup/snapshot process.
- Upgrades may require brief downtime.
- SQLite may require periodic manual vacuum/maintenance.
- Metrics are exposed but not retained; use Prometheus or another external collector.
- Backups are full/snapshot-oriented. Point-in-time recovery and incremental backup are not built in.

## Compatibility Notes

### Tested Clients

- aws-cli compatibility flow is tested.
- Go SDK smoke flow is tested.
- Python boto3 smoke flow is tested.
- JavaScript AWS SDK smoke flow is tested.

### Untested or Not Yet Automated

- `rclone`
- `restic`
- MinIO `mc`
- `s3cmd`
- GUI tools such as Cyberduck or S3 Browser

## Practical Homelab Guidance

- Use SQLite for simple single-user/home deployments; use PostgreSQL or MariaDB if you expect heavier concurrent writes.
- Put the blob directory and metadata DB on reliable storage with snapshots.
- Use TLS when exposing beyond localhost.
- Set `S000_SSE_MASTER_KEY` before accepting SSE-S3 writes, and back that key up separately.
- Treat replication and notifications as convenience features until durable retry state is added.

## Reporting Issues

If a common S3 client fails against `s000`, file the exact command, client version, request path if available, and the S3 XML error returned. Compatibility issues with common homelab tools are prioritized over obscure AWS-only features.
