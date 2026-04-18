package metadata

import (
	"strings"
	"testing"
)

func TestRelationalMigrationPlanExists(t *testing.T) {
	t.Parallel()

	for _, backend := range []Backend{BackendSQLite, BackendLibSQL, BackendPostgreSQL, BackendMariaDB} {
		backend := backend
		t.Run(string(backend), func(t *testing.T) {
			t.Parallel()

			plan, err := MigrationPlan(backend)
			if err != nil {
				t.Fatalf("migration plan error: %v", err)
			}
			if len(plan) == 0 {
				t.Fatal("expected at least one migration")
			}
			for i := range plan {
				if i == 0 {
					continue
				}
				if plan[i].Version <= plan[i-1].Version {
					t.Fatalf("expected strictly increasing migration versions, got %d then %d", plan[i-1].Version, plan[i].Version)
				}
			}

			joined := ""
			for _, migration := range plan {
				joined += strings.ToLower(migration.UpSQL) + "\n"
			}

			for _, mustInclude := range []string{"buckets", "object_versions", "multipart_uploads", "multipart_parts", "credential_records"} {
				if !strings.Contains(joined, mustInclude) {
					t.Fatalf("expected migration SQL to include %q", mustInclude)
				}
			}
		})
	}
}

func TestValkeyMigrationPlanNotSupported(t *testing.T) {
	t.Parallel()

	if _, err := MigrationPlan(BackendValkey); err == nil {
		t.Fatal("expected valkey migration plan to be unsupported")
	}
}
