package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
)

type ImportOptions struct {
	Directory string
	Region    string
	Metadata  metadata.Store
	Blob      *blob.Store
	Now       func() time.Time
}

type ImportResult struct {
	BucketsCreated int
	ObjectsAdded   int
	ObjectsSkipped int
}

func ImportDirectory(ctx context.Context, opts ImportOptions) (ImportResult, error) {
	if opts.Metadata == nil {
		return ImportResult{}, fmt.Errorf("metadata store is required")
	}
	if opts.Blob == nil {
		return ImportResult{}, fmt.Errorf("blob store is required")
	}
	root := strings.TrimSpace(opts.Directory)
	if root == "" {
		return ImportResult{}, nil
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if strings.TrimSpace(opts.Region) == "" {
		opts.Region = "us-east-1"
	}

	stat, err := os.Stat(root)
	if err != nil {
		return ImportResult{}, fmt.Errorf("inspect import directory %q: %w", root, err)
	}
	if !stat.IsDir() {
		return ImportResult{}, fmt.Errorf("import directory %q is not a directory", root)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return ImportResult{}, fmt.Errorf("read import directory %q: %w", root, err)
	}

	result := ImportResult{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		bucket := entry.Name()
		created, err := ensureBucket(ctx, opts.Metadata, bucket, opts.Region, opts.Now)
		if err != nil {
			return ImportResult{}, err
		}
		if created {
			result.BucketsCreated++
		}

		bucketRoot := filepath.Join(root, bucket)
		walkErr := filepath.WalkDir(bucketRoot, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == bucketRoot {
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}

			relPath, err := filepath.Rel(bucketRoot, path)
			if err != nil {
				return fmt.Errorf("build object key for %q: %w", path, err)
			}
			key := filepath.ToSlash(relPath)

			latest, err := opts.Metadata.GetLatestObjectVersion(ctx, bucket, key)
			if err == nil && !latest.DeleteMarker {
				result.ObjectsSkipped++
				return nil
			}
			if err != nil && !errors.Is(err, metadata.ErrNotFound) {
				return fmt.Errorf("check existing object %s/%s: %w", bucket, key, err)
			}

			meta, err := func() (blob.ObjectMeta, error) {
				f, err := os.Open(path)
				if err != nil {
					return blob.ObjectMeta{}, fmt.Errorf("open import object %q: %w", path, err)
				}
				defer func() { _ = f.Close() }()
				written, err := opts.Blob.WriteObject(ctx, blob.ObjectRef{Bucket: bucket, Key: key, VersionID: "null"}, f)
				if err != nil {
					return blob.ObjectMeta{}, fmt.Errorf("write object %s/%s: %w", bucket, key, err)
				}
				return written, nil
			}()
			if err != nil {
				return err
			}

			if err := opts.Metadata.PutObjectVersion(ctx, metadata.ObjectVersion{
				Bucket:         bucket,
				Key:            key,
				VersionID:      "null",
				Size:           meta.Size,
				ETag:           meta.MD5Hex,
				ChecksumSHA256: meta.SHA256B64,
				ChecksumSHA1:   meta.SHA1B64,
				ChecksumCRC32:  meta.CRC32B64,
				ChecksumCRC32C: meta.CRC32CB64,
				StoragePath:    meta.Path,
				Metadata: map[string]string{
					"content-type": contentTypeForPath(key),
				},
				CreatedAt: meta.CreatedAt,
			}); err != nil {
				return fmt.Errorf("write metadata for %s/%s: %w", bucket, key, err)
			}

			result.ObjectsAdded++
			return nil
		})
		if walkErr != nil {
			return ImportResult{}, fmt.Errorf("import bucket %q: %w", bucket, walkErr)
		}
	}

	return result, nil
}

func ensureBucket(ctx context.Context, store metadata.Store, name, region string, now func() time.Time) (bool, error) {
	_, err := store.GetBucket(ctx, name)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, metadata.ErrNotFound) {
		return false, fmt.Errorf("check bucket %q: %w", name, err)
	}
	if err := store.CreateBucket(ctx, metadata.Bucket{Name: name, Region: region, VersioningStatus: "Suspended", CreatedAt: now()}); err != nil {
		if errors.Is(err, metadata.ErrConflict) {
			return false, nil
		}
		return false, fmt.Errorf("create bucket %q: %w", name, err)
	}
	return true, nil
}

func contentTypeForPath(path string) string {
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if contentType == "" {
		return "application/octet-stream"
	}
	return contentType
}
