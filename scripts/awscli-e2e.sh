#!/usr/bin/env bash
set -euo pipefail

AWS_ENDPOINT="${AWS_ENDPOINT:-http://127.0.0.1:9000}"
AWS_REGION="${AWS_REGION:-us-east-1}"
AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-admin}"
AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-change-me}"
BUCKET="${BUCKET:-compat-e2e}"

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "${tmpdir}"
}
trap cleanup EXIT

export AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_REGION AWS_DEFAULT_REGION="${AWS_REGION}"

echo "hello" > "${tmpdir}/hello.txt"
mkdir -p "${tmpdir}/sync-src"
echo "sync-one" > "${tmpdir}/sync-src/a.txt"
echo "sync-two" > "${tmpdir}/sync-src/b.txt"

aws --endpoint-url "${AWS_ENDPOINT}" s3 mb "s3://${BUCKET}" || true
aws --endpoint-url "${AWS_ENDPOINT}" s3 ls

aws --endpoint-url "${AWS_ENDPOINT}" s3 cp "${tmpdir}/hello.txt" "s3://${BUCKET}/hello.txt"
aws --endpoint-url "${AWS_ENDPOINT}" s3 cp "s3://${BUCKET}/hello.txt" "${tmpdir}/hello.out"

aws --endpoint-url "${AWS_ENDPOINT}" s3 sync "${tmpdir}/sync-src" "s3://${BUCKET}/sync/"
aws --endpoint-url "${AWS_ENDPOINT}" s3 sync "s3://${BUCKET}/sync/" "${tmpdir}/sync-out"

upload_id="$(aws --endpoint-url "${AWS_ENDPOINT}" s3api create-multipart-upload --bucket "${BUCKET}" --key "multi.bin" --query UploadId --output text)"

aws --endpoint-url "${AWS_ENDPOINT}" s3api upload-part \
  --bucket "${BUCKET}" \
  --key "multi.bin" \
  --part-number 1 \
  --upload-id "${upload_id}" \
  --body "${tmpdir}/hello.txt" >/dev/null

etag1="$(aws --endpoint-url "${AWS_ENDPOINT}" s3api list-parts --bucket "${BUCKET}" --key "multi.bin" --upload-id "${upload_id}" --query 'Parts[0].ETag' --output text)"

cat > "${tmpdir}/parts.json" <<EOF
{
  "Parts": [
    {"ETag": ${etag1}, "PartNumber": 1}
  ]
}
EOF

aws --endpoint-url "${AWS_ENDPOINT}" s3api complete-multipart-upload \
  --bucket "${BUCKET}" \
  --key "multi.bin" \
  --upload-id "${upload_id}" \
  --multipart-upload "file://${tmpdir}/parts.json" >/dev/null

echo "aws-cli compatibility flow passed"
