package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type sqlDialect string

const (
	sqlDialectSQLite     sqlDialect = "sqlite"
	sqlDialectPostgreSQL sqlDialect = "postgresql"
	sqlDialectMariaDB    sqlDialect = "mariadb"
)

type sqliteStore struct {
	db      *sql.DB
	now     func() time.Time
	dialect sqlDialect
}

type sqliteTxStore struct {
	tx      *sql.Tx
	dialect sqlDialect
}

func newSQLiteStore(cfg Config) (*sqliteStore, error) {
	dialect := dialectForBackend(cfg.Backend)
	db := cfg.SQLDB
	if db == nil {
		var err error
		driver := "sqlite"
		if cfg.Backend == BackendLibSQL {
			driver = "libsql"
		} else if cfg.Backend == BackendPostgreSQL {
			driver = "pgx"
		} else if cfg.Backend == BackendMariaDB {
			driver = "mysql"
		}
		db, err = sql.Open(driver, cfg.DSN)
		if err != nil {
			return nil, fmt.Errorf("open %s metadata: %w", cfg.Backend, err)
		}
	}
	if err := ensureSQLiteSchema(context.Background(), db, dialect); err != nil {
		return nil, err
	}
	return &sqliteStore{db: db, now: cfg.NowProvider, dialect: dialect}, nil
}

func dialectForBackend(backend Backend) sqlDialect {
	switch backend {
	case BackendPostgreSQL:
		return sqlDialectPostgreSQL
	case BackendMariaDB:
		return sqlDialectMariaDB
	default:
		return sqlDialectSQLite
	}
}

func ensureSQLiteSchema(ctx context.Context, db *sql.DB, dialect sqlDialect) error {
	stmts := schemaStatements(dialect)
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("ensure %s schema: %w", dialect, err)
		}
	}
	if dialect == sqlDialectSQLite {
		if err := ensureSQLiteColumns(ctx, db, "buckets", map[string]string{"region": "TEXT NOT NULL DEFAULT ''", "versioning_status": "TEXT NOT NULL DEFAULT 'Suspended'"}); err != nil {
			return err
		}
		if err := ensureSQLiteColumns(ctx, db, "object_versions", map[string]string{"checksum_sha1": "TEXT NOT NULL DEFAULT ''", "checksum_crc32": "TEXT NOT NULL DEFAULT ''", "checksum_crc32c": "TEXT NOT NULL DEFAULT ''"}); err != nil {
			return err
		}
		if err := ensureSQLiteColumns(ctx, db, "multipart_uploads", map[string]string{"sse_algorithm": "TEXT NOT NULL DEFAULT ''"}); err != nil {
			return err
		}
		if err := ensureSQLiteColumns(ctx, db, "multipart_parts", map[string]string{"checksum_sha1": "TEXT NOT NULL DEFAULT ''", "checksum_crc32": "TEXT NOT NULL DEFAULT ''", "checksum_crc32c": "TEXT NOT NULL DEFAULT ''"}); err != nil {
			return err
		}
		_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS bucket_notifications (bucket TEXT PRIMARY KEY, document TEXT NOT NULL, enabled INTEGER NOT NULL)`)
		if err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS bucket_replications (bucket TEXT PRIMARY KEY, document TEXT NOT NULL, enabled INTEGER NOT NULL)`)
		return err
	}
	return nil
}

func schemaStatements(dialect sqlDialect) []string {
	switch dialect {
	case sqlDialectPostgreSQL:
		return []string{
			`CREATE TABLE IF NOT EXISTS buckets (name TEXT PRIMARY KEY, created_at TEXT NOT NULL, region TEXT NOT NULL DEFAULT '', versioning_status TEXT NOT NULL DEFAULT 'Suspended')`,
			`CREATE TABLE IF NOT EXISTS object_versions (bucket TEXT NOT NULL, object_key TEXT NOT NULL, version_id TEXT NOT NULL, ordinal BIGSERIAL PRIMARY KEY, size BIGINT NOT NULL, etag TEXT NOT NULL, checksum_sha256 TEXT NOT NULL, checksum_sha1 TEXT NOT NULL DEFAULT '', checksum_crc32 TEXT NOT NULL DEFAULT '', checksum_crc32c TEXT NOT NULL DEFAULT '', storage_path TEXT NOT NULL DEFAULT '', metadata_json TEXT NOT NULL DEFAULT '{}', delete_marker INTEGER NOT NULL, created_at TEXT NOT NULL)`,
			`DROP INDEX IF EXISTS ux_object_versions_identity`,
			`CREATE UNIQUE INDEX IF NOT EXISTS ux_object_versions_identity_versioned ON object_versions(bucket, object_key, version_id) WHERE version_id <> 'null'`,
			`CREATE INDEX IF NOT EXISTS idx_object_versions_latest ON object_versions(bucket, object_key, ordinal DESC)`,
			`CREATE INDEX IF NOT EXISTS idx_object_versions_bucket_key ON object_versions(bucket, object_key)`,
			`CREATE TABLE IF NOT EXISTS multipart_uploads (upload_id TEXT PRIMARY KEY, bucket TEXT NOT NULL, object_key TEXT NOT NULL, sse_algorithm TEXT NOT NULL DEFAULT '', initiated_at TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS multipart_parts (upload_id TEXT NOT NULL, part_number INTEGER NOT NULL, etag TEXT NOT NULL, size BIGINT NOT NULL, checksum_sha256 TEXT NOT NULL, checksum_sha1 TEXT NOT NULL DEFAULT '', checksum_crc32 TEXT NOT NULL DEFAULT '', checksum_crc32c TEXT NOT NULL DEFAULT '', storage_path TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT '', PRIMARY KEY (upload_id, part_number))`,
			`CREATE INDEX IF NOT EXISTS idx_multipart_uploads_bucket_key ON multipart_uploads(bucket, object_key)`,
			`CREATE TABLE IF NOT EXISTS credential_records (access_key_id TEXT PRIMARY KEY, secret_hash TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL, rotated_at TEXT NOT NULL DEFAULT '')`,
			`CREATE TABLE IF NOT EXISTS bucket_websites (bucket TEXT PRIMARY KEY, index_document TEXT NOT NULL, error_document TEXT NOT NULL, redirect_all_host TEXT NOT NULL, redirect_all_protocol TEXT NOT NULL, routing_rules_json TEXT NOT NULL DEFAULT '[]', enabled INTEGER NOT NULL, public_read INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_cors (bucket TEXT PRIMARY KEY, allowed_origins TEXT NOT NULL, allowed_methods TEXT NOT NULL, allowed_headers TEXT NOT NULL, expose_headers TEXT NOT NULL, max_age_seconds INTEGER NOT NULL, enabled INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_policies (bucket TEXT PRIMARY KEY, document TEXT NOT NULL, enabled INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_public_access_blocks (bucket TEXT PRIMARY KEY, block_public_acls INTEGER NOT NULL, ignore_public_acls INTEGER NOT NULL, block_public_policy INTEGER NOT NULL, restrict_public_buckets INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_lifecycles (bucket TEXT PRIMARY KEY, document TEXT NOT NULL, enabled INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_notifications (bucket TEXT PRIMARY KEY, document TEXT NOT NULL, enabled INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_replications (bucket TEXT PRIMARY KEY, document TEXT NOT NULL, enabled INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS object_taggings (bucket TEXT NOT NULL, object_key TEXT NOT NULL, version_id TEXT NOT NULL, document TEXT NOT NULL, PRIMARY KEY (bucket, object_key, version_id))`,
		}
	case sqlDialectMariaDB:
		return []string{
			`CREATE TABLE IF NOT EXISTS buckets (name VARCHAR(255) PRIMARY KEY, created_at TEXT NOT NULL, region TEXT NOT NULL DEFAULT '', versioning_status TEXT NOT NULL DEFAULT 'Suspended')`,
			`CREATE TABLE IF NOT EXISTS object_versions (bucket VARCHAR(255) NOT NULL, object_key VARCHAR(1024) NOT NULL, version_id VARCHAR(255) NOT NULL, ordinal BIGINT AUTO_INCREMENT PRIMARY KEY, size BIGINT NOT NULL, etag TEXT NOT NULL, checksum_sha256 TEXT NOT NULL, checksum_sha1 TEXT NOT NULL DEFAULT '', checksum_crc32 TEXT NOT NULL DEFAULT '', checksum_crc32c TEXT NOT NULL DEFAULT '', storage_path TEXT NOT NULL DEFAULT '', metadata_json TEXT NOT NULL DEFAULT '{}', delete_marker INTEGER NOT NULL, created_at TEXT NOT NULL, INDEX idx_object_versions_latest (bucket, object_key, ordinal), INDEX idx_object_versions_bucket_key (bucket, object_key))`,
			`CREATE TABLE IF NOT EXISTS multipart_uploads (upload_id VARCHAR(255) PRIMARY KEY, bucket VARCHAR(255) NOT NULL, object_key VARCHAR(1024) NOT NULL, sse_algorithm TEXT NOT NULL DEFAULT '', initiated_at TEXT NOT NULL, INDEX idx_multipart_uploads_bucket_key (bucket, object_key))`,
			`CREATE TABLE IF NOT EXISTS multipart_parts (upload_id VARCHAR(255) NOT NULL, part_number INTEGER NOT NULL, etag TEXT NOT NULL, size BIGINT NOT NULL, checksum_sha256 TEXT NOT NULL, checksum_sha1 TEXT NOT NULL DEFAULT '', checksum_crc32 TEXT NOT NULL DEFAULT '', checksum_crc32c TEXT NOT NULL DEFAULT '', storage_path TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT '', PRIMARY KEY (upload_id, part_number))`,
			`CREATE TABLE IF NOT EXISTS credential_records (access_key_id VARCHAR(255) PRIMARY KEY, secret_hash TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL, rotated_at TEXT NOT NULL DEFAULT '')`,
			`CREATE TABLE IF NOT EXISTS bucket_websites (bucket VARCHAR(255) PRIMARY KEY, index_document TEXT NOT NULL, error_document TEXT NOT NULL, redirect_all_host TEXT NOT NULL, redirect_all_protocol TEXT NOT NULL, routing_rules_json TEXT NOT NULL DEFAULT '[]', enabled INTEGER NOT NULL, public_read INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_cors (bucket VARCHAR(255) PRIMARY KEY, allowed_origins TEXT NOT NULL, allowed_methods TEXT NOT NULL, allowed_headers TEXT NOT NULL, expose_headers TEXT NOT NULL, max_age_seconds INTEGER NOT NULL, enabled INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_policies (bucket VARCHAR(255) PRIMARY KEY, document TEXT NOT NULL, enabled INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_public_access_blocks (bucket VARCHAR(255) PRIMARY KEY, block_public_acls INTEGER NOT NULL, ignore_public_acls INTEGER NOT NULL, block_public_policy INTEGER NOT NULL, restrict_public_buckets INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_lifecycles (bucket VARCHAR(255) PRIMARY KEY, document TEXT NOT NULL, enabled INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_notifications (bucket VARCHAR(255) PRIMARY KEY, document TEXT NOT NULL, enabled INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_replications (bucket VARCHAR(255) PRIMARY KEY, document TEXT NOT NULL, enabled INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS object_taggings (bucket VARCHAR(255) NOT NULL, object_key VARCHAR(1024) NOT NULL, version_id VARCHAR(255) NOT NULL, document TEXT NOT NULL, PRIMARY KEY (bucket, object_key, version_id))`,
		}
	default:
		return []string{
			`CREATE TABLE IF NOT EXISTS buckets (name TEXT PRIMARY KEY, created_at TEXT NOT NULL, region TEXT NOT NULL DEFAULT '', versioning_status TEXT NOT NULL DEFAULT 'Suspended')`,
			`CREATE TABLE IF NOT EXISTS object_versions (bucket TEXT NOT NULL, object_key TEXT NOT NULL, version_id TEXT NOT NULL, ordinal INTEGER PRIMARY KEY AUTOINCREMENT, size INTEGER NOT NULL, etag TEXT NOT NULL, checksum_sha256 TEXT NOT NULL, checksum_sha1 TEXT NOT NULL DEFAULT '', checksum_crc32 TEXT NOT NULL DEFAULT '', checksum_crc32c TEXT NOT NULL DEFAULT '', storage_path TEXT NOT NULL DEFAULT '', metadata_json TEXT NOT NULL DEFAULT '{}', delete_marker INTEGER NOT NULL, created_at TEXT NOT NULL)`,
			`DROP INDEX IF EXISTS ux_object_versions_identity`,
			`CREATE UNIQUE INDEX IF NOT EXISTS ux_object_versions_identity_versioned ON object_versions(bucket, object_key, version_id) WHERE version_id <> 'null'`,
			`CREATE INDEX IF NOT EXISTS idx_object_versions_latest ON object_versions(bucket, object_key, ordinal DESC)`,
			`CREATE INDEX IF NOT EXISTS idx_object_versions_bucket_key ON object_versions(bucket, object_key)`,
			`CREATE TABLE IF NOT EXISTS multipart_uploads (upload_id TEXT PRIMARY KEY, bucket TEXT NOT NULL, object_key TEXT NOT NULL, sse_algorithm TEXT NOT NULL DEFAULT '', initiated_at TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS multipart_parts (upload_id TEXT NOT NULL, part_number INTEGER NOT NULL, etag TEXT NOT NULL, size INTEGER NOT NULL, checksum_sha256 TEXT NOT NULL, checksum_sha1 TEXT NOT NULL DEFAULT '', checksum_crc32 TEXT NOT NULL DEFAULT '', checksum_crc32c TEXT NOT NULL DEFAULT '', storage_path TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT '', PRIMARY KEY (upload_id, part_number))`,
			`CREATE INDEX IF NOT EXISTS idx_multipart_uploads_bucket_key ON multipart_uploads(bucket, object_key)`,
			`CREATE TABLE IF NOT EXISTS credential_records (access_key_id TEXT PRIMARY KEY, secret_hash TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL, rotated_at TEXT NOT NULL DEFAULT '')`,
			`CREATE TABLE IF NOT EXISTS bucket_websites (bucket TEXT PRIMARY KEY, index_document TEXT NOT NULL, error_document TEXT NOT NULL, redirect_all_host TEXT NOT NULL, redirect_all_protocol TEXT NOT NULL, routing_rules_json TEXT NOT NULL DEFAULT '[]', enabled INTEGER NOT NULL, public_read INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_cors (bucket TEXT PRIMARY KEY, allowed_origins TEXT NOT NULL, allowed_methods TEXT NOT NULL, allowed_headers TEXT NOT NULL, expose_headers TEXT NOT NULL, max_age_seconds INTEGER NOT NULL, enabled INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_policies (bucket TEXT PRIMARY KEY, document TEXT NOT NULL, enabled INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_public_access_blocks (bucket TEXT PRIMARY KEY, block_public_acls INTEGER NOT NULL, ignore_public_acls INTEGER NOT NULL, block_public_policy INTEGER NOT NULL, restrict_public_buckets INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_lifecycles (bucket TEXT PRIMARY KEY, document TEXT NOT NULL, enabled INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_notifications (bucket TEXT PRIMARY KEY, document TEXT NOT NULL, enabled INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bucket_replications (bucket TEXT PRIMARY KEY, document TEXT NOT NULL, enabled INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS object_taggings (bucket TEXT NOT NULL, object_key TEXT NOT NULL, version_id TEXT NOT NULL, document TEXT NOT NULL, PRIMARY KEY (bucket, object_key, version_id))`,
		}
	}
}

func ensureSQLiteColumns(ctx context.Context, db *sql.DB, table string, columns map[string]string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	existing := map[string]struct{}{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		existing[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for name, spec := range columns {
		if _, ok := existing[name]; ok {
			continue
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+name+" "+spec); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqliteStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, sqlQuery(s.dialect, query), args...)
}

func (s *sqliteStore) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, sqlQuery(s.dialect, query), args...)
}

func (s *sqliteStore) queryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, sqlQuery(s.dialect, query), args...)
}

func (t *sqliteTxStore) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, sqlQuery(t.dialect, query), args...)
}

func sqlQuery(dialect sqlDialect, query string) string {
	if dialect != sqlDialectPostgreSQL {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 1
	for _, r := range query {
		if r == '?' {
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			n++
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func upsertSQL(dialect sqlDialect, table string, columns []string, conflict []string, updates []string) string {
	placeholders := make([]string, len(columns))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	base := "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	if dialect == sqlDialectMariaDB {
		assignments := make([]string, 0, len(updates))
		for _, col := range updates {
			assignments = append(assignments, col+" = VALUES("+col+")")
		}
		return base + " ON DUPLICATE KEY UPDATE " + strings.Join(assignments, ", ")
	}
	assignments := make([]string, 0, len(updates))
	for _, col := range updates {
		assignments = append(assignments, col+" = excluded."+col)
	}
	return base + " ON CONFLICT(" + strings.Join(conflict, ", ") + ") DO UPDATE SET " + strings.Join(assignments, ", ")
}

func putObjectVersionSQL() string {
	return `INSERT INTO object_versions (bucket, object_key, version_id, size, etag, checksum_sha256, checksum_sha1, checksum_crc32, checksum_crc32c, storage_path, metadata_json, delete_marker, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

func selectObjectVersionSQL(prefix string) string {
	cols := []string{"bucket", "object_key", "version_id", "size", "etag", "checksum_sha256", "checksum_sha1", "checksum_crc32", "checksum_crc32c", "storage_path", "metadata_json", "delete_marker", "created_at"}
	for i, col := range cols {
		cols[i] = prefix + col
	}
	return strings.Join(cols, ", ")
}

func upsertMultipartPartSQL(dialect sqlDialect) string {
	return upsertSQL(dialect, "multipart_parts", []string{"upload_id", "part_number", "etag", "size", "checksum_sha256", "checksum_sha1", "checksum_crc32", "checksum_crc32c", "storage_path", "created_at"}, []string{"upload_id", "part_number"}, []string{"etag", "size", "checksum_sha256", "checksum_sha1", "checksum_crc32", "checksum_crc32c", "storage_path", "created_at"})
}

func upsertCredentialSQL(dialect sqlDialect) string {
	return upsertSQL(dialect, "credential_records", []string{"access_key_id", "secret_hash", "status", "created_at", "rotated_at"}, []string{"access_key_id"}, []string{"secret_hash", "status", "created_at", "rotated_at"})
}

func (s *sqliteStore) CreateBucket(ctx context.Context, bucket Bucket) error {
	_, err := s.exec(ctx, `INSERT INTO buckets (name, created_at, region, versioning_status) VALUES (?, ?, ?, ?)`, bucket.Name, formatTime(bucket.CreatedAt), bucket.Region, defaultVersioning(bucket.VersioningStatus))
	return mapSQLiteErr(err)
}

func (s *sqliteStore) DeleteBucket(ctx context.Context, bucket string) error {
	res, err := s.exec(ctx, `DELETE FROM buckets WHERE name = ?`, bucket)
	return requireAffected(res, err)
}

func (s *sqliteStore) UpdateBucketVersioning(ctx context.Context, bucket string, status string) error {
	res, err := s.exec(ctx, `UPDATE buckets SET versioning_status = ? WHERE name = ?`, status, bucket)
	return requireAffected(res, err)
}

func (s *sqliteStore) GetBucket(ctx context.Context, name string) (Bucket, error) {
	row := s.queryRow(ctx, `SELECT name, created_at, region, versioning_status FROM buckets WHERE name = ?`, name)
	return scanBucket(row)
}

func (s *sqliteStore) ListBuckets(ctx context.Context) ([]Bucket, error) {
	rows, err := s.query(ctx, `SELECT name, created_at, region, versioning_status FROM buckets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Bucket{}
	for rows.Next() {
		b, err := scanBucket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *sqliteStore) PutObjectVersion(ctx context.Context, version ObjectVersion) error {
	return sqlitePutObjectVersion(ctx, s.db, s.dialect, version)
}

func (s *sqliteStore) DeleteObject(ctx context.Context, bucket string, key string, versionID string, at time.Time) error {
	return s.PutObjectVersion(ctx, ObjectVersion{Bucket: bucket, Key: key, VersionID: versionID, DeleteMarker: true, CreatedAt: at})
}

func (s *sqliteStore) ListObjects(ctx context.Context, bucket string) ([]ObjectVersion, error) {
	if _, err := s.GetBucket(ctx, bucket); err != nil {
		return nil, err
	}
	rows, err := s.query(ctx, `SELECT `+selectObjectVersionSQL("ov.")+` FROM object_versions ov JOIN (SELECT bucket, object_key, MAX(ordinal) AS ordinal FROM object_versions WHERE bucket = ? GROUP BY bucket, object_key) latest ON latest.bucket = ov.bucket AND latest.object_key = ov.object_key AND latest.ordinal = ov.ordinal WHERE ov.delete_marker = 0 ORDER BY ov.object_key`, bucket)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanObjectRows(rows)
}

func (s *sqliteStore) ListObjectVersions(ctx context.Context, bucket string) ([]ObjectVersion, error) {
	if _, err := s.GetBucket(ctx, bucket); err != nil {
		return nil, err
	}
	rows, err := s.query(ctx, `SELECT `+selectObjectVersionSQL("")+` FROM object_versions WHERE bucket = ? ORDER BY object_key, ordinal DESC`, bucket)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanObjectRows(rows)
}

func (s *sqliteStore) ListObjectsV2(ctx context.Context, bucket string, opts ListObjectsV2Options) (ListObjectsV2Result, error) {
	if _, err := s.GetBucket(ctx, bucket); err != nil {
		return ListObjectsV2Result{}, err
	}
	maxKeys := opts.MaxKeys
	if maxKeys < 0 {
		maxKeys = 0
	}
	if maxKeys > 1000 {
		maxKeys = 1000
	}
	if maxKeys == 0 {
		return ListObjectsV2Result{}, nil
	}
	upper, hasUpper := stringPrefixUpperBound(opts.Prefix)
	query := `SELECT ` + selectObjectVersionSQL("ov.") + ` FROM object_versions ov JOIN (SELECT bucket, object_key, MAX(ordinal) AS ordinal FROM object_versions WHERE bucket = ? AND object_key >= ? AND object_key > ?`
	args := []any{bucket, opts.Prefix, opts.StartAfter}
	if hasUpper {
		query += ` AND object_key < ?`
		args = append(args, upper)
	}
	query += ` GROUP BY bucket, object_key) latest ON latest.bucket = ov.bucket AND latest.object_key = ov.object_key AND latest.ordinal = ov.ordinal WHERE ov.delete_marker = 0 ORDER BY ov.object_key`
	rows, err := s.query(ctx, query, args...)
	if err != nil {
		return ListObjectsV2Result{}, err
	}
	defer func() { _ = rows.Close() }()

	result := ListObjectsV2Result{Entries: make([]ListObjectsV2Entry, 0, maxKeys)}
	commonPrefixes := map[string]struct{}{}
	for rows.Next() {
		obj, err := scanObject(rows)
		if err != nil {
			return ListObjectsV2Result{}, err
		}
		if opts.Delimiter != "" && opts.StartAfter != "" && strings.HasSuffix(opts.StartAfter, opts.Delimiter) && strings.HasPrefix(obj.Key, opts.StartAfter) {
			continue
		}
		entry := ListObjectsV2Entry{Value: obj.Key}
		if opts.Delimiter != "" {
			tail := strings.TrimPrefix(obj.Key, opts.Prefix)
			if idx := strings.Index(tail, opts.Delimiter); idx >= 0 {
				cp := opts.Prefix + tail[:idx+len(opts.Delimiter)]
				if _, exists := commonPrefixes[cp]; exists {
					continue
				}
				commonPrefixes[cp] = struct{}{}
				entry.Value = cp
			} else {
				copyObj := obj
				entry.Object = &copyObj
			}
		} else {
			copyObj := obj
			entry.Object = &copyObj
		}
		if len(result.Entries) == maxKeys {
			result.IsTruncated = true
			result.NextAfter = result.Entries[len(result.Entries)-1].Value
			return result, nil
		}
		result.Entries = append(result.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return ListObjectsV2Result{}, err
	}
	return result, nil
}

func stringPrefixUpperBound(prefix string) (string, bool) {
	if prefix == "" {
		return "", false
	}
	b := []byte(prefix)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] != 0xff {
			b[i]++
			return string(b[:i+1]), true
		}
	}
	return "", false
}

func (s *sqliteStore) GetLatestObjectVersion(ctx context.Context, bucket string, key string) (ObjectVersion, error) {
	row := s.queryRow(ctx, `SELECT `+selectObjectVersionSQL("")+` FROM object_versions WHERE bucket = ? AND object_key = ? ORDER BY ordinal DESC LIMIT 1`, bucket, key)
	return scanObject(row)
}

func (s *sqliteStore) GetObjectVersion(ctx context.Context, bucket string, key string, versionID string) (ObjectVersion, error) {
	if versionID == "" {
		return s.GetLatestObjectVersion(ctx, bucket, key)
	}
	row := s.queryRow(ctx, `SELECT `+selectObjectVersionSQL("")+` FROM object_versions WHERE bucket = ? AND object_key = ? AND version_id = ?`, bucket, key, versionID)
	return scanObject(row)
}

func (s *sqliteStore) UpdateObjectMetadata(ctx context.Context, bucket string, key string, versionID string, metadata map[string]string) error {
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	res, err := s.exec(ctx, `UPDATE object_versions SET metadata_json = ? WHERE bucket = ? AND object_key = ? AND version_id = ?`, string(metadataJSON), bucket, key, versionID)
	return requireAffected(res, err)
}

func (s *sqliteStore) DeleteObjectVersion(ctx context.Context, bucket string, key string, versionID string) (ObjectVersion, error) {
	version, err := s.GetObjectVersion(ctx, bucket, key, versionID)
	if err != nil {
		return ObjectVersion{}, err
	}
	res, err := s.exec(ctx, `DELETE FROM object_versions WHERE bucket = ? AND object_key = ? AND version_id = ?`, bucket, key, versionID)
	if err := requireAffected(res, err); err != nil {
		return ObjectVersion{}, err
	}
	return version, nil
}

func (s *sqliteStore) DeleteAllObjectVersions(ctx context.Context, bucket string, key string) ([]ObjectVersion, error) {
	rows, err := s.query(ctx, `SELECT `+selectObjectVersionSQL("")+` FROM object_versions WHERE bucket = ? AND object_key = ? ORDER BY ordinal`, bucket, key)
	if err != nil {
		return nil, err
	}
	versions, err := scanObjectRows(rows)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, ErrNotFound
	}
	_, err = s.exec(ctx, `DELETE FROM object_versions WHERE bucket = ? AND object_key = ?`, bucket, key)
	return versions, err
}

func (s *sqliteStore) CreateMultipartUpload(ctx context.Context, upload MultipartUpload) error {
	_, err := s.exec(ctx, `INSERT INTO multipart_uploads (upload_id, bucket, object_key, sse_algorithm, initiated_at) VALUES (?, ?, ?, ?, ?)`, upload.UploadID, upload.Bucket, upload.Key, upload.SSEAlgorithm, formatTime(upload.InitiatedAt))
	return mapSQLiteErr(err)
}

func (s *sqliteStore) DeleteMultipartUpload(ctx context.Context, uploadID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, sqlQuery(s.dialect, `DELETE FROM multipart_uploads WHERE upload_id = ?`), uploadID)
	if err := requireAffected(res, err); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, sqlQuery(s.dialect, `DELETE FROM multipart_parts WHERE upload_id = ?`), uploadID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) UpsertMultipartPart(ctx context.Context, part MultipartPart) error {
	_, _, err := s.GetMultipartUpload(ctx, part.UploadID)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, upsertMultipartPartSQL(s.dialect), part.UploadID, part.PartNumber, part.ETag, part.Size, part.ChecksumSHA256, part.ChecksumSHA1, part.ChecksumCRC32, part.ChecksumCRC32C, part.StoragePath, formatTime(part.CreatedAt))
	return err
}

func (s *sqliteStore) GetMultipartUpload(ctx context.Context, uploadID string) (MultipartUpload, []MultipartPart, error) {
	u, err := sqliteGetMultipartUpload(ctx, s.db, s.dialect, uploadID)
	if err != nil {
		return MultipartUpload{}, nil, err
	}
	parts, err := sqliteListMultipartParts(ctx, s.db, s.dialect, uploadID)
	return u, parts, err
}

func (s *sqliteStore) ListMultipartUploads(ctx context.Context, bucket string, prefix string) ([]MultipartUpload, error) {
	if _, err := s.GetBucket(ctx, bucket); err != nil {
		return nil, err
	}
	like := prefix + "%"
	rows, err := s.query(ctx, `SELECT upload_id, bucket, object_key, initiated_at FROM multipart_uploads WHERE bucket = ? AND (? = '' OR object_key LIKE ?) ORDER BY object_key, upload_id`, bucket, prefix, like)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []MultipartUpload{}
	for rows.Next() {
		u, err := scanMultipartUpload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *sqliteStore) UpsertCredentialRecord(ctx context.Context, record CredentialRecord) error {
	_, err := s.exec(ctx, upsertCredentialSQL(s.dialect), record.AccessKeyID, record.SecretHash, record.Status, formatTime(record.CreatedAt), formatOptionalTime(record.RotatedAt))
	return err
}

func (s *sqliteStore) GetCredentialRecord(ctx context.Context, accessKeyID string) (CredentialRecord, error) {
	row := s.queryRow(ctx, `SELECT access_key_id, secret_hash, status, created_at, rotated_at FROM credential_records WHERE access_key_id = ?`, accessKeyID)
	var rec CredentialRecord
	var created, rotated string
	if err := row.Scan(&rec.AccessKeyID, &rec.SecretHash, &rec.Status, &created, &rotated); err != nil {
		return CredentialRecord{}, mapSQLNotFound(err)
	}
	rec.CreatedAt = parseTime(created)
	rec.RotatedAt = parseTime(rotated)
	return rec, nil
}

func (s *sqliteStore) RunInTx(ctx context.Context, fn func(tx TxStore) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(&sqliteTxStore{tx: tx, dialect: s.dialect}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqliteStore) ValidateConsistency(ctx context.Context) ([]ConsistencyIssue, error) {
	rows, err := s.query(ctx, `SELECT object_versions.bucket, object_versions.object_key FROM object_versions LEFT JOIN buckets ON buckets.name = object_versions.bucket WHERE buckets.name IS NULL LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	issues := []ConsistencyIssue{}
	for rows.Next() {
		var bucket, key string
		if err := rows.Scan(&bucket, &key); err != nil {
			return nil, err
		}
		issues = append(issues, ConsistencyIssue{Code: "missing_bucket", Message: bucket + "/" + key + " references a missing bucket"})
	}
	return issues, rows.Err()
}

func (s *sqliteStore) RepairConsistency(context.Context) (int, error) { return 0, nil }

func (s *sqliteStore) PutBucketWebsite(ctx context.Context, cfg BucketWebsiteConfig) error {
	if _, err := s.GetBucket(ctx, cfg.Bucket); err != nil {
		return err
	}
	rules, err := json.Marshal(cfg.RoutingRules)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, upsertSQL(s.dialect, "bucket_websites", []string{"bucket", "index_document", "error_document", "redirect_all_host", "redirect_all_protocol", "routing_rules_json", "enabled", "public_read"}, []string{"bucket"}, []string{"index_document", "error_document", "redirect_all_host", "redirect_all_protocol", "routing_rules_json", "enabled", "public_read"}), cfg.Bucket, cfg.IndexDocument, cfg.ErrorDocument, cfg.RedirectAllHost, cfg.RedirectAllProtocol, string(rules), boolInt(cfg.Enabled), boolInt(cfg.PublicRead))
	return err
}

func (s *sqliteStore) GetBucketWebsite(ctx context.Context, bucket string) (BucketWebsiteConfig, error) {
	row := s.queryRow(ctx, `SELECT bucket, index_document, error_document, redirect_all_host, redirect_all_protocol, routing_rules_json, enabled, public_read FROM bucket_websites WHERE bucket = ?`, bucket)
	var cfg BucketWebsiteConfig
	var rules string
	var enabled, publicRead int
	if err := row.Scan(&cfg.Bucket, &cfg.IndexDocument, &cfg.ErrorDocument, &cfg.RedirectAllHost, &cfg.RedirectAllProtocol, &rules, &enabled, &publicRead); err != nil {
		return BucketWebsiteConfig{}, mapSQLNotFound(err)
	}
	_ = json.Unmarshal([]byte(rules), &cfg.RoutingRules)
	cfg.Enabled = enabled != 0
	cfg.PublicRead = publicRead != 0
	return cfg, nil
}

func (s *sqliteStore) DeleteBucketWebsite(ctx context.Context, bucket string) error {
	res, err := s.exec(ctx, `DELETE FROM bucket_websites WHERE bucket = ?`, bucket)
	return requireAffected(res, err)
}

func (s *sqliteStore) PutBucketCORS(ctx context.Context, cfg BucketCORSConfig) error {
	if _, err := s.GetBucket(ctx, cfg.Bucket); err != nil {
		return err
	}
	_, err := s.exec(ctx, upsertSQL(s.dialect, "bucket_cors", []string{"bucket", "allowed_origins", "allowed_methods", "allowed_headers", "expose_headers", "max_age_seconds", "enabled"}, []string{"bucket"}, []string{"allowed_origins", "allowed_methods", "allowed_headers", "expose_headers", "max_age_seconds", "enabled"}), cfg.Bucket, cfg.AllowedOrigins, cfg.AllowedMethods, cfg.AllowedHeaders, cfg.ExposeHeaders, cfg.MaxAgeSeconds, boolInt(cfg.Enabled))
	return err
}

func (s *sqliteStore) GetBucketCORS(ctx context.Context, bucket string) (BucketCORSConfig, error) {
	row := s.queryRow(ctx, `SELECT bucket, allowed_origins, allowed_methods, allowed_headers, expose_headers, max_age_seconds, enabled FROM bucket_cors WHERE bucket = ?`, bucket)
	var cfg BucketCORSConfig
	var enabled int
	if err := row.Scan(&cfg.Bucket, &cfg.AllowedOrigins, &cfg.AllowedMethods, &cfg.AllowedHeaders, &cfg.ExposeHeaders, &cfg.MaxAgeSeconds, &enabled); err != nil {
		return BucketCORSConfig{}, mapSQLNotFound(err)
	}
	cfg.Enabled = enabled != 0
	return cfg, nil
}

func (s *sqliteStore) DeleteBucketCORS(ctx context.Context, bucket string) error {
	res, err := s.exec(ctx, `DELETE FROM bucket_cors WHERE bucket = ?`, bucket)
	return requireAffected(res, err)
}

func (s *sqliteStore) PutBucketPolicy(ctx context.Context, cfg BucketPolicy) error {
	if _, err := s.GetBucket(ctx, cfg.Bucket); err != nil {
		return err
	}
	_, err := s.exec(ctx, upsertSQL(s.dialect, "bucket_policies", []string{"bucket", "document", "enabled"}, []string{"bucket"}, []string{"document", "enabled"}), cfg.Bucket, cfg.Document, boolInt(cfg.Enabled))
	return err
}

func (s *sqliteStore) GetBucketPolicy(ctx context.Context, bucket string) (BucketPolicy, error) {
	row := s.queryRow(ctx, `SELECT bucket, document, enabled FROM bucket_policies WHERE bucket = ?`, bucket)
	var cfg BucketPolicy
	var enabled int
	if err := row.Scan(&cfg.Bucket, &cfg.Document, &enabled); err != nil {
		return BucketPolicy{}, mapSQLNotFound(err)
	}
	cfg.Enabled = enabled != 0
	return cfg, nil
}

func (s *sqliteStore) DeleteBucketPolicy(ctx context.Context, bucket string) error {
	res, err := s.exec(ctx, `DELETE FROM bucket_policies WHERE bucket = ?`, bucket)
	return requireAffected(res, err)
}

func (s *sqliteStore) PutBucketPublicAccessBlock(ctx context.Context, cfg BucketPublicAccessBlock) error {
	if _, err := s.GetBucket(ctx, cfg.Bucket); err != nil {
		return err
	}
	_, err := s.exec(ctx, upsertSQL(s.dialect, "bucket_public_access_blocks", []string{"bucket", "block_public_acls", "ignore_public_acls", "block_public_policy", "restrict_public_buckets"}, []string{"bucket"}, []string{"block_public_acls", "ignore_public_acls", "block_public_policy", "restrict_public_buckets"}), cfg.Bucket, boolInt(cfg.BlockPublicACLs), boolInt(cfg.IgnorePublicACLs), boolInt(cfg.BlockPublicPolicy), boolInt(cfg.RestrictPublicBuckets))
	return err
}

func (s *sqliteStore) GetBucketPublicAccessBlock(ctx context.Context, bucket string) (BucketPublicAccessBlock, error) {
	row := s.queryRow(ctx, `SELECT bucket, block_public_acls, ignore_public_acls, block_public_policy, restrict_public_buckets FROM bucket_public_access_blocks WHERE bucket = ?`, bucket)
	var cfg BucketPublicAccessBlock
	var a, b, c, d int
	if err := row.Scan(&cfg.Bucket, &a, &b, &c, &d); err != nil {
		return BucketPublicAccessBlock{}, mapSQLNotFound(err)
	}
	cfg.BlockPublicACLs = a != 0
	cfg.IgnorePublicACLs = b != 0
	cfg.BlockPublicPolicy = c != 0
	cfg.RestrictPublicBuckets = d != 0
	return cfg, nil
}

func (s *sqliteStore) DeleteBucketPublicAccessBlock(ctx context.Context, bucket string) error {
	res, err := s.exec(ctx, `DELETE FROM bucket_public_access_blocks WHERE bucket = ?`, bucket)
	return requireAffected(res, err)
}

func (s *sqliteStore) PutBucketLifecycle(ctx context.Context, cfg BucketLifecycle) error {
	if _, err := s.GetBucket(ctx, cfg.Bucket); err != nil {
		return err
	}
	_, err := s.exec(ctx, upsertSQL(s.dialect, "bucket_lifecycles", []string{"bucket", "document", "enabled"}, []string{"bucket"}, []string{"document", "enabled"}), cfg.Bucket, cfg.Document, boolInt(cfg.Enabled))
	return err
}

func (s *sqliteStore) GetBucketLifecycle(ctx context.Context, bucket string) (BucketLifecycle, error) {
	row := s.queryRow(ctx, `SELECT bucket, document, enabled FROM bucket_lifecycles WHERE bucket = ?`, bucket)
	var cfg BucketLifecycle
	var enabled int
	if err := row.Scan(&cfg.Bucket, &cfg.Document, &enabled); err != nil {
		return BucketLifecycle{}, mapSQLNotFound(err)
	}
	cfg.Enabled = enabled != 0
	return cfg, nil
}

func (s *sqliteStore) DeleteBucketLifecycle(ctx context.Context, bucket string) error {
	res, err := s.exec(ctx, `DELETE FROM bucket_lifecycles WHERE bucket = ?`, bucket)
	return requireAffected(res, err)
}

func (s *sqliteStore) PutBucketNotification(ctx context.Context, cfg BucketNotification) error {
	if _, err := s.GetBucket(ctx, cfg.Bucket); err != nil {
		return err
	}
	_, err := s.exec(ctx, upsertSQL(s.dialect, "bucket_notifications", []string{"bucket", "document", "enabled"}, []string{"bucket"}, []string{"document", "enabled"}), cfg.Bucket, cfg.Document, boolInt(cfg.Enabled))
	return err
}

func (s *sqliteStore) GetBucketNotification(ctx context.Context, bucket string) (BucketNotification, error) {
	row := s.queryRow(ctx, `SELECT bucket, document, enabled FROM bucket_notifications WHERE bucket = ?`, bucket)
	var cfg BucketNotification
	var enabled int
	if err := row.Scan(&cfg.Bucket, &cfg.Document, &enabled); err != nil {
		return BucketNotification{}, mapSQLNotFound(err)
	}
	cfg.Enabled = enabled != 0
	return cfg, nil
}

func (s *sqliteStore) DeleteBucketNotification(ctx context.Context, bucket string) error {
	res, err := s.exec(ctx, `DELETE FROM bucket_notifications WHERE bucket = ?`, bucket)
	return requireAffected(res, err)
}

func (s *sqliteStore) PutBucketReplication(ctx context.Context, cfg BucketReplication) error {
	if _, err := s.GetBucket(ctx, cfg.Bucket); err != nil {
		return err
	}
	_, err := s.exec(ctx, upsertSQL(s.dialect, "bucket_replications", []string{"bucket", "document", "enabled"}, []string{"bucket"}, []string{"document", "enabled"}), cfg.Bucket, cfg.Document, boolInt(cfg.Enabled))
	return err
}

func (s *sqliteStore) GetBucketReplication(ctx context.Context, bucket string) (BucketReplication, error) {
	row := s.queryRow(ctx, `SELECT bucket, document, enabled FROM bucket_replications WHERE bucket = ?`, bucket)
	var cfg BucketReplication
	var enabled int
	if err := row.Scan(&cfg.Bucket, &cfg.Document, &enabled); err != nil {
		return BucketReplication{}, mapSQLNotFound(err)
	}
	cfg.Enabled = enabled != 0
	return cfg, nil
}

func (s *sqliteStore) DeleteBucketReplication(ctx context.Context, bucket string) error {
	res, err := s.exec(ctx, `DELETE FROM bucket_replications WHERE bucket = ?`, bucket)
	return requireAffected(res, err)
}

func (s *sqliteStore) PutObjectTagging(ctx context.Context, cfg ObjectTagging) error {
	_, err := s.exec(ctx, upsertSQL(s.dialect, "object_taggings", []string{"bucket", "object_key", "version_id", "document"}, []string{"bucket", "object_key", "version_id"}, []string{"document"}), cfg.Bucket, cfg.Key, cfg.VersionID, cfg.Document)
	return err
}

func (s *sqliteStore) GetObjectTagging(ctx context.Context, bucket string, key string, versionID string) (ObjectTagging, error) {
	row := s.queryRow(ctx, `SELECT bucket, object_key, version_id, document FROM object_taggings WHERE bucket = ? AND object_key = ? AND version_id = ?`, bucket, key, versionID)
	var cfg ObjectTagging
	if err := row.Scan(&cfg.Bucket, &cfg.Key, &cfg.VersionID, &cfg.Document); err != nil {
		return ObjectTagging{}, mapSQLNotFound(err)
	}
	return cfg, nil
}

func (s *sqliteStore) DeleteObjectTagging(ctx context.Context, bucket string, key string, versionID string) error {
	res, err := s.exec(ctx, `DELETE FROM object_taggings WHERE bucket = ? AND object_key = ? AND version_id = ?`, bucket, key, versionID)
	return requireAffected(res, err)
}

func (t *sqliteTxStore) CreateBucket(ctx context.Context, bucket Bucket) error {
	_, err := t.exec(ctx, `INSERT INTO buckets (name, created_at, region, versioning_status) VALUES (?, ?, ?, ?)`, bucket.Name, formatTime(bucket.CreatedAt), bucket.Region, defaultVersioning(bucket.VersioningStatus))
	return mapSQLiteErr(err)
}

func (t *sqliteTxStore) DeleteBucket(ctx context.Context, bucket string) error {
	res, err := t.exec(ctx, `DELETE FROM buckets WHERE name = ?`, bucket)
	return requireAffected(res, err)
}

func (t *sqliteTxStore) UpdateBucketVersioning(ctx context.Context, bucket string, status string) error {
	res, err := t.exec(ctx, `UPDATE buckets SET versioning_status = ? WHERE name = ?`, status, bucket)
	return requireAffected(res, err)
}

func (t *sqliteTxStore) PutObjectVersion(ctx context.Context, version ObjectVersion) error {
	return sqlitePutObjectVersion(ctx, t.tx, t.dialect, version)
}

func (t *sqliteTxStore) DeleteObject(ctx context.Context, bucket string, key string, versionID string, at time.Time) error {
	return t.PutObjectVersion(ctx, ObjectVersion{Bucket: bucket, Key: key, VersionID: versionID, DeleteMarker: true, CreatedAt: at})
}

func (t *sqliteTxStore) CreateMultipartUpload(ctx context.Context, upload MultipartUpload) error {
	_, err := t.exec(ctx, `INSERT INTO multipart_uploads (upload_id, bucket, object_key, sse_algorithm, initiated_at) VALUES (?, ?, ?, ?, ?)`, upload.UploadID, upload.Bucket, upload.Key, upload.SSEAlgorithm, formatTime(upload.InitiatedAt))
	return mapSQLiteErr(err)
}

func (t *sqliteTxStore) DeleteMultipartUpload(ctx context.Context, uploadID string) error {
	res, err := t.exec(ctx, `DELETE FROM multipart_uploads WHERE upload_id = ?`, uploadID)
	if err := requireAffected(res, err); err != nil {
		return err
	}
	_, err = t.exec(ctx, `DELETE FROM multipart_parts WHERE upload_id = ?`, uploadID)
	return err
}

func (t *sqliteTxStore) UpsertMultipartPart(ctx context.Context, part MultipartPart) error {
	_, err := t.exec(ctx, upsertMultipartPartSQL(t.dialect), part.UploadID, part.PartNumber, part.ETag, part.Size, part.ChecksumSHA256, part.ChecksumSHA1, part.ChecksumCRC32, part.ChecksumCRC32C, part.StoragePath, formatTime(part.CreatedAt))
	return err
}

func (t *sqliteTxStore) UpsertCredentialRecord(ctx context.Context, record CredentialRecord) error {
	_, err := t.exec(ctx, upsertCredentialSQL(t.dialect), record.AccessKeyID, record.SecretHash, record.Status, formatTime(record.CreatedAt), formatOptionalTime(record.RotatedAt))
	return err
}

type rowScanner interface{ Scan(dest ...any) error }
type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func sqlitePutObjectVersion(ctx context.Context, exec sqlExecer, dialect sqlDialect, version ObjectVersion) error {
	metadataJSON, err := json.Marshal(version.Metadata)
	if err != nil {
		return err
	}
	if version.VersionID == "null" {
		if _, err := exec.ExecContext(ctx, sqlQuery(dialect, `DELETE FROM object_versions WHERE bucket = ? AND object_key = ? AND version_id = ?`), version.Bucket, version.Key, version.VersionID); err != nil {
			return mapSQLiteErr(err)
		}
	}
	_, err = exec.ExecContext(ctx, sqlQuery(dialect, putObjectVersionSQL()), version.Bucket, version.Key, version.VersionID, version.Size, version.ETag, version.ChecksumSHA256, version.ChecksumSHA1, version.ChecksumCRC32, version.ChecksumCRC32C, version.StoragePath, string(metadataJSON), boolInt(version.DeleteMarker), formatTime(version.CreatedAt))
	return mapSQLiteErr(err)
}

func sqliteGetMultipartUpload(ctx context.Context, db *sql.DB, dialect sqlDialect, uploadID string) (MultipartUpload, error) {
	row := db.QueryRowContext(ctx, sqlQuery(dialect, `SELECT upload_id, bucket, object_key, sse_algorithm, initiated_at FROM multipart_uploads WHERE upload_id = ?`), uploadID)
	return scanMultipartUpload(row)
}

func sqliteListMultipartParts(ctx context.Context, db *sql.DB, dialect sqlDialect, uploadID string) ([]MultipartPart, error) {
	rows, err := db.QueryContext(ctx, sqlQuery(dialect, `SELECT upload_id, part_number, etag, size, checksum_sha256, checksum_sha1, checksum_crc32, checksum_crc32c, storage_path, created_at FROM multipart_parts WHERE upload_id = ? ORDER BY part_number`), uploadID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []MultipartPart{}
	for rows.Next() {
		p, err := scanMultipartPart(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanBucket(row rowScanner) (Bucket, error) {
	var b Bucket
	var created string
	if err := row.Scan(&b.Name, &created, &b.Region, &b.VersioningStatus); err != nil {
		return Bucket{}, mapSQLNotFound(err)
	}
	b.CreatedAt = parseTime(created)
	return b, nil
}

func scanObject(row rowScanner) (ObjectVersion, error) {
	var obj ObjectVersion
	var metadataJSON, created string
	var deleteMarker int
	if err := row.Scan(&obj.Bucket, &obj.Key, &obj.VersionID, &obj.Size, &obj.ETag, &obj.ChecksumSHA256, &obj.ChecksumSHA1, &obj.ChecksumCRC32, &obj.ChecksumCRC32C, &obj.StoragePath, &metadataJSON, &deleteMarker, &created); err != nil {
		return ObjectVersion{}, mapSQLNotFound(err)
	}
	if metadataJSON != "" {
		_ = json.Unmarshal([]byte(metadataJSON), &obj.Metadata)
	}
	if obj.Metadata == nil {
		obj.Metadata = map[string]string{}
	}
	obj.DeleteMarker = deleteMarker != 0
	obj.CreatedAt = parseTime(created)
	return obj, nil
}

func scanObjectRows(rows *sql.Rows) ([]ObjectVersion, error) {
	defer func() { _ = rows.Close() }()
	out := []ObjectVersion{}
	for rows.Next() {
		obj, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, obj)
	}
	return out, rows.Err()
}

func scanMultipartUpload(row rowScanner) (MultipartUpload, error) {
	var u MultipartUpload
	var initiated string
	if err := row.Scan(&u.UploadID, &u.Bucket, &u.Key, &u.SSEAlgorithm, &initiated); err != nil {
		return MultipartUpload{}, mapSQLNotFound(err)
	}
	u.InitiatedAt = parseTime(initiated)
	return u, nil
}

func scanMultipartPart(row rowScanner) (MultipartPart, error) {
	var p MultipartPart
	var created string
	if err := row.Scan(&p.UploadID, &p.PartNumber, &p.ETag, &p.Size, &p.ChecksumSHA256, &p.ChecksumSHA1, &p.ChecksumCRC32, &p.ChecksumCRC32C, &p.StoragePath, &created); err != nil {
		return MultipartPart{}, mapSQLNotFound(err)
	}
	p.CreatedAt = parseTime(created)
	return p, nil
}

func mapSQLNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func mapSQLiteErr(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "constraint") {
		return ErrConflict
	}
	return err
}

func requireAffected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return time.Time{}.UTC().Format(time.RFC3339Nano)
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return formatTime(t)
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func defaultVersioning(status string) string {
	if status == "" {
		return "Suspended"
	}
	return status
}
