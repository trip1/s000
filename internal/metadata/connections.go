package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	_ "github.com/tursodatabase/libsql-client-go/libsql"
	_ "modernc.org/sqlite"
)

// Connections holds backend network/database connections.
type Connections struct {
	Backend Backend
	SQLDB   *sql.DB
	Valkey  *redis.Client
}

// OpenConnections initializes and validates backend connections.
func OpenConnections(ctx context.Context, cfg Config) (*Connections, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	connections := &Connections{Backend: cfg.Backend}

	switch cfg.Backend {
	case BackendSQLite:
		if err := ensureSQLiteParentDir(cfg.DSN); err != nil {
			return nil, fmt.Errorf("prepare sqlite path: %w", err)
		}
		db, err := sql.Open("sqlite", cfg.DSN)
		if err != nil {
			return nil, fmt.Errorf("open sqlite connection: %w", err)
		}
		if err := db.PingContext(ctx); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("ping sqlite connection: %w", err)
		}
		connections.SQLDB = db
	case BackendLibSQL:
		db, err := sql.Open("libsql", cfg.DSN)
		if err != nil {
			return nil, fmt.Errorf("open libsql connection: %w", err)
		}
		if err := db.PingContext(ctx); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("ping libsql connection: %w", err)
		}
		connections.SQLDB = db
	case BackendPostgreSQL:
		db, err := sql.Open("pgx", cfg.DSN)
		if err != nil {
			return nil, fmt.Errorf("open postgresql connection: %w", err)
		}
		if err := db.PingContext(ctx); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("ping postgresql connection: %w", err)
		}
		connections.SQLDB = db
	case BackendMariaDB:
		db, err := sql.Open("mysql", cfg.DSN)
		if err != nil {
			return nil, fmt.Errorf("open mariadb connection: %w", err)
		}
		if err := db.PingContext(ctx); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("ping mariadb connection: %w", err)
		}
		connections.SQLDB = db
	case BackendValkey:
		opt := &redis.Options{Addr: cfg.ValkeyAddr}
		if strings.Contains(cfg.DSN, "://") {
			parsed, err := redis.ParseURL(cfg.DSN)
			if err != nil {
				return nil, fmt.Errorf("parse valkey url: %w", err)
			}
			opt = parsed
		}

		client := redis.NewClient(opt)
		if err := client.Ping(ctx).Err(); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("ping valkey connection: %w", err)
		}
		connections.Valkey = client
	default:
		return nil, fmt.Errorf("unsupported metadata backend %q", cfg.Backend)
	}

	return connections, nil
}

func ensureSQLiteParentDir(dsn string) error {
	path := sqlitePathFromDSN(dsn)
	if path == "" || path == ":memory:" {
		return nil
	}
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func sqlitePathFromDSN(dsn string) string {
	value := strings.TrimSpace(dsn)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "file:") {
		value = strings.TrimPrefix(value, "file:")
		if i := strings.Index(value, "?"); i >= 0 {
			value = value[:i]
		}
	}
	return value
}

// Close closes any open backend connections.
func (c *Connections) Close() error {
	if c == nil {
		return nil
	}

	if c.SQLDB != nil {
		if err := c.SQLDB.Close(); err != nil {
			return err
		}
	}
	if c.Valkey != nil {
		if err := c.Valkey.Close(); err != nil {
			return err
		}
	}

	return nil
}

// Ping verifies active backend connections.
func (c *Connections) Ping(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("connections are nil")
	}
	if c.SQLDB != nil {
		if err := c.SQLDB.PingContext(ctx); err != nil {
			return err
		}
	}
	if c.Valkey != nil {
		if err := c.Valkey.Ping(ctx).Err(); err != nil {
			return err
		}
	}
	return nil
}
