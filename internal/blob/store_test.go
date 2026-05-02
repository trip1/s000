package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestObjectPathDeterministicAndSharded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s, err := NewStore(Config{RootDir: root, FsyncMode: FsyncFast})
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	ref := ObjectRef{Bucket: "photos", Key: "a/b/c.jpg", VersionID: "v1"}
	p1 := s.ObjectPath(ref)
	p2 := s.ObjectPath(ref)
	if p1 != p2 {
		t.Fatalf("expected deterministic path, got %q and %q", p1, p2)
	}

	if !strings.Contains(p1, filepath.Join(root, "objects")) {
		t.Fatalf("expected object path under objects root, got %q", p1)
	}

	other := s.ObjectPath(ObjectRef{Bucket: "photos", Key: "a/b/c.jpg", VersionID: "v2"})
	if p1 == other {
		t.Fatalf("expected distinct versions to map to different paths: %q", p1)
	}
}

func TestWriteObjectComputesChecksumAndCommitsAtomically(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s, err := NewStore(Config{RootDir: root, FsyncMode: FsyncFast})
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	ctx := context.Background()
	ref := ObjectRef{Bucket: "docs", Key: "readme.txt", VersionID: "v1"}
	meta, err := s.WriteObject(ctx, ref, strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("write object failed: %v", err)
	}

	wantSum := sha256.Sum256([]byte("hello world"))
	if meta.Size != 11 {
		t.Fatalf("expected size 11, got %d", meta.Size)
	}
	if meta.SHA256 != hex.EncodeToString(wantSum[:]) {
		t.Fatalf("expected sha %q, got %q", hex.EncodeToString(wantSum[:]), meta.SHA256)
	}

	data, err := os.ReadFile(meta.Path)
	if err != nil {
		t.Fatalf("read committed file failed: %v", err)
	}
	if string(data) != "hello world" {
		t.Fatalf("expected committed content %q, got %q", "hello world", string(data))
	}

	tempEntries, err := os.ReadDir(filepath.Join(root, "tmp"))
	if err != nil {
		t.Fatalf("read temp dir failed: %v", err)
	}
	if len(tempEntries) != 0 {
		t.Fatalf("expected temp dir to be empty, found %d entries", len(tempEntries))
	}
}

func TestWriteObjectFailureCleansTempAndSkipsCommit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s, err := NewStore(Config{RootDir: root, FsyncMode: FsyncFast})
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	ctx := context.Background()
	ref := ObjectRef{Bucket: "docs", Key: "broken.bin", VersionID: "v1"}
	_, err = s.WriteObject(ctx, ref, &failingReader{good: []byte("abc"), err: errors.New("boom")})
	if err == nil {
		t.Fatal("expected write error")
	}

	if _, statErr := os.Stat(s.ObjectPath(ref)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no committed object file, got stat error %v", statErr)
	}

	tempEntries, err := os.ReadDir(filepath.Join(root, "tmp"))
	if err != nil {
		t.Fatalf("read temp dir failed: %v", err)
	}
	if len(tempEntries) != 0 {
		t.Fatalf("expected temp dir to be empty after failed write, found %d entries", len(tempEntries))
	}
}

func TestWriteObjectContextCancellationCleansTempAndSkipsCommit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s, err := NewStore(Config{RootDir: root, FsyncMode: FsyncFast})
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	reader := &slowReader{remaining: 8 * 1024 * 1024, pause: 1 * time.Millisecond}

	errCh := make(chan error, 1)
	go func() {
		_, werr := s.WriteObject(ctx, ObjectRef{Bucket: "docs", Key: "cancel.bin", VersionID: "v1"}, reader)
		errCh <- werr
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	if werr := <-errCh; werr == nil {
		t.Fatal("expected write to fail after context cancellation")
	}
	if _, statErr := os.Stat(s.ObjectPath(ObjectRef{Bucket: "docs", Key: "cancel.bin", VersionID: "v1"})); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no committed file after cancellation, got %v", statErr)
	}

	tempEntries, err := os.ReadDir(filepath.Join(root, "tmp"))
	if err != nil {
		t.Fatalf("read temp dir failed: %v", err)
	}
	if len(tempEntries) != 0 {
		t.Fatalf("expected temp dir empty after cancellation, found %d entries", len(tempEntries))
	}
}

func TestWriteObjectDiskFullLikeErrorCleansTempAndSkipsCommit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s, err := NewStore(Config{RootDir: root, FsyncMode: FsyncFast})
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	_, err = s.WriteObject(context.Background(), ObjectRef{Bucket: "docs", Key: "full.bin", VersionID: "v1"}, &failingReader{good: []byte("abc"), err: syscall.ENOSPC})
	if err == nil {
		t.Fatal("expected disk full-like write error")
	}
	if _, statErr := os.Stat(s.ObjectPath(ObjectRef{Bucket: "docs", Key: "full.bin", VersionID: "v1"})); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected no committed file after disk-full error, got %v", statErr)
	}
}

func TestReadObjectSupportsRanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s, err := NewStore(Config{RootDir: root, FsyncMode: FsyncFast})
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	ctx := context.Background()
	ref := ObjectRef{Bucket: "photos", Key: "bytes.bin", VersionID: "v1"}
	meta, err := s.WriteObject(ctx, ref, strings.NewReader("0123456789"))
	if err != nil {
		t.Fatalf("write object failed: %v", err)
	}

	var partial bytes.Buffer
	n, err := s.ReadObject(ctx, meta, &ByteRange{Start: 2, End: 5}, &partial)
	if err != nil {
		t.Fatalf("range read failed: %v", err)
	}
	if n != 4 || partial.String() != "2345" {
		t.Fatalf("expected range bytes %q (%d), got %q (%d)", "2345", 4, partial.String(), n)
	}

	var full bytes.Buffer
	n, err = s.ReadObject(ctx, meta, nil, &full)
	if err != nil {
		t.Fatalf("full read failed: %v", err)
	}
	if n != 10 || full.String() != "0123456789" {
		t.Fatalf("expected full bytes %q (%d), got %q (%d)", "0123456789", 10, full.String(), n)
	}
}

func TestPromoteMultipartPartCommitsWithoutRecopy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s, err := NewStore(Config{RootDir: root, FsyncMode: FsyncFast})
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	ctx := context.Background()
	part, err := s.WriteMultipartPart(ctx, "u1", 1, strings.NewReader("hello multipart"))
	if err != nil {
		t.Fatalf("write part failed: %v", err)
	}
	ref := ObjectRef{Bucket: "backup", Key: "one-part.bin", VersionID: "v1"}
	meta, err := s.PromoteMultipartPart(ctx, "u1", 1, ref, part)
	if err != nil {
		t.Fatalf("promote part failed: %v", err)
	}
	if meta.Path != s.ObjectPath(ref) || meta.MD5Hex != part.MD5Hex || meta.SHA256 != part.SHA256 {
		t.Fatalf("unexpected promoted metadata: %+v part=%+v", meta, part)
	}
	if _, err := os.Stat(part.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected original part path gone after rename, got %v", err)
	}
	var got bytes.Buffer
	if _, err := s.ReadObject(ctx, meta, nil, &got); err != nil {
		t.Fatalf("read promoted object failed: %v", err)
	}
	if got.String() != "hello multipart" {
		t.Fatalf("unexpected promoted object content %q", got.String())
	}
}

func TestDeleteObjectVersionedAndUnversioned(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s, err := NewStore(Config{RootDir: root, FsyncMode: FsyncFast})
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	ctx := context.Background()
	refV1 := ObjectRef{Bucket: "photos", Key: "a.jpg", VersionID: "v1"}
	refV2 := ObjectRef{Bucket: "photos", Key: "a.jpg", VersionID: "v2"}
	if _, err := s.WriteObject(ctx, refV1, strings.NewReader("v1")); err != nil {
		t.Fatalf("write v1 failed: %v", err)
	}
	if _, err := s.WriteObject(ctx, refV2, strings.NewReader("v2")); err != nil {
		t.Fatalf("write v2 failed: %v", err)
	}

	if err := s.DeleteObject(ctx, refV1, true); err != nil {
		t.Fatalf("delete versioned failed: %v", err)
	}
	if _, err := os.Stat(s.ObjectPath(refV1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected v1 deleted, got %v", err)
	}
	if _, err := os.Stat(s.ObjectPath(refV2)); err != nil {
		t.Fatalf("expected v2 to remain, got %v", err)
	}

	if err := s.DeleteObject(ctx, refV2, false); err != nil {
		t.Fatalf("delete unversioned failed: %v", err)
	}
	if _, err := os.Stat(s.ObjectPath(refV2)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected v2 deleted after unversioned delete, got %v", err)
	}
}

func TestFsyncModesAccepted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, mode := range []FsyncMode{FsyncStrict, FsyncSafe, FsyncFast} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			s, err := NewStore(Config{RootDir: t.TempDir(), FsyncMode: mode})
			if err != nil {
				t.Fatalf("new store failed: %v", err)
			}
			if _, err := s.WriteObject(ctx, ObjectRef{Bucket: "b", Key: "k", VersionID: "v"}, strings.NewReader("payload")); err != nil {
				t.Fatalf("write object failed for mode %q: %v", mode, err)
			}
		})
	}
}

func TestFsyncErrorPaths(t *testing.T) {
	t.Parallel()

	s, err := NewStore(Config{RootDir: t.TempDir(), FsyncMode: FsyncStrict})
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	f, err := os.CreateTemp(t.TempDir(), "sync-*.tmp")
	if err != nil {
		t.Fatalf("create temp file failed: %v", err)
	}
	_ = f.Close()
	if err := s.syncFile(f); err == nil {
		t.Fatal("expected syncFile to fail on closed file")
	}

	if err := s.syncParentDir(filepath.Join(t.TempDir(), "missing", "obj.bin")); err == nil {
		t.Fatal("expected syncParentDir to fail for missing parent dir")
	}
}

func TestStartupRecoveryRemovesOrphanTempAndStaleMultipart(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	now := time.Date(2026, 4, 16, 15, 0, 0, 0, time.UTC)

	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o755); err != nil {
		t.Fatalf("mkdir tmp failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "tmp", "orphan.tmp"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write orphan temp failed: %v", err)
	}

	staleDir := filepath.Join(root, "multipart", "stale-upload")
	activeDir := filepath.Join(root, "multipart", "active-upload")
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatalf("mkdir stale multipart failed: %v", err)
	}
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatalf("mkdir active multipart failed: %v", err)
	}
	staleTime := now.Add(-2 * time.Hour)
	if err := os.Chtimes(staleDir, staleTime, staleTime); err != nil {
		t.Fatalf("chtimes stale multipart failed: %v", err)
	}
	if err := os.Chtimes(activeDir, now, now); err != nil {
		t.Fatalf("chtimes active multipart failed: %v", err)
	}

	_, err := NewStore(Config{
		RootDir:         root,
		FsyncMode:       FsyncFast,
		NowProvider:     func() time.Time { return now },
		MultipartMaxAge: time.Hour,
	})
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "tmp", "orphan.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected orphan temp removed, got %v", err)
	}
	if _, err := os.Stat(staleDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale multipart removed, got %v", err)
	}
	if _, err := os.Stat(activeDir); err != nil {
		t.Fatalf("expected active multipart to remain, got %v", err)
	}
}

type failingReader struct {
	good []byte
	err  error
	read bool
}

type slowReader struct {
	remaining int
	pause     time.Duration
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	time.Sleep(r.pause)
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	r.remaining -= n
	return n, nil
}

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.read {
		r.read = true
		n := copy(p, r.good)
		return n, r.err
	}
	return 0, io.EOF
}
