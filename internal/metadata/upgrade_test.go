package metadata

import "testing"

func TestUpgradePathIncludesMigrationsAfterFromVersion(t *testing.T) {
	t.Parallel()

	path, err := UpgradePath(BackendSQLite, 0)
	if err != nil {
		t.Fatalf("upgrade path failed: %v", err)
	}
	if len(path) < 1 {
		t.Fatal("expected at least one migration for pre-1.0 upgrade")
	}
	if path[0].Version != 1 {
		t.Fatalf("expected first migration version 1, got %d", path[0].Version)
	}

	next, err := UpgradePath(BackendSQLite, 1)
	if err != nil {
		t.Fatalf("upgrade path from v1 failed: %v", err)
	}
	for _, m := range next {
		if m.Version <= 1 {
			t.Fatalf("expected versions > 1 only, got %d", m.Version)
		}
	}
}

func TestUpgradePathRejectsFutureVersion(t *testing.T) {
	t.Parallel()

	if _, err := UpgradePath(BackendSQLite, 999); err == nil {
		t.Fatal("expected error for from-version newer than latest")
	}
}
