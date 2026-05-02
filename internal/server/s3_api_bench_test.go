package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
)

func BenchmarkS3APISmallObjectPutGet(b *testing.B) {
	h := newBenchS3Handler(b)
	mustBenchRequest(b, h, http.MethodPut, "/bench", nil)
	payload := bytes.Repeat([]byte("x"), 16*1024)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload) * 2))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("/bench/objects/%09d.bin", i)
		mustBenchRequest(b, h, http.MethodPut, key, bytes.NewReader(payload))
		mustBenchRequest(b, h, http.MethodGet, key, nil)
	}
}

func BenchmarkS3APIListObjectsV2Indexed(b *testing.B) {
	h, store, bstore := newBenchS3HandlerWithStores(b)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateBucket(ctx, metadata.Bucket{Name: "bench", CreatedAt: now, Region: "us-east-1", VersioningStatus: "Suspended"}); err != nil {
		b.Fatalf("create bucket failed: %v", err)
	}
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("prefix/%03d/object-%06d.txt", i%100, i)
		ref := blob.ObjectRef{Bucket: "bench", Key: key, VersionID: "null"}
		meta, err := bstore.WriteObject(ctx, ref, bytes.NewReader([]byte("x")))
		if err != nil {
			b.Fatalf("write seed object failed: %v", err)
		}
		if err := store.PutObjectVersion(ctx, metadata.ObjectVersion{Bucket: "bench", Key: key, VersionID: "null", Size: meta.Size, ETag: meta.MD5Hex, ChecksumSHA256: meta.SHA256, StoragePath: meta.Path, CreatedAt: now}); err != nil {
			b.Fatalf("put seed metadata failed: %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mustBenchRequest(b, h, http.MethodGet, "/bench?list-type=2&prefix=prefix/050/&max-keys=1000", nil)
	}
}

func newBenchS3Handler(b *testing.B) http.Handler {
	b.Helper()
	h, _, _ := newBenchS3HandlerWithStores(b)
	return h
}

func newBenchS3HandlerWithStores(b *testing.B) (http.Handler, metadata.Store, *blob.Store) {
	b.Helper()
	root := b.TempDir()
	bstore, err := blob.NewStore(blob.Config{RootDir: filepath.Join(root, "blobs"), FsyncMode: blob.FsyncFast})
	if err != nil {
		b.Fatalf("new blob store failed: %v", err)
	}
	mstore, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:" + filepath.Join(root, "metadata.db")})
	if err != nil {
		b.Fatalf("new metadata store failed: %v", err)
	}
	h := NewHandler(Options{Verifier: allowAllVerifier{}, Metadata: mstore, Blob: bstore, MaxInFlight: 256, HeavyOpsWorkers: 8, HeavyOpsQueue: 256, BucketRegion: "us-east-1"})
	return h, mstore, bstore
}

func mustBenchRequest(b *testing.B, h http.Handler, method string, target string, body io.Reader) {
	b.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Authorization", "test")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	res := rw.Result()
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		b.Fatalf("%s %s returned %d", method, target, res.StatusCode)
	}
}
