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
```

These credentials are used for:
- SigV4 API authentication
- HTML client login (`/app/login`)
- CLI admin operations

### 3. Start the Server

```bash
go run ./cmd/s000
```

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
```

### 5. Enable WASM Functions (Hot Reload Path)

This path does not require calling function CRUD endpoints directly. s000 loads function manifests from disk.

#### 5.1 Configure runtime env vars

```bash
export S000_FUNCTIONS_ENABLED=true
export S000_FUNCTIONS_RUNTIME=wazero
export S000_FUNCTIONS_DIR=./functions
export S000_FUNCTIONS_HOT_RELOAD=true
export S000_FUNCTIONS_RELOAD_INTERVAL=2s
```

Optional production hardening knobs:

```bash
export S000_FUNCTIONS_RATE_LIMIT_PER_MINUTE=120
export S000_FUNCTIONS_MAX_CONCURRENT=8
export S000_FUNCTIONS_DAILY_QUOTA=10000
export S000_FUNCTIONS_ALERT_ERROR_COUNT_THRESHOLD=25
```

#### 5.2 Create one sample WASM function

Create source file (`functions/block_prefix.go`):

```go
package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
)

func main() {
	in, _ := io.ReadAll(os.Stdin)
	evt := map[string]any{}
	_ = json.Unmarshal(in, &evt)
	key, _ := evt["key"].(string)
	if strings.HasPrefix(key, "blocked/") {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"continue": false,
			"output":   map[string]any{"reason": "blocked prefix"},
		})
		return
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"continue": true})
}
```

Compile with TinyGo (WASI target):

```bash
tinygo build -target=wasi -o functions/block_prefix.wasm functions/block_prefix.go
```

#### 5.3 Register function manifest

Create manifest (`functions/block-prefix.json`):

```json
{
  "name": "block-prefix",
  "runtime": "wazero",
  "trigger": "onPutObjectPre",
  "priority": 100,
  "enabled": true,
  "module_path": "block_prefix.wasm"
}
```

Restart the server (`go run ./cmd/s000`) and verify behavior:

```bash
# allowed
echo ok | aws --endpoint-url http://127.0.0.1:9000 s3 cp - s3://my-bucket/allowed/file.txt

# blocked by function pre-hook
echo no | aws --endpoint-url http://127.0.0.1:9000 s3 cp - s3://my-bucket/blocked/file.txt
```

Inspect function runtime state:

```bash
go run ./cmd/s000ctl functions-metrics --endpoint http://127.0.0.1:9000
go run ./cmd/s000ctl functions-alerts --endpoint http://127.0.0.1:9000
go run ./cmd/s000ctl functions-logs --endpoint http://127.0.0.1:9000 --limit 50
```

#### Web UI

Open http://127.0.0.1:9000/app/login in a browser. Log in with the admin credentials configured in step 2.

#### CLI Tooling

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
| `S000_METADATA_BACKEND` | `sqlite` | Metadata backend: `sqlite`, `libsql`, `postgresql`, `mariadb`, `valkey` |
| `S000_METADATA_DSN` | `file:./data/s000-metadata.db` | Metadata connection string |
| `S000_DOMAIN` | (none) | Virtual-host domain suffix |
| `S000_ADMIN_ACCESS_KEY` | (required) | Bootstrap admin access key |
| `S000_ADMIN_SECRET_KEY` | (required) | Bootstrap admin secret key |
| `S000_TLS_ENABLED` | `false` | Enable TLS |
| `S000_TLS_CERT_FILE` | (none) | TLS certificate path |
| `S000_TLS_KEY_FILE` | (none) | TLS key path |
| `S000_METRICS_PATH` | `/metrics` | Prometheus metrics endpoint |
| `S000_LIFECYCLE_RULES` | (none) | Lifecycle rules (e.g., `prefix=logs/,age=7d`) |
| `S000_HEAVY_OPS_WORKERS` | `4` | Worker pool size for heavy operations |
| `S000_MAX_INFLIGHT` | `128` | Max concurrent requests |
| `S000_FUNCTIONS_ENABLED` | `false` | Enable WASM functions runtime |
| `S000_FUNCTIONS_RUNTIME` | `wazero` | Function runtime (`wazero`, `wasmer`) |
| `S000_FUNCTIONS_DIR` | `./functions` | Function manifest/module directory |
| `S000_FUNCTIONS_HOT_RELOAD` | `false` | Enable manifest polling and auto reload |
| `S000_FUNCTIONS_RATE_LIMIT_PER_MINUTE` | `0` | Per-function invocations/minute (0 disables) |
| `S000_FUNCTIONS_MAX_CONCURRENT` | `0` | Per-function concurrent invocations (0 disables) |
| `S000_FUNCTIONS_DAILY_QUOTA` | `0` | Per-function daily invocation quota (0 disables) |
| `S000_FUNCTIONS_ALERT_ERROR_COUNT_THRESHOLD` | `10` | Error count threshold for function alerts |

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
