package metadata

import "fmt"

// MigrationPlan returns the relational schema migration plan by backend.
func MigrationPlan(backend Backend) ([]Migration, error) {
	switch backend {
	case BackendSQLite, BackendLibSQL:
		return []Migration{
			{
				Version: 1,
				Name:    "init_schema",
				UpSQL: `
CREATE TABLE IF NOT EXISTS buckets (
  name TEXT PRIMARY KEY,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS object_versions (
  bucket TEXT NOT NULL,
  object_key TEXT NOT NULL,
  version_id TEXT NOT NULL,
  size INTEGER NOT NULL,
  etag TEXT NOT NULL,
  checksum_sha256 TEXT NOT NULL,
  delete_marker INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (bucket, object_key, version_id)
);

CREATE TABLE IF NOT EXISTS multipart_uploads (
  upload_id TEXT PRIMARY KEY,
  bucket TEXT NOT NULL,
  object_key TEXT NOT NULL,
  initiated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS multipart_parts (
  upload_id TEXT NOT NULL,
  part_number INTEGER NOT NULL,
  etag TEXT NOT NULL,
  size INTEGER NOT NULL,
  checksum_sha256 TEXT NOT NULL,
  PRIMARY KEY (upload_id, part_number)
);

CREATE TABLE IF NOT EXISTS credential_records (
  access_key_id TEXT PRIMARY KEY,
  secret_hash TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  rotated_at TEXT
);

CREATE TABLE IF NOT EXISTS bucket_websites (
  bucket TEXT PRIMARY KEY,
  index_document TEXT NOT NULL,
  error_document TEXT NOT NULL,
  redirect_all_host TEXT NOT NULL,
  redirect_all_protocol TEXT NOT NULL,
  enabled INTEGER NOT NULL,
  public_read INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS bucket_cors (
  bucket TEXT PRIMARY KEY,
  allowed_origins TEXT NOT NULL,
  allowed_methods TEXT NOT NULL,
  allowed_headers TEXT NOT NULL,
  expose_headers TEXT NOT NULL,
  max_age_seconds INTEGER NOT NULL,
  enabled INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS bucket_policies (
  bucket TEXT PRIMARY KEY,
  document TEXT NOT NULL,
  enabled INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS bucket_public_access_blocks (
  bucket TEXT PRIMARY KEY,
  block_public_acls INTEGER NOT NULL,
  ignore_public_acls INTEGER NOT NULL,
  block_public_policy INTEGER NOT NULL,
  restrict_public_buckets INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_object_versions_bucket_key ON object_versions(bucket, object_key);
CREATE INDEX IF NOT EXISTS idx_object_versions_bucket_prefix ON object_versions(bucket, object_key, created_at);
CREATE INDEX IF NOT EXISTS idx_multipart_parts_upload_id ON multipart_parts(upload_id);
`,
			},
			{
				Version: 2,
				Name:    "schema_migrations_table",
				UpSQL: `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  applied_at TEXT NOT NULL
);
`,
			},
		}, nil
	case BackendPostgreSQL:
		return []Migration{
			{
				Version: 1,
				Name:    "init_schema",
				UpSQL: `
CREATE TABLE IF NOT EXISTS buckets (
  name TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS object_versions (
  bucket TEXT NOT NULL,
  object_key TEXT NOT NULL,
  version_id TEXT NOT NULL,
  size BIGINT NOT NULL,
  etag TEXT NOT NULL,
  checksum_sha256 TEXT NOT NULL,
  delete_marker BOOLEAN NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (bucket, object_key, version_id)
);

CREATE TABLE IF NOT EXISTS multipart_uploads (
  upload_id TEXT PRIMARY KEY,
  bucket TEXT NOT NULL,
  object_key TEXT NOT NULL,
  initiated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS multipart_parts (
  upload_id TEXT NOT NULL,
  part_number INTEGER NOT NULL,
  etag TEXT NOT NULL,
  size BIGINT NOT NULL,
  checksum_sha256 TEXT NOT NULL,
  PRIMARY KEY (upload_id, part_number)
);

CREATE TABLE IF NOT EXISTS credential_records (
  access_key_id TEXT PRIMARY KEY,
  secret_hash TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  rotated_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS bucket_websites (
  bucket TEXT PRIMARY KEY,
  index_document TEXT NOT NULL,
  error_document TEXT NOT NULL,
  redirect_all_host TEXT NOT NULL,
  redirect_all_protocol TEXT NOT NULL,
  enabled BOOLEAN NOT NULL,
  public_read BOOLEAN NOT NULL
);

CREATE TABLE IF NOT EXISTS bucket_cors (
  bucket TEXT PRIMARY KEY,
  allowed_origins TEXT NOT NULL,
  allowed_methods TEXT NOT NULL,
  allowed_headers TEXT NOT NULL,
  expose_headers TEXT NOT NULL,
  max_age_seconds INTEGER NOT NULL,
  enabled BOOLEAN NOT NULL
);

CREATE TABLE IF NOT EXISTS bucket_policies (
  bucket TEXT PRIMARY KEY,
  document TEXT NOT NULL,
  enabled BOOLEAN NOT NULL
);

CREATE TABLE IF NOT EXISTS bucket_public_access_blocks (
  bucket TEXT PRIMARY KEY,
  block_public_acls BOOLEAN NOT NULL,
  ignore_public_acls BOOLEAN NOT NULL,
  block_public_policy BOOLEAN NOT NULL,
  restrict_public_buckets BOOLEAN NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_object_versions_bucket_key ON object_versions(bucket, object_key);
CREATE INDEX IF NOT EXISTS idx_object_versions_bucket_prefix ON object_versions(bucket, object_key, created_at);
CREATE INDEX IF NOT EXISTS idx_multipart_parts_upload_id ON multipart_parts(upload_id);
`,
			},
			{
				Version: 2,
				Name:    "schema_migrations_table",
				UpSQL: `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  applied_at TIMESTAMPTZ NOT NULL
);
`,
			},
		}, nil
	case BackendMariaDB:
		return []Migration{
			{
				Version: 1,
				Name:    "init_schema",
				UpSQL: `
CREATE TABLE IF NOT EXISTS buckets (
  name VARCHAR(255) PRIMARY KEY,
  created_at DATETIME(6) NOT NULL
);

CREATE TABLE IF NOT EXISTS object_versions (
  bucket VARCHAR(255) NOT NULL,
  object_key VARCHAR(1024) NOT NULL,
  version_id VARCHAR(255) NOT NULL,
  size BIGINT NOT NULL,
  etag VARCHAR(255) NOT NULL,
  checksum_sha256 VARCHAR(128) NOT NULL,
  delete_marker TINYINT(1) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (bucket, object_key(255), version_id)
);

CREATE TABLE IF NOT EXISTS multipart_uploads (
  upload_id VARCHAR(255) PRIMARY KEY,
  bucket VARCHAR(255) NOT NULL,
  object_key VARCHAR(1024) NOT NULL,
  initiated_at DATETIME(6) NOT NULL
);

CREATE TABLE IF NOT EXISTS multipart_parts (
  upload_id VARCHAR(255) NOT NULL,
  part_number INT NOT NULL,
  etag VARCHAR(255) NOT NULL,
  size BIGINT NOT NULL,
  checksum_sha256 VARCHAR(128) NOT NULL,
  PRIMARY KEY (upload_id, part_number)
);

CREATE TABLE IF NOT EXISTS credential_records (
  access_key_id VARCHAR(255) PRIMARY KEY,
  secret_hash VARCHAR(255) NOT NULL,
  status VARCHAR(64) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  rotated_at DATETIME(6) NULL
);

CREATE TABLE IF NOT EXISTS bucket_websites (
  bucket VARCHAR(255) PRIMARY KEY,
  index_document VARCHAR(1024) NOT NULL,
  error_document VARCHAR(1024) NOT NULL,
  redirect_all_host VARCHAR(1024) NOT NULL,
  redirect_all_protocol VARCHAR(32) NOT NULL,
  enabled TINYINT(1) NOT NULL,
  public_read TINYINT(1) NOT NULL
);

CREATE TABLE IF NOT EXISTS bucket_cors (
  bucket VARCHAR(255) PRIMARY KEY,
  allowed_origins TEXT NOT NULL,
  allowed_methods TEXT NOT NULL,
  allowed_headers TEXT NOT NULL,
  expose_headers TEXT NOT NULL,
  max_age_seconds INT NOT NULL,
  enabled TINYINT(1) NOT NULL
);

CREATE TABLE IF NOT EXISTS bucket_policies (
  bucket VARCHAR(255) PRIMARY KEY,
  document LONGTEXT NOT NULL,
  enabled TINYINT(1) NOT NULL
);

CREATE TABLE IF NOT EXISTS bucket_public_access_blocks (
  bucket VARCHAR(255) PRIMARY KEY,
  block_public_acls TINYINT(1) NOT NULL,
  ignore_public_acls TINYINT(1) NOT NULL,
  block_public_policy TINYINT(1) NOT NULL,
  restrict_public_buckets TINYINT(1) NOT NULL
);

CREATE INDEX idx_object_versions_bucket_key ON object_versions(bucket, object_key(255));
CREATE INDEX idx_object_versions_bucket_prefix ON object_versions(bucket, object_key(255), created_at);
CREATE INDEX idx_multipart_parts_upload_id ON multipart_parts(upload_id);
`,
			},
			{
				Version: 2,
				Name:    "schema_migrations_table",
				UpSQL: `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INT PRIMARY KEY,
  name VARCHAR(255) NOT NULL,
  applied_at DATETIME(6) NOT NULL
);
`,
			},
		}, nil
	case BackendValkey:
		return nil, fmt.Errorf("backend %q does not support relational migrations", backend)
	default:
		return nil, fmt.Errorf("unsupported metadata backend %q", backend)
	}
}
