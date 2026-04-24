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

Optional one-time startup import from host data:

```bash
docker run --rm -p 9000:9000 \
  -e S000_ADMIN_ACCESS_KEY=admin \
  -e S000_ADMIN_SECRET_KEY=change-me \
  -e S000_IMPORT_DIRECTORY=/import \
  -v s000-data:/var/lib/s000/data \
  -v ./seed-data:/import:ro \
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

## Automated Install Script

An installer is available at repository root: `install.sh`.

It can:
- install required download/runtime dependencies,
- download the requested release artifact from GitHub,
- install `s000` to `/usr/local/bin` (or custom path),
- configure and start a service on supported init systems (`systemd`, `openrc`).

Examples:

```bash
curl -fsSL https://raw.githubusercontent.com/trip1/s000/master/install.sh | sudo bash
curl -fsSL https://raw.githubusercontent.com/trip1/s000/master/install.sh | sudo bash -s -- --version v0.1.0 --init systemd
curl -fsSL https://raw.githubusercontent.com/trip1/s000/master/install.sh | sudo bash -s -- --init openrc --access-key admin --secret-key 'change-me'
```

See full options with:

```bash
sudo ./install.sh --help
```

## Low-Resource Validation (2 CPU / 4GB RAM)

Run:

```bash
make validate-low-resource
```

The script builds the local container image, runs it with resource limits (`--cpus=2 --memory=4g`), and verifies `/healthz` and `/readyz`.

## Operational Reminder

- Replace demo credentials (`admin` / `change-me`) before production deployment.
