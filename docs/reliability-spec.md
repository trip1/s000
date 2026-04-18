# Reliability, Backup, and Recovery Spec (Section 12)

This document defines the Release 1 reliability baseline.

## 1) Graceful Shutdown and In-Flight Draining

- Server shutdown must stop accepting new connections.
- In-flight requests must be allowed to complete within configured shutdown timeout.
- If timeout is exceeded, shutdown exits with error.

## 2) Backup Procedures

Backup command/procedure must capture:

- Data root (`S000_DATA_DIR`) including object blobs and staging metadata.
- Metadata database file for file-backed DSNs (for example `file:/.../s000-metadata.db`).

Backup output layout:

- `<backup>/data/...`
- `<backup>/metadata/s000-metadata.db` (when applicable)

CLI support:

- `s000ctl backup-create --data-dir <dir> --metadata-dsn <dsn> --out <backup-dir>`

## 3) Restore Validation Command

A restore validation check must fail fast when backup structure is incomplete.

Required checks:

- backup path exists
- `data/objects` exists
- metadata backup file exists for file-backed metadata snapshots

CLI support:

- `s000ctl restore-validate --path <backup-dir>`

## 4) Chaos/Failure Testing Baseline

Release 1 failure tests should cover:

- interrupted upload path (context cancellation or read error)
- partial write cleanup guarantees
- startup recovery expectations for temp/multipart remnants

## 5) Pre-1.0 Upgrade Path

- Migration upgrades must be monotonic and versioned.
- Upgrade planning must support upgrading from any prior pre-1.0 schema version to latest.
- Upgrade path query should reject unsupported future versions.
