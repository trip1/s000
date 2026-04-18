# Tuning Guide for Consumer Hardware

This guide covers optimizing s000 for deployment on consumer-grade hardware (2-4 cores, 4-8 GB RAM, SATA SSD).

## Target Hardware Profiles

| Profile | CPU | RAM | Storage | Expected Use |
|---------|-----|-----|---------|--------------|
| Minimum | 2 cores | 4 GB | SATA SSD | Development, light production |
| Recommended | 4+ cores | 8+ GB | NVMe SSD | Production workloads |

## OS-Level Tuning

### File Descriptor Limits

Ensure adequate file descriptors for concurrent connections:

```bash
# Check current limit
ulimit -n

# Increase for production (add to /etc/security/limits.conf)
* soft nofile 65536
* hard nofile 65536
```

### Transparent Huge Pages

Disable transparent huge pages to reduce latency variance:

```bash
# Add to /etc/rc.local or systemd service
echo never > /sys/kernel/mm/transparent_hugepage/enabled
echo never > /sys/kernel/mm/transparent_hugepage/defrag
```

### I/O Scheduler

For SATA SSDs, use the `deadline` or `noop` scheduler:

```bash
# Check current scheduler
cat /sys/block/sda/queue/scheduler

# Set deadline scheduler
echo deadline > /sys/block/sda/queue/scheduler
```

For NVMe, the scheduler usually doesn't matter significantly.

## s000 Configuration Tuning

### HTTP Server Defaults

For consumer hardware, these defaults are generally safe:

```bash
# If experiencing high latency under load, reduce timeouts
export S000_HTTP_READ_TIMEOUT=60s
export S000_HTTP_WRITE_TIMEOUT=60s

# Keep default header sizes unless using large custom headers
# S000_HTTP_MAX_HEADER_BYTES=1048576
```

### Worker Pool Sizing

The default `S000_HEAVY_OPS_WORKERS=4` is tuned for 4+ cores. For 2-core systems:

```bash
export S000_HEAVY_OPS_WORKERS=2
export S000_HEAVY_OPS_QUEUE=32
```

For NVMe-backed storage, you can increase:

```bash
export S000_HEAVY_OPS_WORKERS=8
export S000_HEAVY_OPS_QUEUE=128
```

### Concurrency Limits

```bash
# Reduce on low-end systems
export S000_MAX_INFLIGHT=64
```

### Lifecycle Worker

On 2-core systems, consider increasing interval to reduce CPU impact:

```bash
export S000_LIFECYCLE_INTERVAL=15m
export S000_LIFECYCLE_BATCH_SIZE=50
```

### Memory Considerations

s000's memory footprint is typically under 512 MB under moderate load. Monitor with:

```bash
# Watch memory usage
while true; do
  ps -p $(pgrep s000) -o rss,vsz
  sleep 5
done
```

If memory is constrained:
- Reduce `S000_HEAVY_OPS_QUEUE`
- Reduce `S000_MAX_INFLIGHT`
- Avoid running other memory-intensive services on the same host

## Storage Optimization

### Filesystem Recommendations

- `ext4` or `xfs` for Linux
- Use `noatime` mount option to reduce writes:
  ```
  /dev/sda1 /var/lib/s000/data ext4 noatime,nodiratime 0 2
  ```

### Data Directory Layout

The default layout puts everything under `S000_DATA_ROOT`:

```
data/
├── objects/        # object blobs
├── staging/        # temporary uploads
└── multipart/     # multipart upload parts
```

For better isolation on multiple drives:

```bash
export S000_DATA_ROOT=/mnt/fast-drive/s000
```

## Network Tuning

### Keep-Alive

Keep-alive is enabled by default. To tune:

```bash
# Reduce idle timeout if many short-lived connections
export S000_HTTP_IDLE_TIMEOUT=30s
```

### Max Header Bytes

Only adjust if using very large custom headers (e.g., large user metadata):

```bash
export S000_HTTP_MAX_HEADER_BYTES=2097152  # 2 MB
```

## Monitoring Performance

### Key Metrics to Watch

```bash
# Request latency (should be < 50ms for small objects)
curl -s http://127.0.0.1:9000/metrics | grep s000_http_request_duration

# Error rate
curl -s http://127.0.0.1:9000/metrics | grep s000_http_requests_total | grep ",status=\"5"

# Worker queue depth (should stay low)
curl -s http://127.0.0.1:9000/metrics | grep s000_worker_queue_depth
```

### Benchmarking

Run benchmarks to validate performance on your hardware:

```bash
make bench
```

Expected baseline on AMD Ryzen 9 7900X (NVMe):
- 1 KB objects: ~27,000 ns/op
- 1 MB objects: ~2 ms/op
- 100 MB objects: ~178 ms/op

On 2-core SATA SSD, expect 2-3x slower for large objects.

## Recommended Configurations

### Minimum Profile (2 CPU, 4 GB RAM, SATA SSD)

```bash
export S000_ADMIN_ACCESS_KEY=admin
export S000_ADMIN_SECRET_KEY=<secret>
export S000_ADDR=:9000
export S000_DATA_ROOT=/var/lib/s000/data
export S000_METADATA_DSN='file:/var/lib/s000/data/metadata.db'
export S000_HEAVY_OPS_WORKERS=2
export S000_HEAVY_OPS_QUEUE=32
export S000_MAX_INFLIGHT=64
export S000_LIFECYCLE_INTERVAL=15m
export S000_LIFECYCLE_BATCH_SIZE=50
```

### Recommended Profile (4+ CPU, 8 GB RAM, NVMe)

```bash
export S000_ADMIN_ACCESS_KEY=admin
export S000_ADMIN_SECRET_KEY=<secret>
export S000_ADDR=:9000
export S000_DATA_ROOT=/var/lib/s000/data
export S000_METADATA_DSN='file:/var/lib/s000/data/metadata.db'
export S000_HEAVY_OPS_WORKERS=8
export S000_HEAVY_OPS_QUEUE=128
export S000_MAX_INFLIGHT=128
```

### High-Performance Profile (8+ CPU, 16+ GB RAM, NVMe)

```bash
export S000_ADMIN_ACCESS_KEY=admin
export S000_ADMIN_SECRET_KEY=<secret>
export S000_ADDR=:9000
export S000_DATA_ROOT=/var/lib/s000/data
export S000_METADATA_DSN='file:/var/lib/s000/data/metadata.db'
export S000_HEAVY_OPS_WORKERS=16
export S000_HEAVY_OPS_QUEUE=256
export S000_MAX_INFLIGHT=256
export S000_HTTP_READ_TIMEOUT=60s
export S000_HTTP_WRITE_TIMEOUT=60s
```

## Troubleshooting

### High Latency

1. Check disk I/O: `iostat -x 1`
2. Check CPU: `top` or `htop`
3. Reduce concurrency if disk-bound
4. Consider faster storage

### OOM Kills

1. Reduce `S000_HEAVY_OPS_QUEUE` and `S000_MAX_INFLIGHT`
2. Add more RAM
3. Monitor with `dmesg | grep -i oom`

### Connection Refused

1. Check `ulimit -n`
2. Check if port is in use: `lsof -i :9000`
3. Review logs for errors