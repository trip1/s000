package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
)

func TestImportDirectoryCreatesBucketsAndObjects(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	importRoot := t.TempDir()
	dataRoot := t.TempDir()

	mustWriteFile(t, filepath.Join(importRoot, "photos", "raw", "a.txt"), "alpha")
	mustWriteFile(t, filepath.Join(importRoot, "photos", "raw", "b.bin"), "beta")
	mustWriteFile(t, filepath.Join(importRoot, "docs", "index.html"), "<h1>hello</h1>")
	mustWriteFile(t, filepath.Join(importRoot, "top-level.txt"), "ignored")

	store, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file::memory:?cache=shared"})
	if err != nil {
		t.Fatalf("new metadata store: %v", err)
	}
	blobStore, err := blob.NewStore(blob.Config{RootDir: dataRoot, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store: %v", err)
	}

	result, err := ImportDirectory(ctx, ImportOptions{Directory: importRoot, Region: "us-east-1", Metadata: store, Blob: blobStore})
	if err != nil {
		t.Fatalf("import directory: %v", err)
	}
	if result.BucketsCreated != 2 {
		t.Fatalf("expected 2 created buckets, got %d", result.BucketsCreated)
	}
	if result.ObjectsAdded != 3 {
		t.Fatalf("expected 3 imported objects, got %d", result.ObjectsAdded)
	}
	if result.ObjectsSkipped != 0 {
		t.Fatalf("expected 0 skipped objects, got %d", result.ObjectsSkipped)
	}

	buckets, err := store.ListBuckets(ctx)
	if err != nil {
		t.Fatalf("list buckets: %v", err)
	}
	names := make([]string, 0, len(buckets))
	for _, b := range buckets {
		names = append(names, b.Name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "docs,photos" {
		t.Fatalf("unexpected buckets: %v", names)
	}

	obj, err := store.GetLatestObjectVersion(ctx, "photos", "raw/a.txt")
	if err != nil {
		t.Fatalf("get imported object: %v", err)
	}
	if obj.Metadata["content-type"] != "text/plain; charset=utf-8" {
		t.Fatalf("unexpected content-type: %q", obj.Metadata["content-type"])
	}

	bytes, err := os.ReadFile(filepath.Join(importRoot, "photos", "raw", "a.txt"))
	if err != nil {
		t.Fatalf("read source file: %v", err)
	}
	if string(bytes) != "alpha" {
		t.Fatalf("source file changed, got %q", string(bytes))
	}
}

func TestImportDirectorySkipsExistingObjects(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	importRoot := t.TempDir()
	dataRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(importRoot, "photos", "a.txt"), "first")
	mustWriteFile(t, filepath.Join(importRoot, "photos", "b.txt"), "second")

	store, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file::memory:?cache=shared"})
	if err != nil {
		t.Fatalf("new metadata store: %v", err)
	}
	blobStore, err := blob.NewStore(blob.Config{RootDir: dataRoot, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store: %v", err)
	}

	if err := store.CreateBucket(ctx, metadata.Bucket{Name: "photos", Region: "us-east-1", VersioningStatus: "Suspended"}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	meta, err := blobStore.WriteObject(ctx, blob.ObjectRef{Bucket: "photos", Key: "a.txt", VersionID: "null"}, strings.NewReader("existing"))
	if err != nil {
		t.Fatalf("seed object blob: %v", err)
	}
	if err := store.PutObjectVersion(ctx, metadata.ObjectVersion{
		Bucket:         "photos",
		Key:            "a.txt",
		VersionID:      "null",
		Size:           meta.Size,
		ETag:           meta.MD5Hex,
		ChecksumSHA256: meta.SHA256,
		StoragePath:    meta.Path,
		Metadata:       map[string]string{"content-type": "text/plain; charset=utf-8"},
		CreatedAt:      meta.CreatedAt,
	}); err != nil {
		t.Fatalf("seed object metadata: %v", err)
	}

	result, err := ImportDirectory(ctx, ImportOptions{Directory: importRoot, Region: "us-east-1", Metadata: store, Blob: blobStore})
	if err != nil {
		t.Fatalf("import directory: %v", err)
	}
	if result.BucketsCreated != 0 {
		t.Fatalf("expected 0 created buckets, got %d", result.BucketsCreated)
	}
	if result.ObjectsAdded != 1 || result.ObjectsSkipped != 1 {
		t.Fatalf("unexpected import result: %+v", result)
	}

	objects, err := store.ListObjects(ctx, "photos")
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("expected 2 total objects, got %d", len(objects))
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
