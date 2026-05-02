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
| Multipart checksums and ETag semantics | pass | `internal/server/s3_api_test.go` |
| ListObjectVersions / delete markers / versionId delete | pass | `internal/server/s3_api_test.go` |
| Conditional GET/HEAD/CopyObject headers | pass | `internal/server/s3_api_test.go` |
| CORS configuration | pass | `internal/server/s3_api_test.go` |
| Bucket policy persistence | pass | `internal/server/s3_api_test.go` |
| Public access block persistence | pass | `internal/server/s3_api_test.go` |
| Lifecycle configuration and expiration execution | pass | `internal/server/s3_api_test.go`, `internal/lifecycle/worker_test.go` |
| Object tagging | pass | `internal/server/s3_api_test.go` |
| ACL compatibility | partial | Owner full-control XML and common canned ACL no-op handling in `internal/server/s3_api_test.go` |
| SSE-S3 single-part and multipart | pass | `internal/server/s3_api_test.go` |
| Object lock retention/legal hold | pass | `internal/server/s3_api_test.go` |
| Bucket notifications (webhook) | partial | Best-effort HTTP delivery in `internal/server/s3_api_test.go` |
| Bucket replication (HTTP target) | partial | Best-effort object-create replication in `internal/server/s3_api_test.go` |
| aws-cli `ls` / `cp` / `sync` | pass | `scripts/awscli-e2e.sh` |
| Go SDK smoke | pass | `scripts/sdk-smoke-go.sh` |
| Python SDK smoke | pass | `scripts/sdk-smoke-python.sh` |
| JS SDK smoke | pass | `scripts/sdk-smoke-js.sh` |
| rclone flows | todo | Not automated yet |
| restic flows | todo | Not automated yet |
| MinIO `mc` flows | todo | Not automated yet |
| s3cmd flows | todo | Not automated yet |
| S3 error code parity (common failures) | pass | `internal/server/s3_api_test.go` |
| Website static layouts (index/error/docs + SPA shell fallback pattern) | pass | `test/compatibility/website_layout_compatibility_test.go` |
