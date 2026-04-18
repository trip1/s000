package recovery

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BackupConfig configures filesystem backup generation.
type BackupConfig struct {
	DataDir     string
	MetadataDSN string
	OutputDir   string
}

// CreateBackup copies data directory and file-backed metadata DB into OutputDir.
func CreateBackup(cfg BackupConfig) error {
	if strings.TrimSpace(cfg.DataDir) == "" {
		return fmt.Errorf("data dir is required")
	}
	if strings.TrimSpace(cfg.OutputDir) == "" {
		return fmt.Errorf("output dir is required")
	}
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	dataOut := filepath.Join(cfg.OutputDir, "data")
	if err := copyTree(cfg.DataDir, dataOut); err != nil {
		return fmt.Errorf("copy data dir: %w", err)
	}

	if metadataPath := metadataFileFromDSN(cfg.MetadataDSN); metadataPath != "" {
		if err := os.MkdirAll(filepath.Join(cfg.OutputDir, "metadata"), 0o755); err != nil {
			return fmt.Errorf("create metadata backup dir: %w", err)
		}
		if err := copyFile(metadataPath, filepath.Join(cfg.OutputDir, "metadata", "s000-metadata.db")); err != nil {
			return fmt.Errorf("copy metadata db: %w", err)
		}
	}

	return nil
}

// ValidateRestore checks whether backup layout is sufficient for restore.
func ValidateRestore(backupDir string) error {
	if strings.TrimSpace(backupDir) == "" {
		return fmt.Errorf("backup dir is required")
	}
	if st, err := os.Stat(backupDir); err != nil || !st.IsDir() {
		return fmt.Errorf("backup dir is not accessible")
	}
	if st, err := os.Stat(filepath.Join(backupDir, "data", "objects")); err != nil || !st.IsDir() {
		return fmt.Errorf("backup missing data/objects")
	}
	if _, err := os.Stat(filepath.Join(backupDir, "metadata", "s000-metadata.db")); err != nil {
		return fmt.Errorf("backup missing metadata db")
	}
	return nil
}

func metadataFileFromDSN(dsn string) string {
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
	if value == ":memory:" || strings.Contains(value, "://") {
		return ""
	}
	return value
}

func copyTree(srcRoot string, dstRoot string) error {
	return filepath.WalkDir(srcRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(path, target)
	})
}

func copyFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
