package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type statePersister interface {
	Load(ctx context.Context) (persistedState, bool, error)
	Save(ctx context.Context, state persistedState) error
}

func newStatePersister(cfg Config) statePersister {
	if cfg.SQLDB != nil && (cfg.Backend == BackendSQLite || cfg.Backend == BackendLibSQL || cfg.Backend == BackendPostgreSQL || cfg.Backend == BackendMariaDB) {
		return &sqlStatePersister{backend: cfg.Backend, db: cfg.SQLDB}
	}
	if cfg.Valkey != nil && cfg.Backend == BackendValkey {
		return &valkeyStatePersister{client: cfg.Valkey, key: "s000:metadata:state:v1"}
	}
	if path, ok := persistentFilePath(cfg.Backend, cfg.DSN); ok {
		return &fileStatePersister{path: path}
	}
	return nil
}

type fileStatePersister struct {
	path string
}

func (p *fileStatePersister) Load(_ context.Context) (persistedState, bool, error) {
	bytes, err := os.ReadFile(p.path)
	if err != nil {
		if os.IsNotExist(err) {
			return persistedState{}, false, nil
		}
		return persistedState{}, false, err
	}
	var ps persistedState
	if err := json.Unmarshal(bytes, &ps); err != nil {
		return persistedState{}, false, fmt.Errorf("decode persisted state: %w", err)
	}
	return ps, true, nil
}

func (p *fileStatePersister) Save(_ context.Context, state persistedState) error {
	bytes, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode persisted state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return fmt.Errorf("create metadata directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(p.path), "metadata-*.tmp")
	if err != nil {
		return fmt.Errorf("create metadata temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(bytes); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write metadata temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close metadata temp file: %w", err)
	}
	if err := os.Rename(tmpPath, p.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("commit metadata file: %w", err)
	}
	return nil
}

type sqlStatePersister struct {
	backend Backend
	db      *sql.DB
	one     sync.Once
	oneErr  error
}

func (p *sqlStatePersister) ensureTable(ctx context.Context) error {
	p.one.Do(func() {
		_, p.oneErr = p.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS s000_metadata_state (
  id VARCHAR(64) PRIMARY KEY,
  payload TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`)
	})
	return p.oneErr
}

func (p *sqlStatePersister) Load(ctx context.Context) (persistedState, bool, error) {
	if err := p.ensureTable(ctx); err != nil {
		return persistedState{}, false, fmt.Errorf("ensure metadata state table: %w", err)
	}
	row := p.db.QueryRowContext(ctx, "SELECT payload FROM s000_metadata_state WHERE id = "+p.ph(1), "global")
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return persistedState{}, false, nil
		}
		return persistedState{}, false, err
	}
	var state persistedState
	if err := json.Unmarshal([]byte(payload), &state); err != nil {
		return persistedState{}, false, fmt.Errorf("decode sql metadata state: %w", err)
	}
	return state, true, nil
}

func (p *sqlStatePersister) Save(ctx context.Context, state persistedState) error {
	if err := p.ensureTable(ctx); err != nil {
		return fmt.Errorf("ensure metadata state table: %w", err)
	}
	bytes, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode persisted state: %w", err)
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "DELETE FROM s000_metadata_state WHERE id = "+p.ph(1), "global"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO s000_metadata_state (id, payload, updated_at) VALUES ("+p.ph(1)+", "+p.ph(2)+", "+p.ph(3)+")",
		"global", string(bytes), time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (p *sqlStatePersister) ph(n int) string {
	if p.backend == BackendPostgreSQL {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

type valkeyStatePersister struct {
	client *redis.Client
	key    string
}

func (p *valkeyStatePersister) Load(ctx context.Context) (persistedState, bool, error) {
	payload, err := p.client.Get(ctx, p.key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return persistedState{}, false, nil
		}
		return persistedState{}, false, err
	}
	var state persistedState
	if err := json.Unmarshal([]byte(payload), &state); err != nil {
		return persistedState{}, false, fmt.Errorf("decode valkey metadata state: %w", err)
	}
	return state, true, nil
}

func (p *valkeyStatePersister) Save(ctx context.Context, state persistedState) error {
	bytes, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode persisted state: %w", err)
	}
	return p.client.Set(ctx, p.key, string(bytes), 0).Err()
}

func persistentFilePath(backend Backend, dsn string) (string, bool) {
	if backend != BackendSQLite && backend != BackendLibSQL {
		return "", false
	}
	value := strings.TrimSpace(dsn)
	if !strings.HasPrefix(value, "file:") {
		return "", false
	}
	pathPart := strings.TrimPrefix(value, "file:")
	if idx := strings.Index(pathPart, "?"); idx >= 0 {
		pathPart = pathPart[:idx]
	}
	pathPart = strings.TrimSpace(pathPart)
	if pathPart == "" || strings.HasPrefix(pathPart, ":") {
		return "", false
	}
	if !filepath.IsAbs(pathPart) && filepath.Dir(pathPart) == "." {
		return "", false
	}
	return pathPart, true
}
