package metadata

import "testing"

func TestParseBackend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    Backend
		wantErr bool
	}{
		{in: "sqlite", want: BackendSQLite},
		{in: "libsql", want: BackendLibSQL},
		{in: "postgresql", want: BackendPostgreSQL},
		{in: "mariadb", want: BackendMariaDB},
		{in: "valkey", want: BackendValkey},
		{in: "", wantErr: true},
		{in: "mongo", wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			got, err := ParseBackend(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %q", tc.in)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected backend %q, got %q", tc.want, got)
			}
		})
	}
}

func TestCapabilityMatrixContainsAllBackends(t *testing.T) {
	t.Parallel()

	got := CapabilityMatrix()
	for _, backend := range []Backend{BackendSQLite, BackendLibSQL, BackendPostgreSQL, BackendMariaDB, BackendValkey} {
		caps, ok := got[backend]
		if !ok {
			t.Fatalf("missing capability entry for backend %q", backend)
		}
		if caps.Name == "" {
			t.Fatalf("missing capability name for backend %q", backend)
		}
	}
}

func TestCapabilityMatrixMarksNativeSQLBackendsAuthoritative(t *testing.T) {
	t.Parallel()

	caps := CapabilityMatrix()
	for _, backend := range []Backend{BackendSQLite, BackendLibSQL, BackendPostgreSQL, BackendMariaDB} {
		if !caps[backend].AuthoritativeStore {
			t.Fatalf("expected backend %q to be authoritative", backend)
		}
	}
	if caps[BackendValkey].AuthoritativeStore {
		t.Fatal("expected valkey to remain non-authoritative")
	}
}

func TestConfigValidationByBackend(t *testing.T) {
	t.Parallel()

	t.Run("sqlite requires dsn", func(t *testing.T) {
		t.Parallel()
		cfg := Config{Backend: BackendSQLite}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected validation error for empty sqlite dsn")
		}
	})

	t.Run("postgresql requires dsn", func(t *testing.T) {
		t.Parallel()
		cfg := Config{Backend: BackendPostgreSQL}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected validation error for empty postgres dsn")
		}
	})

	t.Run("valkey requires address", func(t *testing.T) {
		t.Parallel()
		cfg := Config{Backend: BackendValkey}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected validation error for empty valkey address")
		}
	})
}
