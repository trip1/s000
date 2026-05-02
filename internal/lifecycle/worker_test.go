package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
)

func TestRunOnceDeletesMatchingObjects(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)

	store, bstore := newLifecycleStores(t)
	seedObject(t, ctx, store, bstore, "photos", "logs/old.txt", "v1", now.Add(-2*time.Hour))
	seedObject(t, ctx, store, bstore, "photos", "logs/new.txt", "v2", now.Add(-30*time.Minute))
	seedObject(t, ctx, store, bstore, "photos", "tmp/old.txt", "v3", now.Add(-2*time.Hour))

	worker, err := NewWorker(Options{
		Metadata:     store,
		Blob:         bstore,
		Rules:        []Rule{{Prefix: "logs/", ExpireAfter: time.Hour}},
		DryRun:       false,
		BatchSize:    1,
		Now:          func() time.Time { return now },
		RetryBackoff: 0,
	})
	if err != nil {
		t.Fatalf("new worker failed: %v", err)
	}

	report, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run once failed: %v", err)
	}
	if report.Scanned != 3 {
		t.Fatalf("expected scanned=3, got %d", report.Scanned)
	}
	if report.Eligible != 1 {
		t.Fatalf("expected eligible=1, got %d", report.Eligible)
	}
	if report.Deleted != 1 {
		t.Fatalf("expected deleted=1, got %d", report.Deleted)
	}
	if report.Failed != 0 {
		t.Fatalf("expected failed=0, got %d", report.Failed)
	}

	if _, err := store.GetLatestObjectVersion(ctx, "photos", "logs/old.txt"); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("expected logs/old.txt deleted from metadata, got %v", err)
	}
	if _, err := store.GetLatestObjectVersion(ctx, "photos", "logs/new.txt"); err != nil {
		t.Fatalf("expected logs/new.txt to remain, got %v", err)
	}
	if _, err := store.GetLatestObjectVersion(ctx, "photos", "tmp/old.txt"); err != nil {
		t.Fatalf("expected tmp/old.txt to remain, got %v", err)
	}

	metrics := worker.MetricsSnapshot()
	if metrics.Runs != 1 || metrics.DeletedTotal != 1 || metrics.EligibleTotal != 1 {
		t.Fatalf("unexpected metrics snapshot: %+v", metrics)
	}
}

func TestRunOnceDryRunSkipsDeletes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)

	store, _ := newLifecycleStores(t)
	blobStub := &stubBlobStore{}
	seedObjectWithNoBlobWrite(t, ctx, store, "photos", "logs/old.txt", "v1", now.Add(-2*time.Hour))

	worker, err := NewWorker(Options{
		Metadata:   store,
		Blob:       blobStub,
		Rules:      []Rule{{Prefix: "logs/", ExpireAfter: time.Hour}},
		DryRun:     true,
		Now:        func() time.Time { return now },
		BatchSize:  100,
		MaxRetries: 1,
	})
	if err != nil {
		t.Fatalf("new worker failed: %v", err)
	}

	report, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run once failed: %v", err)
	}
	if report.Eligible != 1 {
		t.Fatalf("expected eligible=1, got %d", report.Eligible)
	}
	if report.Deleted != 0 {
		t.Fatalf("expected deleted=0 in dry-run, got %d", report.Deleted)
	}
	if blobStub.calls != 0 {
		t.Fatalf("expected no blob deletes in dry-run, got %d", blobStub.calls)
	}
	if _, err := store.GetLatestObjectVersion(ctx, "photos", "logs/old.txt"); err != nil {
		t.Fatalf("expected object to remain during dry-run, got %v", err)
	}
}

func TestRunOnceUsesBucketLifecycleXML(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	store, bstore := newLifecycleStores(t)
	seedObject(t, ctx, store, bstore, "photos", "logs/old.txt", "v1", now.Add(-48*time.Hour))
	seedObject(t, ctx, store, bstore, "photos", "logs/new.txt", "v2", now.Add(-6*time.Hour))
	if err := store.PutBucketLifecycle(ctx, metadata.BucketLifecycle{Bucket: "photos", Enabled: true, Document: `<LifecycleConfiguration><Rule><Status>Enabled</Status><Filter><Prefix>logs/</Prefix></Filter><Expiration><Days>1</Days></Expiration></Rule></LifecycleConfiguration>`}); err != nil {
		t.Fatalf("put lifecycle failed: %v", err)
	}

	worker, err := NewWorker(Options{Metadata: store, Blob: bstore, DryRun: false, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("new worker failed: %v", err)
	}
	report, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run once failed: %v", err)
	}
	if report.Eligible != 1 || report.Deleted != 1 {
		t.Fatalf("expected one lifecycle XML deletion, got report %+v", report)
	}
	if _, err := store.GetLatestObjectVersion(ctx, "photos", "logs/old.txt"); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatalf("expected old object deleted, got %v", err)
	}
	if _, err := store.GetLatestObjectVersion(ctx, "photos", "logs/new.txt"); err != nil {
		t.Fatalf("expected new object to remain, got %v", err)
	}
}

func TestRunOnceRetriesTransientDeleteFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)

	store, _ := newLifecycleStores(t)
	blobStub := &stubBlobStore{failFirst: 1}
	seedObjectWithNoBlobWrite(t, ctx, store, "photos", "logs/old.txt", "v1", now.Add(-2*time.Hour))

	worker, err := NewWorker(Options{
		Metadata:     store,
		Blob:         blobStub,
		Rules:        []Rule{{Prefix: "logs/", ExpireAfter: time.Hour}},
		DryRun:       false,
		Now:          func() time.Time { return now },
		BatchSize:    100,
		MaxRetries:   2,
		RetryBackoff: 0,
	})
	if err != nil {
		t.Fatalf("new worker failed: %v", err)
	}

	report, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run once failed: %v", err)
	}
	if report.Failed != 1 {
		t.Fatalf("expected failure count after transient blob delete error, got %d", report.Failed)
	}
	if report.Deleted != 0 {
		t.Fatalf("expected deleted=0 after failed delete, got %d", report.Deleted)
	}
	if blobStub.calls != 1 {
		t.Fatalf("expected 1 blob delete attempt, got %d", blobStub.calls)
	}
}

func TestNewWorkerRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	_, err := NewWorker(Options{})
	if err == nil || !strings.Contains(err.Error(), "metadata store is required") {
		t.Fatalf("expected metadata requirement error, got %v", err)
	}
}

func newLifecycleStores(t *testing.T) (metadata.Store, *blob.Store) {
	t.Helper()

	ctx := context.Background()
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)

	store, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:test.db"})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	if err := store.CreateBucket(ctx, metadata.Bucket{Name: "photos", CreatedAt: now, VersioningStatus: "Suspended"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}

	bstore, err := blob.NewStore(blob.Config{RootDir: t.TempDir(), FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}

	return store, bstore
}

func seedObject(t *testing.T, ctx context.Context, store metadata.Store, bstore *blob.Store, bucket, key, versionID string, createdAt time.Time) {
	t.Helper()

	meta, err := bstore.WriteObject(ctx, blob.ObjectRef{Bucket: bucket, Key: key, VersionID: versionID}, strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("write blob object failed: %v", err)
	}
	if err := store.PutObjectVersion(ctx, metadata.ObjectVersion{
		Bucket:         bucket,
		Key:            key,
		VersionID:      versionID,
		Size:           meta.Size,
		ETag:           meta.MD5Hex,
		ChecksumSHA256: meta.SHA256,
		StoragePath:    meta.Path,
		CreatedAt:      createdAt,
	}); err != nil {
		t.Fatalf("put object version failed: %v", err)
	}
}

func seedObjectWithNoBlobWrite(t *testing.T, ctx context.Context, store metadata.Store, bucket, key, versionID string, createdAt time.Time) {
	t.Helper()

	if err := store.PutObjectVersion(ctx, metadata.ObjectVersion{
		Bucket:         bucket,
		Key:            key,
		VersionID:      versionID,
		Size:           7,
		ETag:           "etag",
		ChecksumSHA256: "sha",
		StoragePath:    "unused",
		CreatedAt:      createdAt,
	}); err != nil {
		t.Fatalf("put object version failed: %v", err)
	}
}

type stubBlobStore struct {
	failFirst int
	calls     int
}

func (s *stubBlobStore) DeleteObject(_ context.Context, _ blob.ObjectRef, _ bool) error {
	s.calls++
	if s.calls <= s.failFirst {
		return errors.New("transient blob delete failure")
	}
	return nil
}
