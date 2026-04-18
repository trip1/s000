#!/usr/bin/env bash
set -euo pipefail

AWS_ENDPOINT="${AWS_ENDPOINT:-http://127.0.0.1:9000}"
AWS_REGION="${AWS_REGION:-us-east-1}"
AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-admin}"
AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-change-me}"
BUCKET="${BUCKET:-sdk-python-smoke}"

python3 - <<'PY'
import os
import boto3

endpoint = os.environ.get("AWS_ENDPOINT", "http://127.0.0.1:9000")
region = os.environ.get("AWS_REGION", "us-east-1")
access = os.environ.get("AWS_ACCESS_KEY_ID", "admin")
secret = os.environ.get("AWS_SECRET_ACCESS_KEY", "change-me")
bucket = os.environ.get("BUCKET", "sdk-python-smoke")

s3 = boto3.client(
    "s3",
    endpoint_url=endpoint,
    region_name=region,
    aws_access_key_id=access,
    aws_secret_access_key=secret,
)

try:
    s3.create_bucket(Bucket=bucket)
except Exception:
    pass

s3.put_object(Bucket=bucket, Key="smoke.txt", Body=b"hello")
body = s3.get_object(Bucket=bucket, Key="smoke.txt")["Body"].read()
assert body == b"hello"
s3.delete_object(Bucket=bucket, Key="smoke.txt")

print("python sdk smoke passed")
PY
