# Quickstart: Single-Node Deployment

This guide walks through deploying s000 on a single node for development or small-scale production use.

## Prerequisites

- Go 1.25+ (for building from source)
- Or pre-built binaries from the releases page
- ~1 GB disk space for binaries and initial data
- (Optional) SQLite, PostgreSQL, MariaDB, or Valkey for external metadata backend

## Quick Start

### 1. Build the Server

```bash
make build
```

Or use a pre-built binary from releases. For local iteration, `go run ./cmd/s000` is also fine.

### 2. Set Bootstrap Credentials

s000 requires initial admin credentials to boot. Set these environment variables:

```bash
export S000_ADMIN_ACCESS_KEY=admin
export S000_ADMIN_SECRET_KEY=change-me-in-production
export S000_PAT_SIGNING_KEY=change-me-in-production
```

These credentials are used for:
- SigV4 API authentication
- HTML client login (`/app/login`)
- CLI admin operations

### 3. Start the Server

```bash
go run ./cmd/s000
```

Optional: import an existing directory tree at startup (top-level folders become buckets, nested paths become object keys):

```bash
go run ./cmd/s000 --import-directory ./seed-data
```

You can also configure the same behavior with `S000_IMPORT_DIRECTORY=./seed-data`.

The server binds to `:9000` by default. Verify it's running:

```bash
curl -fsS http://127.0.0.1:9000/healthz
curl -fsS http://127.0.0.1:9000/readyz
```

### 4. Use the Service

#### API Access

```bash
# Configure aws-cli
export AWS_ACCESS_KEY_ID=admin
export AWS_SECRET_ACCESS_KEY=change-me-in-production
export AWS_DEFAULT_REGION=us-east-1

aws --endpoint-url http://127.0.0.1:9000 s3 ls

# Create a bucket
aws --endpoint-url http://127.0.0.1:9000 s3 mb s3://my-bucket

# Upload an object
echo "hello world" | aws --endpoint-url http://127.0.0.1:9000 s3 cp - s3://my-bucket/hello.txt

# Download an object
aws --endpoint-url http://127.0.0.1:9000 s3 cp s3://my-bucket/hello.txt -

# Head object metadata
aws --endpoint-url http://127.0.0.1:9000 s3api head-object --bucket my-bucket --key hello.txt

# List objects (ListObjectsV2)
aws --endpoint-url http://127.0.0.1:9000 s3api list-objects-v2 --bucket my-bucket

# Copy object
aws --endpoint-url http://127.0.0.1:9000 s3 cp s3://my-bucket/hello.txt s3://my-bucket/hello-copy.txt

# Create personal access token for bearer auth
TOKEN=$(go run ./cmd/s000ctl token-create --subject local-cli --ttl 24h)

# Upload with s000ctl + bearer token
go run ./cmd/s000ctl put-object \
  --endpoint http://127.0.0.1:9000 \
  --bucket my-bucket \
  --key cli-upload.txt \
  --file ./README.md \
  --token "$TOKEN"
```

### 5. Web UI

Open http://127.0.0.1:9000/app/login in a browser. Log in with the admin credentials configured in step 2.

### 6. CLI Tooling

```bash
# Inspect health
go run ./cmd/s000ctl health-inspect --endpoint http://127.0.0.1:9000

# Create a backup
go run ./cmd/s000ctl backup-create --data-dir ./data --metadata-dsn 'file:./data/s000-metadata.db' --out ./backup
```

## Production Configuration

### Minimal Production Flags

```bash
export S000_ADMIN_ACCESS_KEY=admin
export S000_ADMIN_SECRET_KEY=<secure-random-secret>
export S000_DATA_DIR=/var/lib/s000/data
export S000_METADATA_DSN='file:/var/lib/s000/data/metadata.db'
export S000_TLS_ENABLED=true
export S000_TLS_CERT_FILE=/etc/s000/tls.crt
export S000_TLS_KEY_FILE=/etc/s000/tls.key
```

### All Configuration Options

| Variable | Default | Description |
|----------|---------|-------------|
| `S000_ADDR` | `:9000` | API server bind address |
| `S000_DATA_DIR` | `./data` | Blob storage root directory |
| `S000_IMPORT_DIRECTORY` | (none) | Optional startup import path; top-level folders become buckets and files are imported as objects |
| `S000_METADATA_BACKEND` | `sqlite` | Metadata backend: `sqlite`, `libsql`, `postgresql`, `mariadb`, `valkey` |
| `S000_METADATA_DSN` | `file:<S000_DATA_DIR>/s000-metadata.db` | Metadata catalog file/connection string (catalog snapshot reloaded at startup for the selected backend) |
| `S000_DOMAIN` | (none) | Virtual-host domain suffix |
| `S000_ADMIN_ACCESS_KEY` | (required) | Bootstrap admin access key |
| `S000_ADMIN_SECRET_KEY` | (required) | Bootstrap admin secret key |
| `S000_PAT_SIGNING_KEY` | (fallback to `S000_ADMIN_SECRET_KEY`) | Signing key for personal access tokens |
| `S000_UI_THEME` | `sysadmin90` | Default embedded UI theme |
| `S000_UI_SSE_DASHBOARD_STATS_INTERVAL` | `2s` | Dashboard API stats SSE refresh interval |
| `S000_UI_SSE_BUCKETS_INTERVAL` | `10s` | Buckets page table SSE refresh interval |
| `S000_UI_SSE_TOKENS_INTERVAL` | `10s` | Tokens page table SSE refresh interval |
| `S000_UI_SSE_OBJECTS_INTERVAL` | `10s` | Objects table SSE refresh interval |
| `S000_UI_SSE_OBJECT_METADATA_INTERVAL` | `10s` | Object metadata SSE refresh interval |
| `S000_TLS_ENABLED` | `false` | Enable TLS |
| `S000_TLS_CERT_FILE` | (none) | TLS certificate path |
| `S000_TLS_KEY_FILE` | (none) | TLS key path |
| `S000_METRICS_PATH` | `/metrics` | Prometheus metrics endpoint |
| `S000_LIFECYCLE_RULES` | (none) | Lifecycle rules (e.g., `prefix=logs/,age=7d`) |
| `S000_HEAVY_OPS_WORKERS` | `4` | Worker pool size for heavy operations |
| `S000_MAX_INFLIGHT` | `128` | Max concurrent requests |

See `observability-spec.md` for full HTTP tuning options.

## Running as a Service

### systemd

```bash
sudo useradd --system --home /var/lib/s000 --shell /usr/sbin/nologin s000
sudo mkdir -p /var/lib/s000/data
sudo chown -R s000:s000 /var/lib/s000
sudo cp deploy/systemd/s000.service /etc/systemd/system/s000.service
sudo systemctl daemon-reload
sudo systemctl enable --now s000
```

### Docker

```bash
docker run --rm -p 9000:9000 \
  -e S000_ADMIN_ACCESS_KEY=admin \
  -e S000_ADMIN_SECRET_KEY=change-me \
  -v s000-data:/var/lib/s000/data \
  s000:release-tag
```

See `deployment-examples.md` for more examples.

## Next Steps

- Read `docs/operations-guide.md` for monitoring, backups, and upgrades.
- Read `docs/tuning-guide.md` for consumer hardware optimization.
- Review `docs/limitations.md` for Release 1 scope and known limitations.
