#!/usr/bin/env bash
set -euo pipefail

AWS_ENDPOINT="${AWS_ENDPOINT:-http://127.0.0.1:9000}"
AWS_REGION="${AWS_REGION:-us-east-1}"
AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-admin}"
AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-change-me}"
BUCKET="${BUCKET:-sdk-go-smoke}"

tmpdir="$(mktemp -d)"
cleanup() { rm -rf "${tmpdir}"; }
trap cleanup EXIT

cat > "${tmpdir}/main.go" <<'EOF'
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
	ctx := context.Background()
	endpoint := mustEnv("AWS_ENDPOINT")
	region := mustEnv("AWS_REGION")
	access := mustEnv("AWS_ACCESS_KEY_ID")
	secret := mustEnv("AWS_SECRET_ACCESS_KEY")
	bucket := mustEnv("BUCKET")

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(access, secret, "")),
	)
	if err != nil { panic(err) }

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	_, _ = client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})

	_, err = client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String("smoke.txt"), Body: bytes.NewReader([]byte("hello"))})
	if err != nil { panic(err) }

	obj, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String("smoke.txt")})
	if err != nil { panic(err) }
	defer obj.Body.Close()
	b, _ := io.ReadAll(obj.Body)
	if string(b) != "hello" { panic("unexpected body") }

	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String("smoke.txt")})
	if err != nil { panic(err) }

	fmt.Println("go sdk smoke passed")
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" { panic("missing env " + key) }
	return v
}
EOF

cat > "${tmpdir}/go.mod" <<'EOF'
module sdksmoke

go 1.25.0
EOF

(
  cd "${tmpdir}"
  export AWS_ENDPOINT AWS_REGION AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY BUCKET
  go mod tidy
  go run .
)
