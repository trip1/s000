#!/usr/bin/env bash
set -euo pipefail

IMAGE_TAG="${IMAGE_TAG:-s000:local}"
CONTAINER_NAME="${CONTAINER_NAME:-s000-low-resource-check}"

cleanup() {
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker build -t "${IMAGE_TAG}" .

docker run -d --name "${CONTAINER_NAME}" \
  --cpus="2.0" \
  --memory="4g" \
  -p 19000:9000 \
  -e S000_ADMIN_ACCESS_KEY=admin \
  -e S000_ADMIN_SECRET_KEY=change-me \
  "${IMAGE_TAG}"

for _ in $(seq 1 30); do
  if ! docker ps --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
    docker logs "${CONTAINER_NAME}" || true
    echo "container exited before health checks"
    exit 1
  fi
  if curl -fsS "http://127.0.0.1:19000/healthz" >/dev/null; then
    break
  fi
  sleep 1
done

curl -fsS "http://127.0.0.1:19000/healthz" >/dev/null
curl -fsS "http://127.0.0.1:19000/readyz" >/dev/null

echo "low-resource validation passed (2 CPU / 4GB RAM)"
