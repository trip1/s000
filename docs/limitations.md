# Known Limitations and Non-Goals (Release 1)

This document outlines what s000 Release 1 does not include and known limitations.

## Non-Goals (Out of Scope)

The following features are explicitly out of scope for Release 1:

### IAM and Access Control
- **Full IAM policy parity**: No IAM policies, roles, or resource-based policies.
- **Bucket policies**: Not supported.
- **ACLs**: Basic ACL support not implemented.

### Scaling and Distribution
- **Multi-node clustering**: Single-node only in Release 1.
- **Distributed erasure coding**: Not supported.
- **Multi-region replication**: Out of scope.

### Storage Tiers
- **Glacier-class archival**: No cold storage tier.
- **Object lock/WORM**: Deferred to post-Release 1.
- **SSE-KMS**: Server-side encryption with external key management not supported.

### Advanced Features
- **Versioning beyond basic enable/suspend**: No MFA delete, lifecycle on versions.
- **Cross-origin (CORS) configuration**: Not implemented.
- **Tagging on buckets/objects**: Not implemented.
- **Batch operations**: No批量删除或批量还原。
- **Select/S3 Select**: Not supported.

## Known Limitations

### S3 API Compatibility
- **Virtual-host style**: Requires `S000_DOMAIN` to be set; limited to single bucket per domain.
- **Path-style only by default**: Virtual-host routing is optional.
- **Pre-signed URLs**: Only GET and PUT methods supported; expires limited to max 7 days.
- **CopyObject**: Does not support `x-amz-copy-source-if-*` conditional headers.

### Performance
- **No parallel uploads**: Multipart must upload parts sequentially (though parts can be uploaded concurrently by client).
- **Limited caching**: No object caching layer; all requests hit storage.
- **No CDN integration**: Static website hosting serves directly from storage.

### Metadata Backends
- **Valkey limitations**: Valkey adapter is for metadata caching/coordination only, not authoritative metadata storage.
- **Transaction limitations**: SQLite has database-level locking; high concurrency may cause `database is locked` errors.
- **No automatic failover**: If using networked backends (PostgreSQL, MariaDB), there's no automatic failover.

### Operations
- **No rolling upgrades**: Upgrades require brief downtime.
- **No online compaction**: SQLite database may grow over time; periodic manual vacuum recommended.
- **Limited metrics retention**: Metrics are exposed but not stored; external Prometheus required for long-term retention.

### Security
- **No data-at-rest encryption**: SSE-S3 style encryption deferred.
- **No mutual TLS (mTLS)**: Client certificate authentication not supported.
- **No audit log export**: Logs to stdout only; external log aggregation required.

### Backup/Restore
- **No incremental backup**: Full snapshots only.
- **No point-in-time recovery**: Restore to any arbitrary point not supported.
- **No replication**: Backup to remote storage must be done externally.

## Compatibility Notes

### Tested Clients
- aws-cli (tested)
- Go SDK (smoke tested)
- Python boto3 (smoke tested)
- JavaScript AWS SDK (smoke tested)

### Untested/Unverified
- S3 Browser and other GUI tools
- rclone
- Restic, Borg backup (may work but unverified)
- Cyberduck

## Future Considerations (Post-Release 1)

Based on the roadmap, these features are under consideration for future releases:

1. **Multi-node clustering** with placement strategies
2. **Async replication** to secondary targets
3. **Object lock/WORM** for compliance
4. **SSE-S3 with local master key**
5. **Expanded policy system and tenancy controls**
6. **Advanced lifecycle transitions** (transition to cold storage)

## Workarounds

For features not available in Release 1:

| Missing Feature | Workaround |
|-----------------|-------------|
| IAM policies | Use access key/secret key with limited scope (application-level) |
| Replication | External sync tool (rclone, aws-cli sync) to second s000 instance |
| Encryption | Layer encryption at application level; filesystem-level LUKS |
| Multi-node | Run multiple s000 instances on different ports; load balance at application level |
| Object lock | Application-level lease/mutex; external enforcement |

## Reporting Issues

If you encounter behavior that seems like a bug (not listed as a limitation), please open an issue. Compatibility issues with standard S3 tools that block common workflows are prioritized for fixes.