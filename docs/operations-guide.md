# Operations Guide

This guide covers running s000 in production: monitoring, backups, and upgrades.

## Monitoring

### Health and Readiness

```bash
curl -fsS http://127.0.0.1:9000/healthz
curl -fsS http://127.0.0.1:9000/readyz
```

- `healthz` returns `200` when the service can start handling requests.
- `readyz` returns `200` when all dependencies (metadata backend, data directory) are reachable.

### Metrics

Prometheus metrics are exposed at `/metrics` (configurable via `S000_METRICS_PATH`).

Key metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `s000_http_requests_total` | Counter | Total HTTP requests by method, path, status |
| `s000_http_request_duration_seconds` | Histogram | Request latency |
| `s000_http_request_bytes_total` | Counter | Bytes sent/received |
| `s000_lifecycle_runs_total` | Counter | Lifecycle worker runs |
| `s000_lifecycle_objects_scanned_total` | Counter | Objects scanned by lifecycle |
| `s000_lifecycle_objects_deleted_total` | Counter | Objects deleted by lifecycle |

Scrape configuration for Prometheus:

```yaml
scrape_configs:
  - job_name: s000
    static_configs:
      - targets: ['localhost:9000']
```

### Structured Logs

Logs include request ID, principal, bucket, key, method, path, status, duration_ms, bytes_in, bytes_out.

Example log line:

```
request_id=abc123 principal=admin bucket=my-bucket key=hello.txt method=GET path=/my-bucket/hello.txt status=200 duration_ms=12 bytes_in=0 bytes_out=12
```

### Debug Endpoints

When lifecycle rules are configured:

- `GET /debug/lifecycle/config` - lifecycle worker configuration
- `GET /debug/lifecycle/metrics` - cumulative lifecycle run counters

### Tracing

Optional OpenTelemetry tracing can be enabled:

```bash
export S000_TRACING_ENABLED=true
```

## Backups

### Creating Backups

Use the CLI to create a cold backup snapshot:

```bash
go run ./cmd/s000ctl backup-create \
  --data-dir /var/lib/s000/data \
  --metadata-dsn 'file:/var/lib/s000/data/metadata.db' \
  --out /backup/$(date +%Y%m%d)
```

This captures:
- Data root (object blobs, staging area)
- Metadata database (for file-backed backends)

### Backup Schedule Recommendation

- Daily incremental backups of new objects
- Weekly full backups
- Test restore procedure quarterly

### Validating Backups

```bash
go run ./cmd/s000ctl restore-validate --path /backup/20240401
```

The validation checks:
- Backup directory exists
- `data/objects` directory exists
- Metadata backup file exists (for file-backed DSNs)

### Restoring from Backup

1. Stop the service
2. Copy backup contents to data root:
   ```bash
   cp -r /backup/20240401/data/* /var/lib/s000/data/
   ```
3. For metadata backups, copy the database file:
   ```bash
   cp /backup/20240401/metadata/s000-metadata.db /var/lib/s000/data/metadata.db
   ```
4. Start the service

### Backup for Networked Backends

For PostgreSQL, MariaDB, or Valkey backends:
- Backup the database using backend-native tools (`pg_dump`, `mysqldump`, `redis-cli BGSAVE`)
- The `backup-create` command captures only the blob data directory

## Upgrades

### Upgrade Path

s000 uses semantic versioning. Upgrades between minor versions within Release 1 are non-disruptive.

### Pre-Upgrade Checklist

1. Review release notes for breaking changes
2. Create a backup
3. Test the upgrade in a staging environment
4. Schedule a maintenance window (brief downtime expected)

### Upgrade Procedure

1. Stop the current instance:
   ```bash
   systemctl stop s000
   ```

2. Backup data (if not done recently):
   ```bash
   go run ./cmd/s000ctl backup-create \
     --data-dir /var/lib/s000/data \
     --metadata-dsn 'file:/var/lib/s000/data/metadata.db' \
     --out /backup/pre-upgrade-$(date +%Y%m%d)
   ```

3. Replace the binary:
   ```bash
   cp s000-new /usr/local/bin/s000
   ```

4. Start the instance:
   ```bash
   systemctl start s000
   ```

5. Verify readiness:
   ```bash
   curl -fsS http://127.0.0.1:9000/readyz
   ```

6. Run health inspection:
   ```bash
   go run ./cmd/s000ctl health-inspect --endpoint http://127.0.0.1:9000
   ```

### Rollback Procedure

If issues occur after upgrade:

1. Stop the instance
2. Restore from backup:
   ```bash
   cp -r /backup/pre-upgrade-YYYYMMDD/data/* /var/lib/s000/data/
   cp /backup/pre-upgrade-YYYYMMDD/metadata/s000-metadata.db /var/lib/s000/data/metadata.db
   ```
3. Revert the binary:
   ```bash
   cp s000-old /usr/local/bin/s000
   ```
4. Start the instance

## Data Directory Maintenance

### Disk Usage Monitoring

Monitor disk space for the data root:

```bash
du -sh /var/lib/s000/data
```

### Orphaned Temp Files

On startup, s000 automatically cleans up:
- Partial multipart uploads in staging directory
- Temporary files from interrupted writes

This recovery is automatic and logged at startup.

### Lifecycle Policies

Configure automatic expiration of old objects:

```bash
export S000_LIFECYCLE_RULES='prefix=logs/,age=30d;prefix=tmp/,age=7d'
```

This deletes objects older than 30 days under `logs/` and 7 days under `tmp/`.

Dry-run mode for testing:

```bash
export S000_LIFECYCLE_DRY_RUN=true
```

## Performance Tuning

For recommendations on tuning for consumer hardware, see `docs/tuning-guide.md`.

## Security

### TLS Configuration

Enable TLS in production:

```bash
export S000_TLS_ENABLED=true
export S000_TLS_CERT_FILE=/etc/s000/tls.crt
export S000_TLS_KEY_FILE=/etc/s000/tls.key
export S000_TLS_MIN_VERSION=1.2
```

### Rate Limiting

Brute-force protection is built-in:
- `S000_AUTH_FAIL_THRESHOLD`: max failed attempts before blocking (default 20)
- `S000_AUTH_FAIL_WINDOW`: time window for counting failures (default 1m)
- `S000_AUTH_BLOCK_DURATION`: how long to block after threshold (default 5m)

### Audit Logs

Auth failures and destructive operations are logged. Search for:
- `auth failure` in logs
- `delete` operations on buckets/objects