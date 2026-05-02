# S3 Compatibility TODO

## High Value
- [x] Execute S3 lifecycle XML rules from `?lifecycle` in the lifecycle worker.
- [x] Add versioning parity: `ListObjectVersions`, specific `versionId` delete, delete-marker listing/pagination markers, and stricter null-version overwrite/delete behavior.
- [x] Add conditional request support: `If-Match`, `If-None-Match`, `If-Modified-Since`, `If-Unmodified-Since`, and `x-amz-copy-source-if-*`.
- [x] Add checksum parity: single-part and multipart PUT/GET/HEAD CRC32, CRC32C, SHA1, and SHA256 checksum validation/headers.
- [x] Add multipart edge parity: min part size, max part count, paginated `ListParts`, paginated `ListMultipartUploads`, multipart ETag semantics, and multipart checksum behavior.

## Medium Value
- [x] Add ACL compatibility: basic/canned ACLs or accurate S3-style unsupported responses for `?acl`.
- [x] Add SSE-S3: `x-amz-server-side-encryption: AES256`, encrypted blob writes, and encryption metadata.
- [x] Add SSE-S3 multipart upload encryption intent and completion support.
- [x] Add object lock/retention: retention mode/date, legal hold, and WORM behavior.
- [x] Add bucket notifications with webhook/NATS-style delivery targets.
- [x] Add async replication to another `s000` or S3-compatible target.

## Compatibility Validation
- [ ] Add real-client flows for `rclone`.
- [ ] Add real-client flows for `restic`.
- [ ] Add real-client flows for MinIO `mc`.
- [ ] Add real-client flows for `s3cmd`.
- [ ] Add SDK multipart/tagging/lifecycle compatibility tests.
