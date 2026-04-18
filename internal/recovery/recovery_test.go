package recovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateBackupCopiesDataAndMetadata(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "objects"), 0o755); err != nil {
		t.Fatalf("mkdir data objects failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "objects", "a.bin"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write object failed: %v", err)
	}
	metadataPath := filepath.Join(dataDir, "s000-metadata.db")
	if err := os.WriteFile(metadataPath, []byte("sqlite-bytes"), 0o600); err != nil {
		t.Fatalf("write metadata failed: %v", err)
	}

	backupDir := filepath.Join(root, "backup")
	if err := CreateBackup(BackupConfig{DataDir: dataDir, MetadataDSN: "file:" + metadataPath, OutputDir: backupDir}); err != nil {
		t.Fatalf("create backup failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(backupDir, "data", "objects", "a.bin")); err != nil {
		t.Fatalf("expected object copied in backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "metadata", "s000-metadata.db")); err != nil {
		t.Fatalf("expected metadata copied in backup: %v", err)
	}
}

func TestValidateRestore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	backupDir := filepath.Join(root, "backup")
	if err := os.MkdirAll(filepath.Join(backupDir, "data", "objects"), 0o755); err != nil {
		t.Fatalf("mkdir backup objects failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(backupDir, "metadata"), 0o755); err != nil {
		t.Fatalf("mkdir backup metadata failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "metadata", "s000-metadata.db"), []byte("sqlite"), 0o600); err != nil {
		t.Fatalf("write backup metadata failed: %v", err)
	}

	if err := ValidateRestore(backupDir); err != nil {
		t.Fatalf("expected restore validation success, got %v", err)
	}

	badDir := filepath.Join(root, "bad")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir bad dir failed: %v", err)
	}
	if err := ValidateRestore(badDir); err == nil {
		t.Fatal("expected restore validation failure for missing backup layout")
	}
}
