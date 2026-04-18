# Release Checklist and Rollback Procedure

This document provides the checklist for releasing s000 and the procedure to roll back if issues arise.

## Release Checklist

### Pre-Release Validation

- [ ] All unit tests pass: `make test`
- [ ] All integration tests pass: `make integration`
- [ ] Lint checks pass: `make lint`
- [ ] Benchmarks run successfully: `make bench`
- [ ] Cross-build succeeds for all platforms: `make cross-build`
- [ ] Release artifacts generated: `make release-artifacts`
- [ ] Low-resource validation passes: `make validate-low-resource`

### Compatibility Validation

- [ ] aws-cli compatibility: `make compatibility-awscli`
- [ ] Go SDK smoke: `make compatibility-sdk-go`
- [ ] Python SDK smoke: `make compatibility-sdk-python`
- [ ] JavaScript SDK smoke: `make compatibility-sdk-js`

### Documentation Review

- [ ] README.md updated with latest features and environment variables
- [ ] Quickstart guide reviewed: `docs/quickstart.md`
- [ ] Operations guide complete: `docs/operations-guide.md`
- [ ] Tuning guide complete: `docs/tuning-guide.md`
- [ ] Limitations documented: `docs/limitations.md`
- [ ] SPEC.md matches implementation

### Security Review

- [ ] Security hardening spec reviewed: `docs/security-hardening-spec.md`
- [ ] Threat model reviewed: `docs/threat-model.md`
- [ ] Security checklist completed: `docs/security-review-checklist.md`
- [ ] TLS configuration tested in production mode

### Deployment Verification

- [ ] systemd unit tested: `deploy/systemd/s000.service`
- [ ] Docker Compose tested: `deploy/docker-compose.yml`
- [ ] Container image builds successfully
- [ ] Health and readiness endpoints verified on clean startup
- [ ] Backup/restore validated: `make backup` + `make restore-validate`

### Operational Readiness

- [ ] Metrics endpoint accessible at `/metrics`
- [ ] Lifecycle rules tested (if configured)
- [ ] Debug endpoints functional (when lifecycle enabled)
- [ ] Log output reviewed for proper formatting
- [ ] Admin credentials rotated from defaults

### Performance Baseline

- [ ] Benchmark results recorded
- [ ] Performance meets targets:
  - HEAD/metadata: < 20ms P95
  - Small object GET/PUT (<1MB): < 50ms P95
- [ ] Memory usage under 512MB steady-state

### Sign-Off

- [ ] QA team sign-off
- [ ] Security review sign-off
- [ ] Ops team sign-off
- [ ] Release notes drafted

## Rollback Procedure

### Trigger Conditions

Initiate rollback if any of the following occur:
- Service fails to start after upgrade
- Critical functionality broken (cannot create/list buckets)
- Data corruption detected
- Latency degraded >3x baseline
- Memory leak causing OOM

### Rollback Steps

#### 1. Stop the Service

```bash
# For systemd
sudo systemctl stop s000

# For Docker
docker stop s000
```

#### 2. Identify the Previous Version

```bash
# Check which version was previously running
# - Check backup timestamps
# - Check deployment records
# - Check binary version if logged
```

#### 3. Restore from Backup

```bash
# List available backups
ls -la /backup/

# Choose the most recent backup before the upgrade
BACKUP=/backup/YYYYMMDD

# Restore data directory
cp -r $BACKUP/data/* /var/lib/s000/data/

# Restore metadata (for file-backed backends)
cp $BACKUP/metadata/s000-metadata.db /var/lib/s000/data/metadata.db

# Verify restored files
ls -la /var/lib/s000/data/
```

#### 4. Revert the Binary

```bash
# If you have the previous binary
cp s000-old /usr/local/bin/s000

# Or rebuild from previous version
git checkout <previous-tag>
make build
cp s000 /usr/local/bin/s000
```

#### 5. Start the Service

```bash
# For systemd
sudo systemctl start s000

# For Docker
docker run -d --name s000 ...
```

#### 6. Verify Service Health

```bash
# Check health endpoint
curl -fsS http://127.0.0.1:9000/healthz

# Check readiness endpoint
curl -fsS http://127.0.0.1:9000/readyz

# Run health inspection
go run ./cmd/s000ctl health-inspect --endpoint http://127.0.0.1:9000
```

#### 7. Validate Functionality

```bash
# Test bucket operations
aws --endpoint-url http://127.0.0.1:9000 s3 ls

# Test object operations
echo "test" | aws --endpoint-url http://127.0.0.1:9000 s3 cp - s3://test-bucket/test.txt
aws --endpoint-url http://127.0.0.1:9000 s3 cp s3://test-bucket/test.txt -

# Verify metrics
curl -s http://127.0.0.1:9000/metrics | head
```

#### 8. Monitor for Issues

- Watch logs for 30 minutes: `journalctl -u s000 -f`
- Monitor metrics for errors: `curl -s http://127.0.0.1:9000/metrics | grep error`
- Check memory usage: `ps -p $(pgrep s000) -o rss`

### Emergency Contacts

- Dev team: [insert contact]
- On-call: [insert contact]
- Incident channel: [insert Slack/Teams channel]

### Post-Rollback Actions

1. Document the failure:
   - What triggered the rollback
   - Symptoms observed
   - Timeline of events

2. Analyze root cause:
   - Review logs
   - Check if bug is reproducible
   - Identify fix needed

3. Plan next steps:
   - Fix the issue
   - Test in staging
   - Re-schedule upgrade when resolved

## Version Recovery Matrix

| Scenario | Recovery Method |
|----------|-----------------|
| Binary fails to start | Restore previous binary, restore data from backup |
| Binary starts but API broken | Restore previous binary, restore metadata backup |
| Data corruption | Restore from backup, investigate cause |
| Performance regression | Adjust config, or revert to previous binary |

## Preventive Measures

- Always backup before upgrade
- Test in staging first
- Have rollback plan documented
- Keep previous binary available
- Monitor closely after upgrade