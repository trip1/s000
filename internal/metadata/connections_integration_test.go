//go:build integration

package metadata

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenConnectionsSQLiteIntegration(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dsn := "file:" + filepath.Join(t.TempDir(), "sqlite-meta.db")
	connections, err := OpenConnections(ctx, Config{Backend: BackendSQLite, DSN: dsn})
	if err != nil {
		t.Fatalf("open sqlite connections failed: %v", err)
	}
	defer func() { _ = connections.Close() }()

	if err := connections.Ping(ctx); err != nil {
		t.Fatalf("sqlite ping failed: %v", err)
	}
}

func TestOpenConnectionsLibSQLIntegration(t *testing.T) {
	t.Parallel()

	dsn := strings.TrimSpace(os.Getenv("S000_TEST_LIBSQL_DSN"))
	if dsn == "" {
		t.Skip("S000_TEST_LIBSQL_DSN not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connections, err := OpenConnections(ctx, Config{Backend: BackendLibSQL, DSN: dsn})
	if err != nil {
		t.Fatalf("open libsql connections failed: %v", err)
	}
	defer func() { _ = connections.Close() }()

	if err := connections.Ping(ctx); err != nil {
		t.Fatalf("libsql ping failed: %v", err)
	}
}

func TestOpenConnectionsPostgreSQLIntegration(t *testing.T) {
	t.Parallel()

	dsn := strings.TrimSpace(os.Getenv("S000_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("S000_TEST_POSTGRES_DSN not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connections, err := OpenConnections(ctx, Config{Backend: BackendPostgreSQL, DSN: dsn})
	if err != nil {
		t.Fatalf("open postgresql connections failed: %v", err)
	}
	defer func() { _ = connections.Close() }()

	if err := connections.Ping(ctx); err != nil {
		t.Fatalf("postgresql ping failed: %v", err)
	}
}

func TestOpenConnectionsMariaDBIntegration(t *testing.T) {
	t.Parallel()

	dsn := strings.TrimSpace(os.Getenv("S000_TEST_MARIADB_DSN"))
	if dsn == "" {
		t.Skip("S000_TEST_MARIADB_DSN not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connections, err := OpenConnections(ctx, Config{Backend: BackendMariaDB, DSN: dsn})
	if err != nil {
		t.Fatalf("open mariadb connections failed: %v", err)
	}
	defer func() { _ = connections.Close() }()

	if err := connections.Ping(ctx); err != nil {
		t.Fatalf("mariadb ping failed: %v", err)
	}
}

func TestOpenConnectionsValkeyIntegration(t *testing.T) {
	t.Parallel()

	addr := strings.TrimSpace(os.Getenv("S000_TEST_VALKEY_ADDR"))
	if addr == "" {
		t.Skip("S000_TEST_VALKEY_ADDR not set")
	}

	dsn := strings.TrimSpace(os.Getenv("S000_TEST_VALKEY_DSN"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connections, err := OpenConnections(ctx, Config{Backend: BackendValkey, ValkeyAddr: addr, DSN: dsn})
	if err != nil {
		t.Fatalf("open valkey connections failed: %v", err)
	}
	defer func() { _ = connections.Close() }()

	if err := connections.Ping(ctx); err != nil {
		t.Fatalf("valkey ping failed: %v", err)
	}
}
