# Compatibility Matrix (Section 13)

Status key:

- `pass`: covered by tests/scripts and expected to work
- `partial`: behavior present with known caveats
- `todo`: not validated yet

| API / Flow | Status | Validation |
|---|---|---|
| ListBuckets | pass | `internal/server/s3_api_test.go` |
| CreateBucket | pass | `internal/server/s3_api_test.go` |
| DeleteBucket (empty only) | pass | `internal/server/s3_api_test.go` |
| GetBucketLocation | pass | `internal/server/s3_api_test.go` |
| PutBucketVersioning / GetBucketVersioning | pass | `internal/server/s3_api_test.go` |
| ListObjectsV2 | pass | `internal/server/s3_api_test.go` |
| PutObject | pass | `internal/server/s3_api_test.go`, `scripts/awscli-e2e.sh` |
| GetObject | pass | `internal/server/s3_api_test.go`, `scripts/awscli-e2e.sh` |
| HeadObject | pass | `internal/server/s3_api_test.go` |
| DeleteObject | pass | `internal/server/s3_api_test.go` |
| CopyObject | pass | `internal/server/s3_api_test.go` |
| Multipart (create/upload/list/complete/abort) | pass | `internal/server/s3_api_test.go`, `scripts/awscli-e2e.sh` |
| aws-cli `ls` / `cp` / `sync` | pass | `scripts/awscli-e2e.sh` |
| Go SDK smoke | pass | `scripts/sdk-smoke-go.sh` |
| Python SDK smoke | pass | `scripts/sdk-smoke-python.sh` |
| JS SDK smoke | pass | `scripts/sdk-smoke-js.sh` |
| S3 error code parity (common failures) | pass | `internal/server/s3_api_test.go` |
| Website static layouts (index/error/docs + SPA shell fallback pattern) | pass | `test/compatibility/website_layout_compatibility_test.go` |
