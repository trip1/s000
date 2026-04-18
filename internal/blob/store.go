package blob

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultMultipartMaxAge = 24 * time.Hour
	defaultCopyBufferSize  = 1024 * 1024
)

// FsyncMode controls durability behavior for file writes.
type FsyncMode string

const (
	// FsyncStrict fsyncs file and parent directory metadata.
	FsyncStrict FsyncMode = "strict"
	// FsyncSafe fsyncs file contents only.
	FsyncSafe FsyncMode = "safe"
	// FsyncFast skips fsync for lower latency.
	FsyncFast FsyncMode = "fast"
)

// Config configures local blob storage behavior.
type Config struct {
	RootDir         string
	FsyncMode       FsyncMode
	NowProvider     func() time.Time
	MultipartMaxAge time.Duration
	CopyBufferSize  int
}

// ObjectRef identifies a single object version.
type ObjectRef struct {
	Bucket    string
	Key       string
	VersionID string
}

// ObjectMeta describes stored object data.
type ObjectMeta struct {
	Ref       ObjectRef
	Path      string
	Size      int64
	MD5Hex    string
	SHA256    string
	CreatedAt time.Time
}

// ByteRange is an inclusive byte range [Start, End].
type ByteRange struct {
	Start int64
	End   int64
}

// MultipartPartMeta describes stored multipart part data.
type MultipartPartMeta struct {
	UploadID   string
	PartNumber int
	Path       string
	Size       int64
	MD5Hex     string
	SHA256     string
	CreatedAt  time.Time
}

// Store provides local blob IO operations.
type Store struct {
	root            string
	objectsRoot     string
	tempRoot        string
	multipartRoot   string
	fsyncMode       FsyncMode
	now             func() time.Time
	multipartMaxAge time.Duration
	copyBufferSize  int
	bufPool         sync.Pool
}

// NewStore creates a blob store and runs startup recovery.
func NewStore(cfg Config) (*Store, error) {
	if strings.TrimSpace(cfg.RootDir) == "" {
		return nil, fmt.Errorf("root dir is required")
	}
	if cfg.NowProvider == nil {
		cfg.NowProvider = func() time.Time { return time.Now().UTC() }
	}
	if cfg.MultipartMaxAge <= 0 {
		cfg.MultipartMaxAge = defaultMultipartMaxAge
	}
	if cfg.CopyBufferSize <= 0 {
		cfg.CopyBufferSize = defaultCopyBufferSize
	}
	if cfg.FsyncMode == "" {
		cfg.FsyncMode = FsyncSafe
	}
	if cfg.FsyncMode != FsyncStrict && cfg.FsyncMode != FsyncSafe && cfg.FsyncMode != FsyncFast {
		return nil, fmt.Errorf("invalid fsync mode %q", cfg.FsyncMode)
	}

	s := &Store{
		root:            cfg.RootDir,
		objectsRoot:     filepath.Join(cfg.RootDir, "objects"),
		tempRoot:        filepath.Join(cfg.RootDir, "tmp"),
		multipartRoot:   filepath.Join(cfg.RootDir, "multipart"),
		fsyncMode:       cfg.FsyncMode,
		now:             cfg.NowProvider,
		multipartMaxAge: cfg.MultipartMaxAge,
		copyBufferSize:  cfg.CopyBufferSize,
	}
	s.bufPool.New = func() any {
		buf := make([]byte, s.copyBufferSize)
		return &buf
	}

	if err := os.MkdirAll(s.objectsRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create objects root: %w", err)
	}
	if err := os.MkdirAll(s.tempRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create temp root: %w", err)
	}
	if err := os.MkdirAll(s.multipartRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create multipart root: %w", err)
	}

	if _, err := s.Recover(context.Background()); err != nil {
		return nil, err
	}

	return s, nil
}

// ObjectPath returns deterministic sharded path for one object version.
func (s *Store) ObjectPath(ref ObjectRef) string {
	keyDigest := digestHex(ref.Bucket + "\x00" + ref.Key)
	version := ref.VersionID
	if version == "" {
		version = "null"
	}
	verDigest := digestHex(version)

	return filepath.Join(
		s.objectsRoot,
		keyDigest[:2],
		keyDigest[2:4],
		keyDigest,
		verDigest+".obj",
	)
}

// WriteObject streams data into a temp file and atomically commits it.
func (s *Store) WriteObject(ctx context.Context, ref ObjectRef, src io.Reader) (ObjectMeta, error) {
	if ref.Bucket == "" || ref.Key == "" {
		return ObjectMeta{}, fmt.Errorf("bucket and key are required")
	}

	tmpFile, err := os.CreateTemp(s.tempRoot, "obj-*.tmp")
	if err != nil {
		return ObjectMeta{}, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	cleanupTemp := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}

	h := sha256.New()
	hMD5 := md5.New()
	w := io.MultiWriter(tmpFile, h, hMD5)
	bp := s.bufPool.Get().(*[]byte)
	buf := *bp
	written, err := io.CopyBuffer(w, &contextReader{ctx: ctx, src: src}, buf)
	s.bufPool.Put(bp)
	if err != nil {
		cleanupTemp()
		return ObjectMeta{}, fmt.Errorf("stream object write: %w", err)
	}

	if err := s.syncFile(tmpFile); err != nil {
		cleanupTemp()
		return ObjectMeta{}, fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		cleanupTemp()
		return ObjectMeta{}, fmt.Errorf("close temp file: %w", err)
	}

	finalPath := s.ObjectPath(ref)
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		cleanupTemp()
		return ObjectMeta{}, fmt.Errorf("create object directory: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		cleanupTemp()
		return ObjectMeta{}, fmt.Errorf("commit object atomically: %w", err)
	}
	if err := s.syncParentDir(finalPath); err != nil {
		return ObjectMeta{}, fmt.Errorf("sync parent directory: %w", err)
	}

	return ObjectMeta{
		Ref:       ref,
		Path:      finalPath,
		Size:      written,
		MD5Hex:    digestHashHex(hMD5),
		SHA256:    digestHashHex(h),
		CreatedAt: s.now(),
	}, nil
}

// ReadObject streams object bytes to dst with optional byte range.
func (s *Store) ReadObject(ctx context.Context, meta ObjectMeta, br *ByteRange, dst io.Writer) (int64, error) {
	f, err := os.Open(meta.Path)
	if err != nil {
		return 0, fmt.Errorf("open object: %w", err)
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat object: %w", err)
	}

	start := int64(0)
	end := stat.Size() - 1
	if br != nil {
		start = br.Start
		end = br.End
	}
	if start < 0 || end < start || end >= stat.Size() {
		return 0, fmt.Errorf("invalid range %d-%d for size %d", start, end, stat.Size())
	}

	section := io.NewSectionReader(f, start, end-start+1)
	bp := s.bufPool.Get().(*[]byte)
	buf := *bp
	n, err := io.CopyBuffer(dst, &contextReader{ctx: ctx, src: section}, buf)
	s.bufPool.Put(bp)
	if err != nil {
		return n, fmt.Errorf("stream object read: %w", err)
	}

	return n, nil
}

// DeleteObject deletes one version or all versions for a key.
func (s *Store) DeleteObject(_ context.Context, ref ObjectRef, versioned bool) error {
	if versioned {
		path := s.ObjectPath(ref)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete versioned object: %w", err)
		}
		s.cleanupObjectDirs(filepath.Dir(path))
		return nil
	}

	keyDir := s.objectKeyDir(ref.Bucket, ref.Key)
	entries, err := os.ReadDir(keyDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("list key versions: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(keyDir, e.Name())); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete version file: %w", err)
		}
	}
	s.cleanupObjectDirs(keyDir)
	return nil
}

// CreateMultipartUpload initializes multipart staging for one upload ID.
func (s *Store) CreateMultipartUpload(_ context.Context, uploadID string) error {
	if strings.TrimSpace(uploadID) == "" {
		return fmt.Errorf("upload id is required")
	}
	return os.MkdirAll(s.multipartUploadDir(uploadID), 0o755)
}

// WriteMultipartPart stores one multipart part using atomic temp-to-final write.
func (s *Store) WriteMultipartPart(ctx context.Context, uploadID string, partNumber int, src io.Reader) (MultipartPartMeta, error) {
	if partNumber <= 0 {
		return MultipartPartMeta{}, fmt.Errorf("part number must be positive")
	}
	if err := s.CreateMultipartUpload(ctx, uploadID); err != nil {
		return MultipartPartMeta{}, err
	}

	tmpFile, err := os.CreateTemp(s.tempRoot, "part-*.tmp")
	if err != nil {
		return MultipartPartMeta{}, fmt.Errorf("create temp part file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}

	hSHA := sha256.New()
	hMD5 := md5.New()
	bp := s.bufPool.Get().(*[]byte)
	buf := *bp
	written, err := io.CopyBuffer(io.MultiWriter(tmpFile, hSHA, hMD5), &contextReader{ctx: ctx, src: src}, buf)
	s.bufPool.Put(bp)
	if err != nil {
		cleanup()
		return MultipartPartMeta{}, fmt.Errorf("stream multipart part write: %w", err)
	}
	if err := s.syncFile(tmpFile); err != nil {
		cleanup()
		return MultipartPartMeta{}, fmt.Errorf("sync multipart temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		cleanup()
		return MultipartPartMeta{}, fmt.Errorf("close multipart temp file: %w", err)
	}

	finalPath := s.multipartPartPath(uploadID, partNumber)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		cleanup()
		return MultipartPartMeta{}, fmt.Errorf("commit multipart part atomically: %w", err)
	}
	if err := s.syncParentDir(finalPath); err != nil {
		return MultipartPartMeta{}, fmt.Errorf("sync multipart part directory: %w", err)
	}

	return MultipartPartMeta{
		UploadID:   uploadID,
		PartNumber: partNumber,
		Path:       finalPath,
		Size:       written,
		MD5Hex:     digestHashHex(hMD5),
		SHA256:     digestHashHex(hSHA),
		CreatedAt:  s.now(),
	}, nil
}

// CompleteMultipartUpload assembles parts in order into destination object.
func (s *Store) CompleteMultipartUpload(ctx context.Context, uploadID string, partNumbers []int, dst ObjectRef) (ObjectMeta, error) {
	if len(partNumbers) == 0 {
		return ObjectMeta{}, fmt.Errorf("at least one part is required")
	}
	readers := make([]io.Reader, 0, len(partNumbers))
	closers := make([]io.Closer, 0, len(partNumbers))
	for _, partNumber := range partNumbers {
		path := s.multipartPartPath(uploadID, partNumber)
		f, err := os.Open(path)
		if err != nil {
			for _, c := range closers {
				_ = c.Close()
			}
			return ObjectMeta{}, fmt.Errorf("open multipart part %d: %w", partNumber, err)
		}
		readers = append(readers, f)
		closers = append(closers, f)
	}
	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}()

	meta, err := s.WriteObject(ctx, dst, io.MultiReader(readers...))
	if err != nil {
		return ObjectMeta{}, err
	}
	if err := s.AbortMultipartUpload(ctx, uploadID); err != nil {
		return ObjectMeta{}, err
	}
	return meta, nil
}

// AbortMultipartUpload removes multipart staging for one upload.
func (s *Store) AbortMultipartUpload(_ context.Context, uploadID string) error {
	err := os.RemoveAll(s.multipartUploadDir(uploadID))
	if err != nil {
		return fmt.Errorf("remove multipart upload staging: %w", err)
	}
	return nil
}

// RemoveStaleMultipartUploads removes old multipart staging directories.
func (s *Store) RemoveStaleMultipartUploads(ctx context.Context) (int, error) {
	report, err := s.Recover(ctx)
	if err != nil {
		return 0, err
	}
	return report.RemovedMultipartDir, nil
}

// RecoveryReport describes startup cleanup results.
type RecoveryReport struct {
	RemovedTempFiles    int
	RemovedMultipartDir int
}

// Recover removes orphan temp files and stale multipart directories.
func (s *Store) Recover(_ context.Context) (RecoveryReport, error) {
	report := RecoveryReport{}

	tempEntries, err := os.ReadDir(s.tempRoot)
	if err != nil {
		return report, fmt.Errorf("read temp root: %w", err)
	}
	for _, entry := range tempEntries {
		if err := os.RemoveAll(filepath.Join(s.tempRoot, entry.Name())); err != nil {
			return report, fmt.Errorf("remove temp entry %q: %w", entry.Name(), err)
		}
		report.RemovedTempFiles++
	}

	now := s.now()
	multipartEntries, err := os.ReadDir(s.multipartRoot)
	if err != nil {
		return report, fmt.Errorf("read multipart root: %w", err)
	}
	for _, entry := range multipartEntries {
		path := filepath.Join(s.multipartRoot, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return report, fmt.Errorf("stat multipart entry %q: %w", entry.Name(), err)
		}
		if now.Sub(info.ModTime()) <= s.multipartMaxAge {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return report, fmt.Errorf("remove stale multipart %q: %w", entry.Name(), err)
		}
		report.RemovedMultipartDir++
	}

	return report, nil
}

// HealthCheck validates that required blob directories remain accessible.
func (s *Store) HealthCheck(_ context.Context) error {
	for _, p := range []string{s.root, s.objectsRoot, s.tempRoot, s.multipartRoot} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("stat %s: %w", p, err)
		}
	}
	return nil
}

func (s *Store) objectKeyDir(bucket string, key string) string {
	keyDigest := digestHex(bucket + "\x00" + key)
	return filepath.Join(s.objectsRoot, keyDigest[:2], keyDigest[2:4], keyDigest)
}

func (s *Store) multipartUploadDir(uploadID string) string {
	return filepath.Join(s.multipartRoot, uploadID)
}

func (s *Store) multipartPartPath(uploadID string, partNumber int) string {
	return filepath.Join(s.multipartUploadDir(uploadID), fmt.Sprintf("part-%06d.bin", partNumber))
}

func (s *Store) cleanupObjectDirs(startDir string) {
	current := startDir
	for {
		if current == s.objectsRoot || current == "." || current == string(filepath.Separator) {
			return
		}
		err := os.Remove(current)
		if err != nil {
			return
		}
		current = filepath.Dir(current)
	}
}

func (s *Store) syncFile(file *os.File) error {
	if s.fsyncMode == FsyncFast {
		return nil
	}
	return file.Sync()
}

func (s *Store) syncParentDir(path string) error {
	if s.fsyncMode != FsyncStrict {
		return nil
	}

	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.src.Read(p)
	}
}

func digestHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func digestHashHex(h hash.Hash) string {
	return hex.EncodeToString(h.Sum(nil))
}
