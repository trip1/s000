package blob

import (
	"bytes"
	"context"
	"testing"
)

func BenchmarkObjectIO(b *testing.B) {
	sizes := []struct {
		name string
		n    int
	}{
		{name: "1KB", n: 1024},
		{name: "1MB", n: 1024 * 1024},
		{name: "100MB", n: 100 * 1024 * 1024},
	}

	for _, sz := range sizes {
		sz := sz
		b.Run(sz.name, func(b *testing.B) {
			ctx := context.Background()
			store, err := NewStore(Config{RootDir: b.TempDir(), FsyncMode: FsyncFast})
			if err != nil {
				b.Fatalf("new store failed: %v", err)
			}
			payload := bytes.Repeat([]byte("x"), sz.n)

			b.SetBytes(int64(sz.n))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ref := ObjectRef{Bucket: "bench", Key: "obj", VersionID: "v"}
				meta, err := store.WriteObject(ctx, ref, bytes.NewReader(payload))
				if err != nil {
					b.Fatalf("write object failed: %v", err)
				}
				if _, err := store.ReadObject(ctx, meta, nil, bytes.NewBuffer(nil)); err != nil {
					b.Fatalf("read object failed: %v", err)
				}
			}
		})
	}
}
