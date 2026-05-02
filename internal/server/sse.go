package server

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"net/http"
	"os"
	"strings"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
)

const (
	sseAlgorithmAES256 = "AES256"
	sseMetadataKey     = "__s000_sse"
	sseIVSize          = aes.BlockSize
)

type hashingReader struct {
	src    io.Reader
	sha256 hash.Hash
	sha1   hash.Hash
	md5    hash.Hash
	crc32  hash.Hash32
	crc32c hash.Hash32
	size   int64
}

func newHashingReader(src io.Reader) *hashingReader {
	return &hashingReader{src: src, sha256: sha256.New(), sha1: sha1.New(), md5: md5.New(), crc32: crc32.NewIEEE(), crc32c: crc32.New(crc32.MakeTable(crc32.Castagnoli))}
}

func (r *hashingReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 {
		chunk := p[:n]
		r.size += int64(n)
		_, _ = r.sha256.Write(chunk)
		_, _ = r.sha1.Write(chunk)
		_, _ = r.md5.Write(chunk)
		_, _ = r.crc32.Write(chunk)
		_, _ = r.crc32c.Write(chunk)
	}
	return n, err
}

func (r *hashingReader) objectMeta(base blob.ObjectMeta) blob.ObjectMeta {
	base.Size = r.size
	base.MD5Hex = hex.EncodeToString(r.md5.Sum(nil))
	base.SHA256 = hex.EncodeToString(r.sha256.Sum(nil))
	base.SHA256B64 = base64.StdEncoding.EncodeToString(r.sha256.Sum(nil))
	base.SHA1B64 = base64.StdEncoding.EncodeToString(r.sha1.Sum(nil))
	base.CRC32B64 = base64.StdEncoding.EncodeToString(r.crc32.Sum(nil))
	base.CRC32CB64 = base64.StdEncoding.EncodeToString(r.crc32c.Sum(nil))
	return base
}

func sseRequested(r *http.Request) (bool, string) {
	value := strings.TrimSpace(r.Header.Get("x-amz-server-side-encryption"))
	if value == "" {
		return false, ""
	}
	return true, value
}

func (a *s3API) validateSSEHeaders(w http.ResponseWriter, r *http.Request, bucket, key string) (bool, bool) {
	requested, alg := sseRequested(r)
	if !requested {
		return false, true
	}
	if alg != sseAlgorithmAES256 {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidArgument", Message: "Unsupported server-side encryption algorithm.", Resource: "/" + bucket + "/" + key})
		return false, false
	}
	if len(a.sseKey) != 32 {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "InvalidRequest", Message: "SSE-S3 is not configured.", Resource: "/" + bucket + "/" + key})
		return false, false
	}
	return true, true
}

func (a *s3API) writeMaybeEncryptedObject(r *http.Request, ref blob.ObjectRef, src io.Reader, encrypted bool) (blob.ObjectMeta, error) {
	if !encrypted {
		return a.blob.WriteObject(r.Context(), ref, src)
	}
	iv := make([]byte, sseIVSize)
	if _, err := rand.Read(iv); err != nil {
		return blob.ObjectMeta{}, fmt.Errorf("generate sse iv: %w", err)
	}
	block, err := aes.NewCipher(a.sseKey)
	if err != nil {
		return blob.ObjectMeta{}, err
	}
	plain := newHashingReader(src)
	ciphertext := cipher.StreamReader{S: cipher.NewCTR(block, iv), R: plain}
	base, err := a.blob.WriteObject(r.Context(), ref, io.MultiReader(bytes.NewReader(iv), ciphertext))
	if err != nil {
		return blob.ObjectMeta{}, err
	}
	return plain.objectMeta(base), nil
}

func (a *s3API) readMaybeEncryptedObject(r *http.Request, meta blob.ObjectMeta, obj metadata.ObjectVersion, br *blob.ByteRange, dst io.Writer) (int64, error) {
	if !objectUsesSSE(obj) {
		return a.blob.ReadObject(r.Context(), meta, br, dst)
	}
	if len(a.sseKey) != 32 {
		return 0, fmt.Errorf("sse-s3 is not configured")
	}
	f, err := os.Open(meta.Path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	iv := make([]byte, sseIVSize)
	if _, err := io.ReadFull(f, iv); err != nil {
		return 0, err
	}
	start, end := int64(0), obj.Size-1
	if br != nil {
		start, end = br.Start, br.End
	}
	if start < 0 || end < start || end >= obj.Size {
		return 0, fmt.Errorf("invalid range %d-%d for size %d", start, end, obj.Size)
	}
	block, err := aes.NewCipher(a.sseKey)
	if err != nil {
		return 0, err
	}
	blockStart := start - start%int64(block.BlockSize())
	streamIV := advanceCTRIV(iv, blockStart/int64(block.BlockSize()))
	if _, err := f.Seek(sseIVSize+blockStart, io.SeekStart); err != nil {
		return 0, err
	}
	reader := cipher.StreamReader{S: cipher.NewCTR(block, streamIV), R: io.LimitReader(f, end-blockStart+1)}
	if discard := start - blockStart; discard > 0 {
		if _, err := io.CopyN(io.Discard, reader, discard); err != nil {
			return 0, err
		}
	}
	return io.Copy(dst, reader)
}

func advanceCTRIV(iv []byte, blocks int64) []byte {
	out := append([]byte(nil), iv...)
	carry := uint64(blocks)
	for i := len(out) - 1; i >= 0 && carry > 0; i-- {
		sum := uint64(out[i]) + (carry & 0xff)
		out[i] = byte(sum)
		carry = (carry >> 8) + (sum >> 8)
	}
	return out
}

func objectUsesSSE(obj metadata.ObjectVersion) bool {
	return obj.Metadata != nil && obj.Metadata[sseMetadataKey] == sseAlgorithmAES256
}

func setSSEHeader(h http.Header, obj metadata.ObjectVersion) {
	if objectUsesSSE(obj) {
		h.Set("x-amz-server-side-encryption", sseAlgorithmAES256)
	}
}

func markSSEMetadata(meta map[string]string, encrypted bool) map[string]string {
	if meta == nil {
		meta = map[string]string{}
	}
	if encrypted {
		meta[sseMetadataKey] = sseAlgorithmAES256
	} else {
		delete(meta, sseMetadataKey)
	}
	return meta
}
