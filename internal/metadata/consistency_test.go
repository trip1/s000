package metadata

import (
	"context"
	"testing"
	"time"
)

func TestConsistencyValidateAndRepair(t *testing.T) {
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

	if err := store.PutObjectVersion(ctx, ObjectVersion{
		Bucket:         "missing-bucket",
		Key:            "ghost.bin",
		VersionID:      "v1",
		Size:           1,
		ETag:           "etag-ghost",
		ChecksumSHA256: "sha-ghost",
		CreatedAt:      now,
	}); err != nil {
		t.Fatalf("inject orphan object version failed: %v", err)
	}

	issues, err := store.ValidateConsistency(ctx)
	if err != nil {
		t.Fatalf("validate consistency failed: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("expected at least one consistency issue")
	}

	repaired, err := store.RepairConsistency(ctx)
	if err != nil {
		t.Fatalf("repair consistency failed: %v", err)
	}
	if repaired == 0 {
		t.Fatal("expected at least one repair action")
	}

	issues, err = store.ValidateConsistency(ctx)
	if err != nil {
		t.Fatalf("validate consistency after repair failed: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no issues after repair, got %d", len(issues))
	}
}
