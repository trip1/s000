# Bucket Folder Support Spec

## 1. Purpose

Add practical "folder" support inside buckets while preserving S3-compatible behavior.

In S3, folders are virtual (derived from object key prefixes), not first-class filesystem directories. This spec standardizes that behavior in `s000` and defines UI ergonomics for creating, browsing, and deleting folder-like paths.

## 2. Problem Statement

Current object keys already allow `/`, but folder workflows are incomplete:

- `ListObjectsV2` currently drops nested keys when `delimiter` is set but does not return `CommonPrefixes`.
- There is no explicit operator flow in the embedded UI to create/delete folder markers.
- Pagination behavior is based only on object keys and is undefined for mixed object/prefix listings.

Result: clients can store nested keys, but folder navigation and compatibility with common S3 tooling are inconsistent.

## 3. Goals

- Make folder navigation S3-compatible using `prefix` + `delimiter=/` semantics.
- Return `CommonPrefixes` in `ListObjectsV2` responses.
- Support conventional folder marker objects (`key` ending in `/`, zero-byte payload).
- Expose folder workflows in the embedded UI without introducing a new storage primitive.
- Keep metadata model backend-agnostic (no required schema migration for base support).

## 4. Non-Goals

- No new first-class "Folder" table/entity in metadata.
- No POSIX directory semantics (permissions, rename transactions, hard links).
- No recursive server-side delete API in S3 surface (batch recursive delete can be added later as UI helper).
- No changes to object versioning model beyond normal object behavior.

## 5. Definitions

- Folder (virtual): a common key prefix segment ending with `/` when listing with `delimiter=/`.
- Folder marker: a normal object with a key that ends in `/` and usually zero bytes.
- Child object: an object where `strings.HasPrefix(key, prefix)`.

## 6. Functional Requirements

### 6.1 Key rules

- Keys may contain `/` at any position.
- A key ending in `/` is allowed and treated as a normal object for PUT/GET/HEAD/DELETE.
- No additional server-side key normalization is introduced in this scope.

### 6.2 `ListObjectsV2` behavior

For `GET /{bucket}?list-type=2`:

- Honor existing `prefix`, `delimiter`, `max-keys`, and `continuation-token`.
- When `delimiter` is empty: return only `Contents` (current behavior).
- When `delimiter=/`:
  - Return direct child objects in `Contents`.
  - Return direct child folder prefixes in `CommonPrefixes`.
  - Deduplicate `CommonPrefixes`.
  - Sort response entries lexicographically by key/prefix.
- `KeyCount` must reflect total returned items (`len(Contents) + len(CommonPrefixes)`).
- `IsTruncated` and `NextContinuationToken` must reflect combined pagination surface (not only objects).

### 6.3 Pagination contract

`continuation-token` becomes an opaque token for folder-aware listings.

- Token payload should encode:
  - listing mode (`prefix`, `delimiter`),
  - last emitted entry kind (`object` or `prefix`),
  - last emitted value,
  - optional schema version.
- Token must be base64url-encoded and validated on parse.
- Invalid token returns `InvalidArgument` with S3-style XML error body.
- Token is only valid for matching `prefix`/`delimiter` inputs.

Note: this implementation does not preserve legacy plain-key continuation tokens; all continuation tokens are opaque.

### 6.4 Embedded UI behavior

Object browser (`/app/buckets/:bucket/objects`):

- Default listing uses `delimiter=/`.
- Show folders first (from `CommonPrefixes`), then objects.
- Add breadcrumb navigation derived from current `prefix`.
- Add "Create folder" action that writes zero-byte marker object `<prefix><name>/`.
- Add "Delete folder marker" action for marker objects; do not recursively delete child objects in this phase.
- Keep existing object upload/download/delete behavior.

### 6.5 Compatibility expectations

Must interoperate with:

- `aws s3 ls s3://bucket/ --recursive` (flat listing)
- `aws s3 ls s3://bucket/prefix/` (folder-style listing)
- SDK `ListObjectsV2` calls that depend on `CommonPrefixes`.

## 7. API and Data Shape Changes

### 7.1 XML response additions for `ListObjectsV2`

Add/confirm fields in `ListBucketResult`:

- `KeyCount`
- `CommonPrefixes` entries of shape:
  - `<CommonPrefixes><Prefix>photos/2026/</Prefix></CommonPrefixes>`

### 7.2 Internal listing abstraction

Introduce an internal normalized listing entry model for pagination logic:

- `type: object | prefix`
- `value: string` (object key or common prefix)

This allows deterministic mixed pagination independent of output XML assembly.

### 7.3 Metadata layer

Base implementation can continue using existing `ListObjects(bucket)` + in-memory projection.

Optional follow-up for scale:

- Add `ListObjectsPage(bucket, prefix, startAfter, limit)` to store interface.
- Push filtering and pagination into backend-specific adapters.

## 8. Error Handling

- Invalid continuation token format -> `400 Bad Request`, `Code=InvalidArgument`.
- Missing bucket -> existing `NoSuchBucket` behavior.
- Unsupported delimiter values: accept arbitrary delimiter for compatibility, but only `/` is exercised in tests and UI defaults.

## 9. Testing Plan

### 9.1 Unit tests

In `internal/server/s3_api_test.go`:

- `ListObjectsV2` returns `CommonPrefixes` with `delimiter=/`.
- `CommonPrefixes` dedupes multiple descendants.
- `KeyCount` matches returned object+prefix count.
- Pagination across mixed object/prefix entries is stable.
- Legacy continuation tokens still work for non-delimiter listings.
- Invalid token returns `InvalidArgument`.

In UI tests:

- Prefix navigation renders breadcrumbs.
- Create-folder action writes marker key ending in `/`.
- Folder rows appear in object browser partials.

### 9.2 Integration tests

In `test/integration`:

- Upload keys under nested paths, verify `list-type=2&delimiter=/` XML contains both `Contents` and `CommonPrefixes`.
- Verify continuation-token paging over mixed entries.
- Verify folder marker lifecycle (PUT marker, list shows marker/object as expected, DELETE marker removes it).

### 9.3 Compatibility tests

- Extend compatibility suite to include at least one `aws s3 ls` folder-navigation scenario.

## 10. Rollout Plan

1. Implement S3 API response/model updates (`CommonPrefixes`, `KeyCount`, token parser/encoder).
2. Add server/unit/integration tests for mixed listing behavior.
3. Update embedded UI object browser to default `delimiter=/` and support folder actions.
4. Document behavior in `docs/compatibility-matrix.md`, `docs/limitations.md`, and quickstart examples.

## 11. Risks and Mitigations

- Pagination regressions: mitigate with deterministic entry model + exhaustive tests.
- Client token incompatibility: mitigate with dual token parsing (legacy + opaque).
- Large bucket listing cost: accept initially; mitigate later with backend paging API.
- User confusion on recursive delete: UI copy must explicitly state marker-only delete in this phase.

## 12. Acceptance Criteria

- `ListObjectsV2` with `delimiter=/` returns `CommonPrefixes` and valid `KeyCount`.
- Folder-style navigation works in embedded UI with breadcrumbs.
- Operators can create folder markers from UI and via normal object PUT.
- Existing flat object APIs remain backward compatible.
- `make test` and `make integration` pass with new coverage.
