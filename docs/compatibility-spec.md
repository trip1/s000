# Compatibility Validation Spec (Section 13)

This document defines Release 1 compatibility validation scope.

## Goals

- Verify each Release 1 API surface expected by common S3 clients.
- Provide executable compatibility flows for `aws-cli` and major SDKs.
- Add repeatable flows for homelab clients: `rclone`, `restic`, MinIO `mc`, and `s3cmd`.
- Preserve S3-style error code parity for common failure paths.
- Track and close top interoperability issues before RC.

## Required Validation Artifacts

- Compatibility matrix: `docs/compatibility-matrix.md`
- `aws-cli` E2E script: `scripts/awscli-e2e.sh`
- SDK smoke scripts:
  - Go: `scripts/sdk-smoke-go.sh`
  - Python: `scripts/sdk-smoke-python.sh`
  - JavaScript: `scripts/sdk-smoke-js.sh`
- Interoperability tracker: `docs/interoperability-bugs.md`
- Future real-client scripts:
  - `scripts/rclone-e2e.sh`
  - `scripts/restic-e2e.sh`
  - `scripts/mc-e2e.sh`
  - `scripts/s3cmd-e2e.sh`

## Core Flows

### aws-cli

Must validate at least:

- `aws s3 ls`
- `aws s3 cp` upload/download
- `aws s3 sync` upload/download
- multipart upload via `s3api` commands

### SDK Smoke Coverage

Each SDK smoke flow must validate:

- create bucket
- put object
- get object
- delete object

### Homelab Client Coverage

Real-client validation should prioritize common workflows over exhaustive AWS feature parity:

- `rclone`: configure S3 remote, `copy`, `sync`, `check`, delete, and large-file multipart transfer.
- `restic`: `init`, `backup`, `snapshots`, `restore`, and `check` against an S3 repository.
- MinIO `mc`: `alias set`, `mb`, `cp`, `mirror`, `stat`, `ls`, and `rm`.
- `s3cmd`: configure endpoint, `mb`, `put`, `get`, `sync`, `ls`, and `del`.

### Advanced API Regression Coverage

Unit/integration tests should continue covering these S3 subresources even before every client exercises them:

- bucket versioning and `ListObjectVersions`
- multipart checksums
- CORS
- lifecycle
- object tagging
- SSE-S3
- object lock retention/legal hold
- notifications and replication best-effort delivery

## Error Code Parity

Common failures must emit expected S3-style XML error codes:

- `NoSuchBucket`
- `NoSuchKey`
- `BucketNotEmpty`
- `InvalidPart`
- `InvalidPartOrder`
- `SlowDown`
