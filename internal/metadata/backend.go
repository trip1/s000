package metadata

import (
	"fmt"
	"strings"
	"time"
)

// Backend identifies a metadata backend type.
type Backend string

const (
	// BackendSQLite is local embedded sqlite metadata storage.
	BackendSQLite Backend = "sqlite"
	// BackendLibSQL is libsql metadata storage.
	BackendLibSQL Backend = "libsql"
	// BackendPostgreSQL is PostgreSQL metadata storage.
	BackendPostgreSQL Backend = "postgresql"
	// BackendMariaDB is MariaDB metadata storage.
	BackendMariaDB Backend = "mariadb"
	// BackendValkey is valkey-backed metadata/cache coordination storage.
	BackendValkey Backend = "valkey"
)

// Capabilities describes backend behavior relevant to metadata semantics.
type Capabilities struct {
	Name                 string
	Relational           bool
	SupportsTransactions bool
	SupportsMigrations   bool
	AuthoritativeStore   bool
}

// Config configures metadata backend selection.
type Config struct {
	Backend     Backend
	DSN         string
	ValkeyAddr  string
	NowProvider func() time.Time
}

// ParseBackend parses and validates a backend value.
func ParseBackend(value string) (Backend, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(BackendSQLite):
		return BackendSQLite, nil
	case string(BackendLibSQL):
		return BackendLibSQL, nil
	case string(BackendPostgreSQL):
		return BackendPostgreSQL, nil
	case string(BackendMariaDB):
		return BackendMariaDB, nil
	case string(BackendValkey):
		return BackendValkey, nil
	default:
		return "", fmt.Errorf("unsupported metadata backend %q", value)
	}
}

// CapabilityMatrix returns backend capabilities for all supported backends.
func CapabilityMatrix() map[Backend]Capabilities {
	return map[Backend]Capabilities{
		BackendSQLite: {
			Name:                 "SQLite",
			Relational:           true,
			SupportsTransactions: true,
			SupportsMigrations:   true,
			AuthoritativeStore:   true,
		},
		BackendLibSQL: {
			Name:                 "libSQL",
			Relational:           true,
			SupportsTransactions: true,
			SupportsMigrations:   true,
			AuthoritativeStore:   true,
		},
		BackendPostgreSQL: {
			Name:                 "PostgreSQL",
			Relational:           true,
			SupportsTransactions: true,
			SupportsMigrations:   true,
			AuthoritativeStore:   true,
		},
		BackendMariaDB: {
			Name:                 "MariaDB",
			Relational:           true,
			SupportsTransactions: true,
			SupportsMigrations:   true,
			AuthoritativeStore:   true,
		},
		BackendValkey: {
			Name:                 "Valkey",
			Relational:           false,
			SupportsTransactions: true,
			SupportsMigrations:   false,
			AuthoritativeStore:   false,
		},
	}
}

// Validate validates metadata configuration by backend type.
func (c Config) Validate() error {
	if c.Backend == "" {
		return fmt.Errorf("metadata backend is required")
	}

	switch c.Backend {
	case BackendSQLite, BackendLibSQL, BackendPostgreSQL, BackendMariaDB:
		if strings.TrimSpace(c.DSN) == "" {
			return fmt.Errorf("metadata dsn is required for backend %q", c.Backend)
		}
	case BackendValkey:
		if strings.TrimSpace(c.ValkeyAddr) == "" {
			return fmt.Errorf("valkey address is required for backend %q", c.Backend)
		}
	default:
		return fmt.Errorf("unsupported metadata backend %q", c.Backend)
	}

	return nil
}
