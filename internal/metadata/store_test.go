package metadata

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCrossBackendCompatibilityCoreCRUD(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)

	for _, backend := range []Backend{BackendSQLite, BackendLibSQL, BackendPostgreSQL, BackendMariaDB, BackendValkey} {
		backend := backend
		t.Run(string(backend), func(t *testing.T) {
			t.Parallel()

			store, err := NewStore(Config{
				Backend:     backend,
				DSN:         "local://metadata",
				ValkeyAddr:  "127.0.0.1:6379",
				NowProvider: func() time.Time { return now },
			})
			if err != nil {
				t.Fatalf("new store failed: %v", err)
			}

			if err := store.CreateBucket(ctx, Bucket{Name: "photos", CreatedAt: now}); err != nil {
				t.Fatalf("create bucket failed: %v", err)
			}

			version := ObjectVersion{
				Bucket:         "photos",
				Key:            "a.jpg",
				VersionID:      "v1",
				Size:           100,
				ETag:           "etag-v1",
				ChecksumSHA256: "sha-v1",
				CreatedAt:      now,
			}
			if err := store.PutObjectVersion(ctx, version); err != nil {
				t.Fatalf("put object version failed: %v", err)
			}

			got, err := store.GetLatestObjectVersion(ctx, "photos", "a.jpg")
			if err != nil {
				t.Fatalf("get latest object version failed: %v", err)
			}
			if got.VersionID != "v1" {
				t.Fatalf("expected version v1, got %q", got.VersionID)
			}

			if err := store.DeleteObject(ctx, "photos", "a.jpg", "v2", now.Add(time.Second)); err != nil {
				t.Fatalf("delete object failed: %v", err)
			}

			got, err = store.GetLatestObjectVersion(ctx, "photos", "a.jpg")
			if err != nil {
				t.Fatalf("get latest object version after delete failed: %v", err)
			}
			if !got.DeleteMarker {
				t.Fatal("expected delete marker as latest object version")
			}
		})
	}
}

func TestTransactionalCommitAndRollback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)

	store, err := NewStore(Config{
		Backend:     BackendSQLite,
		DSN:         "file:metadata.db",
		NowProvider: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	if err := store.RunInTx(ctx, func(tx TxStore) error {
		if err := tx.CreateBucket(ctx, Bucket{Name: "docs", CreatedAt: now}); err != nil {
			return err
		}
		return tx.PutObjectVersion(ctx, ObjectVersion{
			Bucket:         "docs",
			Key:            "readme.txt",
			VersionID:      "v1",
			Size:           10,
			ETag:           "etag-v1",
			ChecksumSHA256: "sha-v1",
			CreatedAt:      now,
		})
	}); err != nil {
		t.Fatalf("commit tx failed: %v", err)
	}

	if _, err := store.GetLatestObjectVersion(ctx, "docs", "readme.txt"); err != nil {
		t.Fatalf("expected committed version, got error: %v", err)
	}

	errRollback := errors.New("force rollback")
	err = store.RunInTx(ctx, func(tx TxStore) error {
		if err := tx.PutObjectVersion(ctx, ObjectVersion{
			Bucket:         "docs",
			Key:            "readme.txt",
			VersionID:      "v2",
			Size:           11,
			ETag:           "etag-v2",
			ChecksumSHA256: "sha-v2",
			CreatedAt:      now.Add(time.Second),
		}); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("expected rollback error %v, got %v", errRollback, err)
	}

	latest, err := store.GetLatestObjectVersion(ctx, "docs", "readme.txt")
	if err != nil {
		t.Fatalf("expected latest version after rollback, got %v", err)
	}
	if latest.VersionID != "v1" {
		t.Fatalf("expected rolled back version to remain v1, got %q", latest.VersionID)
	}
}

func TestMultipartAndCredentialModels(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)

	store, err := NewStore(Config{
		Backend:     BackendMariaDB,
		DSN:         "mariadb://user:pass@tcp(localhost:3306)/s000",
		NowProvider: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	if err := store.CreateBucket(ctx, Bucket{Name: "backup", CreatedAt: now}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}

	if err := store.CreateMultipartUpload(ctx, MultipartUpload{
		UploadID:    "u1",
		Bucket:      "backup",
		Key:         "db.dump",
		InitiatedAt: now,
	}); err != nil {
		t.Fatalf("create multipart upload failed: %v", err)
	}

	if err := store.UpsertMultipartPart(ctx, MultipartPart{
		UploadID:       "u1",
		PartNumber:     1,
		ETag:           "part-etag",
		Size:           4096,
		ChecksumSHA256: "part-sha",
	}); err != nil {
		t.Fatalf("upsert multipart part failed: %v", err)
	}

	mp, parts, err := store.GetMultipartUpload(ctx, "u1")
	if err != nil {
		t.Fatalf("get multipart upload failed: %v", err)
	}
	if mp.UploadID != "u1" || len(parts) != 1 {
		t.Fatalf("unexpected multipart state: upload=%q parts=%d", mp.UploadID, len(parts))
	}

	if err := store.UpsertCredentialRecord(ctx, CredentialRecord{
		AccessKeyID: "AKIA_TEST",
		SecretHash:  "hash-1",
		Status:      "active",
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("upsert credential failed: %v", err)
	}

	cred, err := store.GetCredentialRecord(ctx, "AKIA_TEST")
	if err != nil {
		t.Fatalf("get credential failed: %v", err)
	}
	if cred.AccessKeyID != "AKIA_TEST" {
		t.Fatalf("expected credential AKIA_TEST, got %q", cred.AccessKeyID)
	}
}

func TestBucketWebsiteConfigCRUD(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)

	store, err := NewStore(Config{Backend: BackendSQLite, DSN: "file:metadata.db", NowProvider: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}
	if err := store.CreateBucket(ctx, Bucket{Name: "site", CreatedAt: now}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}

	cfg := BucketWebsiteConfig{Bucket: "site", IndexDocument: "index.html", ErrorDocument: "error.html", Enabled: true, PublicRead: true}
	if err := store.PutBucketWebsite(ctx, cfg); err != nil {
		t.Fatalf("put bucket website failed: %v", err)
	}

	got, err := store.GetBucketWebsite(ctx, "site")
	if err != nil {
		t.Fatalf("get bucket website failed: %v", err)
	}
	if got.IndexDocument != "index.html" || got.ErrorDocument != "error.html" {
		t.Fatalf("unexpected website config: %+v", got)
	}

	if err := store.DeleteBucketWebsite(ctx, "site"); err != nil {
		t.Fatalf("delete bucket website failed: %v", err)
	}
	if _, err := store.GetBucketWebsite(ctx, "site"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected website config not found after delete, got %v", err)
	}
}

func TestBucketPolicyAndSettingsCRUD(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)

	store, err := NewStore(Config{Backend: BackendSQLite, DSN: "file:metadata.db", NowProvider: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}
	if err := store.CreateBucket(ctx, Bucket{Name: "ops", CreatedAt: now}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}

	cors := BucketCORSConfig{Bucket: "ops", AllowedOrigins: "https://example.com", AllowedMethods: "GET,PUT", AllowedHeaders: "Authorization", ExposeHeaders: "ETag", MaxAgeSeconds: 300, Enabled: true}
	if err := store.PutBucketCORS(ctx, cors); err != nil {
		t.Fatalf("put bucket cors failed: %v", err)
	}
	if got, err := store.GetBucketCORS(ctx, "ops"); err != nil || got.AllowedMethods != "GET,PUT" || !got.Enabled {
		t.Fatalf("unexpected bucket cors config: cfg=%+v err=%v", got, err)
	}

	policy := BucketPolicy{Bucket: "ops", Document: `{"Version":"2012-10-17"}`, Enabled: true}
	if err := store.PutBucketPolicy(ctx, policy); err != nil {
		t.Fatalf("put bucket policy failed: %v", err)
	}
	if got, err := store.GetBucketPolicy(ctx, "ops"); err != nil || !got.Enabled || got.Document == "" {
		t.Fatalf("unexpected bucket policy: cfg=%+v err=%v", got, err)
	}

	publicAccess := BucketPublicAccessBlock{Bucket: "ops", BlockPublicACLs: true, IgnorePublicACLs: true, BlockPublicPolicy: true, RestrictPublicBuckets: true}
	if err := store.PutBucketPublicAccessBlock(ctx, publicAccess); err != nil {
		t.Fatalf("put public access block failed: %v", err)
	}
	if got, err := store.GetBucketPublicAccessBlock(ctx, "ops"); err != nil || !got.BlockPublicACLs || !got.BlockPublicPolicy {
		t.Fatalf("unexpected public access block: cfg=%+v err=%v", got, err)
	}

	if err := store.DeleteBucketCORS(ctx, "ops"); err != nil {
		t.Fatalf("delete bucket cors failed: %v", err)
	}
	if _, err := store.GetBucketCORS(ctx, "ops"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected cors config not found after delete, got %v", err)
	}

	if err := store.DeleteBucketPolicy(ctx, "ops"); err != nil {
		t.Fatalf("delete bucket policy failed: %v", err)
	}
	if _, err := store.GetBucketPolicy(ctx, "ops"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected policy not found after delete, got %v", err)
	}

	if err := store.DeleteBucketPublicAccessBlock(ctx, "ops"); err != nil {
		t.Fatalf("delete public access block failed: %v", err)
	}
	if _, err := store.GetBucketPublicAccessBlock(ctx, "ops"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected public access block not found after delete, got %v", err)
	}
}
