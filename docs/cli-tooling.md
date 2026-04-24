# CLI Tooling (Section 15)

`s000ctl` is the operational CLI for local admin workflows.

## Command Surface

- `backup-create` - create a cold backup snapshot.
- `restore-validate` - validate backup restore layout.
- `health-inspect` - probe `/healthz` and `/readyz`.
- `token-create` - mint personal access token for bearer auth.
- `put-object` - upload one object with bearer token auth.
- `presign-url` - generate a SigV4 presigned object URL.
- `completion` - print shell completion snippet.
- `help` - show command usage.

## UX Conventions

- Exit code `0`: success.
- Exit code `1`: command executed but operation failed.
- Exit code `2`: usage/argument error.
- Successful commands print concise status lines to stdout.
- Failures print a short actionable error to stderr.

## Usage Examples

```bash
# Create backup
s000ctl backup-create \
  --data-dir ./data \
  --metadata-dsn file:./data/s000-metadata.db \
  --out ./backup

# Validate backup
s000ctl restore-validate --path ./backup

# Check service health
s000ctl health-inspect --endpoint http://127.0.0.1:9000

# Create token (defaults to S000_PAT_SIGNING_KEY if set)
TOKEN=$(s000ctl token-create --subject ci --ttl 24h)

# Upload object with token
s000ctl put-object --endpoint http://127.0.0.1:9000 --bucket my-bucket --key notes.txt --file ./notes.txt --token "$TOKEN"

# Generate presigned object URL
s000ctl presign-url --endpoint http://127.0.0.1:9000 --bucket my-bucket --key notes.txt --method GET --expires 15m

# Generate shell completion snippet
s000ctl completion --shell bash
```

## Troubleshooting

- `backup-create failed: data dir is required`
  - Ensure `--data-dir` points to your runtime data directory.
- `restore validation failed: backup missing data/objects`
  - Backup is incomplete; re-run `backup-create` and verify destination permissions.
- `healthz failed` or `readyz failed`
  - Check endpoint URL/port, service startup status, and TLS/non-TLS scheme.
- `token-create failed: pat signing key is required`
  - Set `S000_PAT_SIGNING_KEY` (or pass `--signing-key`) and retry.
- `put-object failed: bucket, key, file, and token are required`
  - Provide all four flags; `--token` can be replaced by `S000_ACCESS_TOKEN` env var.
- `presign-url failed: bucket, key, access-key, and secret-key are required`
  - Provide object location and signing credentials, or set `S000_ADMIN_ACCESS_KEY` and `S000_ADMIN_SECRET_KEY`.
- `completion failed: unsupported shell`
  - Use one of: `bash`, `zsh`, `fish`, `powershell`.
