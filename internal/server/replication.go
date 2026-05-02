package server

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
)

type replicationConfigurationXML struct {
	XMLName xml.Name             `xml:"ReplicationConfiguration"`
	Rules   []replicationRuleXML `xml:"Rule"`
}

type replicationRuleXML struct {
	ID          string               `xml:"ID"`
	Status      string               `xml:"Status"`
	Prefix      string               `xml:"Prefix"`
	Filter      replicationFilterXML `xml:"Filter"`
	Destination replicationDestXML   `xml:"Destination"`
}

type replicationFilterXML struct {
	Prefix string `xml:"Prefix"`
}

type replicationDestXML struct {
	Bucket   string `xml:"Bucket"`
	Endpoint string `xml:"Endpoint"`
	URL      string `xml:"Url"`
}

func (a *s3API) handlePutBucketReplication(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := a.store.GetBucket(r.Context(), bucket); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "NoSuchBucket", Message: "The specified bucket does not exist.", Resource: "/" + bucket})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024+1))
	if err != nil || !isWellFormedXML(body) {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "The XML you provided was not well-formed.", Resource: "/" + bucket})
		return
	}
	var cfg replicationConfigurationXML
	if err := xml.Unmarshal(body, &cfg); err != nil || len(cfg.Rules) == 0 {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusBadRequest, Code: "MalformedXML", Message: "The XML you provided was not well-formed.", Resource: "/" + bucket})
		return
	}
	if err := a.store.PutBucketReplication(r.Context(), metadata.BucketReplication{Bucket: bucket, Document: string(body), Enabled: true}); err != nil {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Put bucket replication failed.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *s3API) handleGetBucketReplication(w http.ResponseWriter, r *http.Request, bucket string) {
	cfg, err := a.store.GetBucketReplication(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, metadata.ErrNotFound) {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusNotFound, Code: "ReplicationConfigurationNotFoundError", Message: "The replication configuration was not found.", Resource: "/" + bucket})
			return
		}
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Get bucket replication failed.", Resource: "/" + bucket})
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(cfg.Document))
}

func (a *s3API) handleDeleteBucketReplication(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := a.store.DeleteBucketReplication(r.Context(), bucket); err != nil && !errors.Is(err, metadata.ErrNotFound) {
		writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Delete bucket replication failed.", Resource: "/" + bucket})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *s3API) emitReplication(r *http.Request, obj metadata.ObjectVersion) {
	cfg, err := a.store.GetBucketReplication(r.Context(), obj.Bucket)
	if err != nil || !cfg.Enabled || strings.TrimSpace(cfg.Document) == "" {
		return
	}
	var parsed replicationConfigurationXML
	if err := xml.Unmarshal([]byte(cfg.Document), &parsed); err != nil {
		return
	}
	for _, rule := range parsed.Rules {
		if !replicationRuleMatches(rule, obj.Key) {
			continue
		}
		endpoint := strings.TrimSpace(rule.Destination.Endpoint)
		if endpoint == "" {
			endpoint = strings.TrimSpace(rule.Destination.URL)
		}
		dstBucket := strings.TrimSpace(rule.Destination.Bucket)
		if endpoint == "" || dstBucket == "" {
			continue
		}
		go a.replicateObject(endpoint, dstBucket, obj)
	}
}

func replicationRuleMatches(rule replicationRuleXML, key string) bool {
	if !strings.EqualFold(strings.TrimSpace(rule.Status), "Enabled") {
		return false
	}
	prefix := rule.Filter.Prefix
	if prefix == "" {
		prefix = rule.Prefix
	}
	return prefix == "" || strings.HasPrefix(key, prefix)
}

func (a *s3API) replicateObject(endpoint string, dstBucket string, obj metadata.ObjectVersion) {
	var body bytes.Buffer
	meta := blob.ObjectMeta{Ref: blob.ObjectRef{Bucket: obj.Bucket, Key: obj.Key, VersionID: obj.VersionID}, Path: obj.StoragePath, Size: obj.Size, SHA256: obj.ChecksumSHA256, MD5Hex: obj.ETag, CreatedAt: obj.CreatedAt}
	if _, err := a.readMaybeEncryptedObject(&http.Request{}, meta, obj, nil, &body); err != nil {
		return
	}
	target := strings.TrimRight(endpoint, "/") + "/" + url.PathEscape(dstBucket) + "/" + escapeReplicationKey(obj.Key)
	req, err := http.NewRequest(http.MethodPut, target, bytes.NewReader(body.Bytes()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("ETag", quotedETag(obj.ETag))
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err == nil && resp != nil {
		_ = resp.Body.Close()
	}
}

func escapeReplicationKey(key string) string {
	parts := strings.Split(key, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
