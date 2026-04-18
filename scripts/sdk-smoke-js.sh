#!/usr/bin/env bash
set -euo pipefail

AWS_ENDPOINT="${AWS_ENDPOINT:-http://127.0.0.1:9000}"
AWS_REGION="${AWS_REGION:-us-east-1}"
AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-admin}"
AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-change-me}"
BUCKET="${BUCKET:-sdk-js-smoke}"

tmpdir="$(mktemp -d)"
cleanup() { rm -rf "${tmpdir}"; }
trap cleanup EXIT

cat > "${tmpdir}/package.json" <<'EOF'
{
  "name": "sdk-js-smoke",
  "private": true,
  "type": "module",
  "dependencies": {
    "@aws-sdk/client-s3": "^3.873.0"
  }
}
EOF

cat > "${tmpdir}/index.mjs" <<'EOF'
import {
  S3Client,
  CreateBucketCommand,
  PutObjectCommand,
  GetObjectCommand,
  DeleteObjectCommand,
} from "@aws-sdk/client-s3"

const endpoint = process.env.AWS_ENDPOINT || "http://127.0.0.1:9000"
const region = process.env.AWS_REGION || "us-east-1"
const accessKeyId = process.env.AWS_ACCESS_KEY_ID || "admin"
const secretAccessKey = process.env.AWS_SECRET_ACCESS_KEY || "change-me"
const bucket = process.env.BUCKET || "sdk-js-smoke"

const client = new S3Client({
  endpoint,
  region,
  forcePathStyle: true,
  credentials: { accessKeyId, secretAccessKey },
})

try {
  await client.send(new CreateBucketCommand({ Bucket: bucket }))
} catch (_) {}

await client.send(new PutObjectCommand({ Bucket: bucket, Key: "smoke.txt", Body: "hello" }))
await client.send(new GetObjectCommand({ Bucket: bucket, Key: "smoke.txt" }))
await client.send(new DeleteObjectCommand({ Bucket: bucket, Key: "smoke.txt" }))

console.log("js sdk smoke passed")
EOF

(
  cd "${tmpdir}"
  npm install --silent
  AWS_ENDPOINT="${AWS_ENDPOINT}" \
  AWS_REGION="${AWS_REGION}" \
  AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}" \
  AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}" \
  BUCKET="${BUCKET}" \
  node index.mjs
)
