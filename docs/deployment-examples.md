# Deployment Examples

This document covers minimal packaging/deployment examples for Release 1 portability.

## Container Image

Build image:

```bash
docker build -t s000:local .
```

Run image:

```bash
docker run --rm -p 9000:9000 \
  -e S000_ADMIN_ACCESS_KEY=admin \
  -e S000_ADMIN_SECRET_KEY=change-me \
  -v s000-data:/var/lib/s000/data \
  s000:local
```

After startup, verify:

- API health: `curl -fsS http://127.0.0.1:9000/healthz`
- API readiness: `curl -fsS http://127.0.0.1:9000/readyz`
- HTML client login: `http://127.0.0.1:9000/app/login`

Persistent storage path in the container:

- `/var/lib/s000/data`

You must mount this path to keep data and metadata across restarts.

## Docker Compose

- Example file: `deploy/docker-compose.yml`

Run:

```bash
cd deploy
docker compose up -d --build
```

## systemd Unit

- Example unit file: `deploy/systemd/s000.service`

Typical install flow:

```bash
sudo useradd --system --home /var/lib/s000 --shell /usr/sbin/nologin s000
sudo mkdir -p /var/lib/s000/data
sudo chown -R s000:s000 /var/lib/s000
sudo cp deploy/systemd/s000.service /etc/systemd/system/s000.service
sudo systemctl daemon-reload
sudo systemctl enable --now s000
```

## Low-Resource Validation (2 CPU / 4GB RAM)

Run:

```bash
make validate-low-resource
```

The script builds the local container image, runs it with resource limits (`--cpus=2 --memory=4g`), and verifies `/healthz` and `/readyz`.

## Operational Reminder

- Replace demo credentials (`admin` / `change-me`) before production deployment.
