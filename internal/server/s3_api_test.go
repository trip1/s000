package server

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/xml"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ds9labs.com/s000/internal/auth"
	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
)

type s3ErrorBody struct {
	Code string `xml:"Code"`
}

func TestBucketLifecycleAndVersioning(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)

	resp := execute(t, h, http.MethodPut, "/photos", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create bucket status = %d", resp.StatusCode)
	}

	resp = execute(t, h, http.MethodGet, "/", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list buckets status = %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "<Name>photos</Name>") {
		t.Fatalf("list buckets body missing bucket: %s", body)
	}

	resp = execute(t, h, http.MethodGet, "/photos?location", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get bucket location status = %d", resp.StatusCode)
	}
	if !strings.Contains(readBody(t, resp), "us-east-1") {
		t.Fatal("expected location us-east-1")
	}

	resp = execute(t, h, http.MethodPut, "/photos?versioning", `<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Status>Enabled</Status></VersioningConfiguration>`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put bucket versioning status = %d", resp.StatusCode)
	}

	resp = execute(t, h, http.MethodGet, "/photos?versioning", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get bucket versioning status = %d", resp.StatusCode)
	}
	if !strings.Contains(readBody(t, resp), "<Status>Enabled</Status>") {
		t.Fatal("expected enabled versioning status")
	}
}

func TestObjectCRUDCopyAndListObjectsV2(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)

	if execute(t, h, http.MethodPut, "/photos", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create photos bucket")
	}
	if execute(t, h, http.MethodPut, "/archive", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create archive bucket")
	}

	md5 := base64.StdEncoding.EncodeToString([]byte{0x5d, 0x41, 0x40, 0x2a, 0xbc, 0x4b, 0x2a, 0x76, 0xb9, 0x71, 0x9d, 0x91, 0x10, 0x17, 0xc5, 0x92}) // hello
	req := httptest.NewRequest(http.MethodPut, "/photos/album/hello.txt", strings.NewReader("hello"))
	req.Header.Set("Content-MD5", md5)
	req.Header.Set("X-Amz-Meta-Origin", "camera")
	req.Header.Set("Authorization", "test")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("put object status = %d body=%s", rw.Code, rw.Body.String())
	}
	if rw.Header().Get("ETag") == "" {
		t.Fatal("expected ETag header")
	}

	resp := execute(t, h, http.MethodHead, "/photos/album/hello.txt", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("head object status = %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Amz-Meta-Origin") != "camera" {
		t.Fatalf("expected user metadata header, got %q", resp.Header.Get("X-Amz-Meta-Origin"))
	}

	resp = execute(t, h, http.MethodGet, "/photos/album/hello.txt", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get object status = %d", resp.StatusCode)
	}
	if readBody(t, resp) != "hello" {
		t.Fatal("unexpected object body")
	}

	req = httptest.NewRequest(http.MethodPut, "/archive/hello-copy.txt", nil)
	req.Header.Set("Authorization", "test")
	req.Header.Set("X-Amz-Copy-Source", "/photos/album/hello.txt")
	rw = httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("copy object status = %d body=%s", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), "CopyObjectResult") {
		t.Fatal("expected copy object XML response")
	}

	resp = execute(t, h, http.MethodGet, "/photos?list-type=2&prefix=album/&max-keys=1000", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list objects status = %d", resp.StatusCode)
	}
	if !strings.Contains(readBody(t, resp), "<Key>album/hello.txt</Key>") {
		t.Fatal("expected listed key album/hello.txt")
	}

	resp = execute(t, h, http.MethodDelete, "/photos/album/hello.txt", "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete object status = %d", resp.StatusCode)
	}

	resp = execute(t, h, http.MethodGet, "/photos/album/hello.txt", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", resp.StatusCode)
	}
}

func TestPutObjectChecksumHeadersValidatedAndReturned(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	if execute(t, h, http.MethodPut, "/photos", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}
	payload := []byte("hello checksums")
	sha256Sum := sha256.Sum256(payload)
	sha1Sum := sha1.Sum(payload)
	crc := crc32.ChecksumIEEE(payload)
	crcBytes := []byte{byte(crc >> 24), byte(crc >> 16), byte(crc >> 8), byte(crc)}
	crc32c := crc32.Checksum(payload, crc32.MakeTable(crc32.Castagnoli))
	crc32cBytes := []byte{byte(crc32c >> 24), byte(crc32c >> 16), byte(crc32c >> 8), byte(crc32c)}

	req := httptest.NewRequest(http.MethodPut, "/photos/checksum.txt", strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "test")
	req.Header.Set("x-amz-checksum-sha256", base64.StdEncoding.EncodeToString(sha256Sum[:]))
	req.Header.Set("x-amz-checksum-sha1", base64.StdEncoding.EncodeToString(sha1Sum[:]))
	req.Header.Set("x-amz-checksum-crc32", base64.StdEncoding.EncodeToString(crcBytes))
	req.Header.Set("x-amz-checksum-crc32c", base64.StdEncoding.EncodeToString(crc32cBytes))
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("put checksum object status=%d body=%s", rw.Code, rw.Body.String())
	}
	if rw.Header().Get("x-amz-checksum-sha256") != base64.StdEncoding.EncodeToString(sha256Sum[:]) {
		t.Fatalf("expected sha256 checksum response header, got %q", rw.Header().Get("x-amz-checksum-sha256"))
	}

	resp := execute(t, h, http.MethodHead, "/photos/checksum.txt", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("head checksum object status=%d", resp.StatusCode)
	}
	if resp.Header.Get("x-amz-checksum-crc32c") != base64.StdEncoding.EncodeToString(crc32cBytes) {
		t.Fatalf("expected crc32c checksum header, got %q", resp.Header.Get("x-amz-checksum-crc32c"))
	}

	req = httptest.NewRequest(http.MethodPut, "/photos/bad-checksum.txt", strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "test")
	req.Header.Set("x-amz-checksum-sha256", base64.StdEncoding.EncodeToString([]byte("bad")))
	rw = httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("expected bad checksum 400, got %d body=%s", rw.Code, rw.Body.String())
	}
}

func TestDeleteBucketRequiresEmptyBucket(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	if execute(t, h, http.MethodPut, "/photos", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}
	if execute(t, h, http.MethodPut, "/photos/file.txt", "data").StatusCode != http.StatusOK {
		t.Fatal("failed to put object")
	}

	resp := execute(t, h, http.MethodDelete, "/photos", "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected conflict for non-empty bucket delete, got %d", resp.StatusCode)
	}
}

func TestMultiDeleteAndBucketConfigurationAPIs(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	if execute(t, h, http.MethodPut, "/photos", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}
	if execute(t, h, http.MethodPut, "/photos/a.txt", "a").StatusCode != http.StatusOK {
		t.Fatal("failed to put a.txt")
	}
	if execute(t, h, http.MethodPut, "/photos/b.txt", "b").StatusCode != http.StatusOK {
		t.Fatal("failed to put b.txt")
	}

	deleteBody := `<Delete><Object><Key>a.txt</Key></Object><Object><Key>b.txt</Key></Object></Delete>`
	resp := execute(t, h, http.MethodPost, "/photos?delete", deleteBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("multi-delete status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "<Key>a.txt</Key>") || !strings.Contains(body, "<Key>b.txt</Key>") {
		t.Fatalf("expected deleted keys in response, got %s", body)
	}
	if execute(t, h, http.MethodGet, "/photos/a.txt", "").StatusCode != http.StatusNotFound {
		t.Fatal("expected a.txt deleted")
	}

	corsBody := `<CORSConfiguration><CORSRule><AllowedOrigin>https://example.com</AllowedOrigin><AllowedMethod>GET</AllowedMethod><AllowedMethod>PUT</AllowedMethod><AllowedHeader>Authorization</AllowedHeader><ExposeHeader>ETag</ExposeHeader><MaxAgeSeconds>300</MaxAgeSeconds></CORSRule></CORSConfiguration>`
	resp = execute(t, h, http.MethodPut, "/photos?cors", corsBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put cors status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = execute(t, h, http.MethodGet, "/photos?cors", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(readBody(t, resp), "https://example.com") {
		t.Fatalf("expected cors configuration, status=%d", resp.StatusCode)
	}

	policy := `{"Version":"2012-10-17","Statement":{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::photos/*"}}`
	resp = execute(t, h, http.MethodPut, "/photos?policy", policy)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put policy status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = execute(t, h, http.MethodGet, "/photos?policy", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(readBody(t, resp), "s3:GetObject") {
		t.Fatalf("expected policy document, status=%d", resp.StatusCode)
	}

	block := `<PublicAccessBlockConfiguration><BlockPublicAcls>true</BlockPublicAcls><IgnorePublicAcls>true</IgnorePublicAcls><BlockPublicPolicy>true</BlockPublicPolicy><RestrictPublicBuckets>true</RestrictPublicBuckets></PublicAccessBlockConfiguration>`
	resp = execute(t, h, http.MethodPut, "/photos?publicAccessBlock", block)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put public access block status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = execute(t, h, http.MethodGet, "/photos?publicAccessBlock", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(readBody(t, resp), "<BlockPublicPolicy>true</BlockPublicPolicy>") {
		t.Fatalf("expected public access block configuration, status=%d", resp.StatusCode)
	}

	lifecycle := `<LifecycleConfiguration><Rule><ID>expire-logs</ID><Status>Enabled</Status><Filter><Prefix>logs/</Prefix></Filter><Expiration><Days>30</Days></Expiration></Rule></LifecycleConfiguration>`
	resp = execute(t, h, http.MethodPut, "/photos?lifecycle", lifecycle)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put lifecycle status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = execute(t, h, http.MethodGet, "/photos?lifecycle", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(readBody(t, resp), "expire-logs") {
		t.Fatalf("expected lifecycle configuration, status=%d", resp.StatusCode)
	}

	if execute(t, h, http.MethodPut, "/photos/tagged.txt", "tagged").StatusCode != http.StatusOK {
		t.Fatal("failed to put tagged object")
	}
	tagging := `<Tagging><TagSet><Tag><Key>project</Key><Value>s000</Value></Tag></TagSet></Tagging>`
	resp = execute(t, h, http.MethodPut, "/photos/tagged.txt?tagging", tagging)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put object tagging status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = execute(t, h, http.MethodGet, "/photos/tagged.txt?tagging", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(readBody(t, resp), "project") {
		t.Fatalf("expected object tagging, status=%d", resp.StatusCode)
	}
	resp = execute(t, h, http.MethodDelete, "/photos/tagged.txt?tagging", "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete object tagging status=%d", resp.StatusCode)
	}
}

func TestBucketNotificationConfigAndWebhookDelivery(t *testing.T) {
	t.Parallel()

	events := make(chan string, 2)
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		events <- string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(webhook.Close)

	h := newS3TestHandler(t)
	if execute(t, h, http.MethodPut, "/notify", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}
	cfg := `<NotificationConfiguration><QueueConfiguration><Id>webhook</Id><Endpoint>` + webhook.URL + `</Endpoint><Event>ObjectCreated:*</Event><Event>ObjectRemoved:*</Event></QueueConfiguration></NotificationConfiguration>`
	resp := execute(t, h, http.MethodPut, "/notify?notification", cfg)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put notification status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = execute(t, h, http.MethodGet, "/notify?notification", "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, webhook.URL) || !strings.Contains(body, "ObjectCreated:*") {
		t.Fatalf("expected notification config, status=%d body=%s", resp.StatusCode, body)
	}

	if execute(t, h, http.MethodPut, "/notify/file.txt", "payload").StatusCode != http.StatusOK {
		t.Fatal("failed to put notified object")
	}
	created := waitForNotification(t, events)
	if !strings.Contains(created, "ObjectCreated:Put") || !strings.Contains(created, "file.txt") || !strings.Contains(created, "notify") {
		t.Fatalf("unexpected created notification: %s", created)
	}

	if execute(t, h, http.MethodDelete, "/notify/file.txt", "").StatusCode != http.StatusNoContent {
		t.Fatal("failed to delete notified object")
	}
	removed := waitForNotification(t, events)
	if !strings.Contains(removed, "ObjectRemoved:Delete") || !strings.Contains(removed, "file.txt") {
		t.Fatalf("unexpected removed notification: %s", removed)
	}

	resp = execute(t, h, http.MethodDelete, "/notify?notification", "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete notification status=%d", resp.StatusCode)
	}
}

func TestBucketReplicationConfigAndAsyncDelivery(t *testing.T) {
	t.Parallel()

	replicated := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		replicated <- r.URL.Path + ":" + string(body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)

	h := newS3TestHandler(t)
	if execute(t, h, http.MethodPut, "/source", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create source bucket")
	}
	cfg := `<ReplicationConfiguration><Rule><ID>r1</ID><Status>Enabled</Status><Filter><Prefix>docs/</Prefix></Filter><Destination><Endpoint>` + target.URL + `</Endpoint><Bucket>dest</Bucket></Destination></Rule></ReplicationConfiguration>`
	resp := execute(t, h, http.MethodPut, "/source?replication", cfg)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put replication status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = execute(t, h, http.MethodGet, "/source?replication", "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, target.URL) || !strings.Contains(body, "dest") {
		t.Fatalf("expected replication config, status=%d body=%s", resp.StatusCode, body)
	}

	if execute(t, h, http.MethodPut, "/source/other.txt", "skip").StatusCode != http.StatusOK {
		t.Fatal("failed to put skipped object")
	}
	select {
	case got := <-replicated:
		t.Fatalf("unexpected replication for non-matching prefix: %s", got)
	case <-time.After(100 * time.Millisecond):
	}

	if execute(t, h, http.MethodPut, "/source/docs/file.txt", "replicated").StatusCode != http.StatusOK {
		t.Fatal("failed to put replicated object")
	}
	got := waitForNotification(t, replicated)
	if !strings.Contains(got, "/dest/docs/file.txt:replicated") {
		t.Fatalf("unexpected replicated request: %s", got)
	}

	resp = execute(t, h, http.MethodDelete, "/source?replication", "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete replication status=%d", resp.StatusCode)
	}
}

func TestListObjectVersionsAndDeleteSpecificVersion(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	if execute(t, h, http.MethodPut, "/photos", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}
	if execute(t, h, http.MethodPut, "/photos?versioning", `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`).StatusCode != http.StatusOK {
		t.Fatal("failed to enable versioning")
	}
	put1 := execute(t, h, http.MethodPut, "/photos/file.txt", "v1")
	if put1.StatusCode != http.StatusOK {
		t.Fatalf("put v1 status=%d", put1.StatusCode)
	}
	put2 := execute(t, h, http.MethodPut, "/photos/file.txt", "v2")
	if put2.StatusCode != http.StatusOK {
		t.Fatalf("put v2 status=%d", put2.StatusCode)
	}
	versions := execute(t, h, http.MethodGet, "/photos?versions", "")
	if versions.StatusCode != http.StatusOK {
		t.Fatalf("list versions status=%d body=%s", versions.StatusCode, readBody(t, versions))
	}
	body := readBody(t, versions)
	if strings.Count(body, "<Key>file.txt</Key>") != 2 || !strings.Contains(body, "<VersionId>") {
		t.Fatalf("expected two object versions, got %s", body)
	}
	var parsed struct {
		Versions []struct {
			Key       string `xml:"Key"`
			VersionID string `xml:"VersionId"`
			IsLatest  bool   `xml:"IsLatest"`
		} `xml:"Version"`
	}
	if err := xml.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("parse versions xml failed: %v", err)
	}
	var oldVersion string
	for _, version := range parsed.Versions {
		if version.Key == "file.txt" && !version.IsLatest {
			oldVersion = version.VersionID
		}
	}
	if oldVersion == "" {
		t.Fatalf("expected non-latest version in %s", body)
	}
	resp := execute(t, h, http.MethodDelete, "/photos/file.txt?versionId="+oldVersion, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete version status=%d", resp.StatusCode)
	}
	versions = execute(t, h, http.MethodGet, "/photos?versions", "")
	body = readBody(t, versions)
	if strings.Contains(body, oldVersion) {
		t.Fatalf("deleted version still listed: %s", body)
	}
	get := execute(t, h, http.MethodGet, "/photos/file.txt", "")
	if get.StatusCode != http.StatusOK || readBody(t, get) != "v2" {
		t.Fatalf("expected latest version to remain, status=%d", get.StatusCode)
	}
}

func TestObjectRetentionBlocksDeleteAndOverwrite(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	if execute(t, h, http.MethodPut, "/locked", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}
	if execute(t, h, http.MethodPut, "/locked/file.txt", "v1").StatusCode != http.StatusOK {
		t.Fatal("failed to put object")
	}
	retainUntil := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	retentionXML := `<Retention><Mode>COMPLIANCE</Mode><RetainUntilDate>` + retainUntil + `</RetainUntilDate></Retention>`
	resp := execute(t, h, http.MethodPut, "/locked/file.txt?retention", retentionXML)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put retention status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = execute(t, h, http.MethodGet, "/locked/file.txt?retention", "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "COMPLIANCE") || !strings.Contains(body, retainUntil) {
		t.Fatalf("expected retention XML, status=%d body=%s", resp.StatusCode, body)
	}
	resp = execute(t, h, http.MethodHead, "/locked/file.txt", "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("x-amz-object-lock-mode") != "COMPLIANCE" {
		t.Fatalf("expected retention headers, status=%d headers=%v", resp.StatusCode, resp.Header)
	}
	resp = execute(t, h, http.MethodDelete, "/locked/file.txt", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected retention delete block, status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = execute(t, h, http.MethodPut, "/locked/file.txt", "v2")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected retention overwrite block, status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = execute(t, h, http.MethodGet, "/locked/file.txt", "")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || body != "v1" {
		t.Fatalf("expected original object after blocked overwrite, status=%d body=%s", resp.StatusCode, body)
	}
}

func TestObjectLegalHoldBlocksSpecificVersionDelete(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	if execute(t, h, http.MethodPut, "/held", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}
	if execute(t, h, http.MethodPut, "/held?versioning", `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`).StatusCode != http.StatusOK {
		t.Fatal("failed to enable versioning")
	}
	put := execute(t, h, http.MethodPut, "/held/file.txt", "v1")
	if put.StatusCode != http.StatusOK {
		t.Fatalf("put version status=%d", put.StatusCode)
	}
	versions := execute(t, h, http.MethodGet, "/held?versions", "")
	var parsed struct {
		Versions []struct {
			Key       string `xml:"Key"`
			VersionID string `xml:"VersionId"`
		} `xml:"Version"`
	}
	if err := xml.Unmarshal([]byte(readBody(t, versions)), &parsed); err != nil {
		t.Fatalf("parse versions failed: %v", err)
	}
	versionID := parsed.Versions[0].VersionID
	resp := execute(t, h, http.MethodPut, "/held/file.txt?legal-hold&versionId="+versionID, `<LegalHold><Status>ON</Status></LegalHold>`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put legal hold status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = execute(t, h, http.MethodGet, "/held/file.txt?legal-hold&versionId="+versionID, "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "<Status>ON</Status>") {
		t.Fatalf("expected legal hold XML, status=%d body=%s", resp.StatusCode, body)
	}
	resp = execute(t, h, http.MethodDelete, "/held/file.txt?versionId="+versionID, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected legal hold delete block, status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = execute(t, h, http.MethodPut, "/held/file.txt?legal-hold&versionId="+versionID, `<LegalHold><Status>OFF</Status></LegalHold>`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear legal hold status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = execute(t, h, http.MethodDelete, "/held/file.txt?versionId="+versionID, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected delete after legal hold off, status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
}

func TestListObjectVersionsDeleteMarkersAndPagination(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	if execute(t, h, http.MethodPut, "/versioned-markers", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}
	if execute(t, h, http.MethodPut, "/versioned-markers?versioning", `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`).StatusCode != http.StatusOK {
		t.Fatal("failed to enable versioning")
	}
	if execute(t, h, http.MethodPut, "/versioned-markers/file.txt", "v1").StatusCode != http.StatusOK {
		t.Fatal("failed to put object")
	}
	if execute(t, h, http.MethodDelete, "/versioned-markers/file.txt", "").StatusCode != http.StatusNoContent {
		t.Fatal("failed to create delete marker")
	}

	resp := execute(t, h, http.MethodGet, "/versioned-markers?versions", "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "<DeleteMarker>") || !strings.Contains(body, "<IsLatest>true</IsLatest>") {
		t.Fatalf("expected latest delete marker in versions list, status=%d body=%s", resp.StatusCode, body)
	}

	resp = execute(t, h, http.MethodGet, "/versioned-markers?versions&max-keys=1", "")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "<IsTruncated>true</IsTruncated>") || !strings.Contains(body, "<NextVersionIdMarker>") {
		t.Fatalf("expected truncated versions page with version marker, status=%d body=%s", resp.StatusCode, body)
	}
	var page struct {
		NextKeyMarker       string `xml:"NextKeyMarker"`
		NextVersionIDMarker string `xml:"NextVersionIdMarker"`
	}
	if err := xml.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("parse page failed: %v", err)
	}
	if page.NextKeyMarker == "" || page.NextVersionIDMarker == "" {
		t.Fatalf("expected both next markers, got %#v", page)
	}
	resp = execute(t, h, http.MethodGet, "/versioned-markers?versions&max-keys=1&key-marker="+page.NextKeyMarker+"&version-id-marker="+page.NextVersionIDMarker, "")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || strings.Contains(body, "<IsTruncated>true</IsTruncated>") || !strings.Contains(body, "<Version>") {
		t.Fatalf("expected second page with object version, status=%d body=%s", resp.StatusCode, body)
	}
}

func TestNullVersionOverwriteAndDeleteSpecificNullVersion(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	if execute(t, h, http.MethodPut, "/null-versions", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}
	if execute(t, h, http.MethodPut, "/null-versions/file.txt", "v1").StatusCode != http.StatusOK {
		t.Fatal("failed to put first null version")
	}
	if execute(t, h, http.MethodPut, "/null-versions/file.txt", "v2").StatusCode != http.StatusOK {
		t.Fatal("failed to overwrite null version")
	}
	resp := execute(t, h, http.MethodGet, "/null-versions?versions", "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || strings.Count(body, "<VersionId>null</VersionId>") != 1 {
		t.Fatalf("expected one null version, status=%d body=%s", resp.StatusCode, body)
	}
	resp = execute(t, h, http.MethodGet, "/null-versions/file.txt?versionId=null", "")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || body != "v2" {
		t.Fatalf("expected latest null version content, status=%d body=%s", resp.StatusCode, body)
	}
	if execute(t, h, http.MethodDelete, "/null-versions/file.txt?versionId=null", "").StatusCode != http.StatusNoContent {
		t.Fatal("failed to delete null version")
	}
	resp = execute(t, h, http.MethodGet, "/null-versions/file.txt", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected deleted null version to disappear, status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
}

func TestConditionalGetHeadAndCopy(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	if execute(t, h, http.MethodPut, "/photos", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create photos bucket")
	}
	if execute(t, h, http.MethodPut, "/archive", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create archive bucket")
	}
	put := execute(t, h, http.MethodPut, "/photos/file.txt", "hello")
	if put.StatusCode != http.StatusOK {
		t.Fatalf("put object status=%d", put.StatusCode)
	}
	etag := put.Header.Get("ETag")

	req := httptest.NewRequest(http.MethodGet, "/photos/file.txt", nil)
	req.Header.Set("Authorization", "test")
	req.Header.Set("If-None-Match", etag)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusNotModified {
		t.Fatalf("expected if-none-match 304, got %d body=%s", rw.Code, rw.Body.String())
	}

	req = httptest.NewRequest(http.MethodHead, "/photos/file.txt", nil)
	req.Header.Set("Authorization", "test")
	req.Header.Set("If-Match", `"does-not-match"`)
	rw = httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected if-match 412, got %d body=%s", rw.Code, rw.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/photos/file.txt", nil)
	req.Header.Set("Authorization", "test")
	req.Header.Set("If-Modified-Since", time.Now().Add(24*time.Hour).UTC().Format(http.TimeFormat))
	rw = httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusNotModified {
		t.Fatalf("expected if-modified-since 304, got %d body=%s", rw.Code, rw.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/archive/copy.txt", nil)
	req.Header.Set("Authorization", "test")
	req.Header.Set("X-Amz-Copy-Source", "/photos/file.txt")
	req.Header.Set("X-Amz-Copy-Source-If-Match", `"does-not-match"`)
	rw = httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected copy source if-match 412, got %d body=%s", rw.Code, rw.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/archive/copy.txt", nil)
	req.Header.Set("Authorization", "test")
	req.Header.Set("X-Amz-Copy-Source", "/photos/file.txt")
	req.Header.Set("X-Amz-Copy-Source-If-Match", etag)
	rw = httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("expected copy source if-match success, got %d body=%s", rw.Code, rw.Body.String())
	}
}

func TestPresignedURLSupportsPutAndGetObject(t *testing.T) {
	t.Parallel()

	broot := t.TempDir()
	bstore, err := blob.NewStore(blob.Config{RootDir: broot, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}

	mstore, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:test.db"})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	if err := mstore.CreateBucket(context.Background(), metadata.Bucket{Name: "photos", CreatedAt: time.Now().UTC(), Region: "us-east-1", VersioningStatus: "Suspended"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}

	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	credentials := auth.NewCredentialStore(func() time.Time { return now })
	if err := credentials.CreateCredential("AKIDEXAMPLE", "very-secret"); err != nil {
		t.Fatalf("create credential failed: %v", err)
	}
	verifier := auth.NewVerifier(credentials, auth.VerifierOptions{Now: func() time.Time { return now }})

	h := NewHandler(Options{Verifier: verifier, Metadata: mstore, Blob: bstore, MaxInFlight: 128, HeavyOpsWorkers: 4, HeavyOpsQueue: 64, BucketRegion: "us-east-1"})

	putReq := httptest.NewRequest(http.MethodPut, "/photos/presigned.txt", strings.NewReader("hello from presign"))
	if err := auth.PresignRequest(putReq, "AKIDEXAMPLE", "very-secret", auth.PresignOptions{Now: func() time.Time { return now }, Region: "us-east-1", Service: "s3", Expires: 5 * time.Minute}); err != nil {
		t.Fatalf("presign put failed: %v", err)
	}
	putRR := httptest.NewRecorder()
	h.ServeHTTP(putRR, putReq)
	if putRR.Code != http.StatusOK {
		t.Fatalf("presigned put status=%d body=%s", putRR.Code, putRR.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/photos/presigned.txt", nil)
	if err := auth.PresignRequest(getReq, "AKIDEXAMPLE", "very-secret", auth.PresignOptions{Now: func() time.Time { return now }, Region: "us-east-1", Service: "s3", Expires: 5 * time.Minute}); err != nil {
		t.Fatalf("presign get failed: %v", err)
	}
	getRR := httptest.NewRecorder()
	h.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("presigned get status=%d body=%s", getRR.Code, getRR.Body.String())
	}
	if getRR.Body.String() != "hello from presign" {
		t.Fatalf("unexpected presigned get body: %q", getRR.Body.String())
	}
}

func TestAnonymousObjectReadAllowedByBucketPolicy(t *testing.T) {
	t.Parallel()

	h, store, bstore := newUnauthenticatedS3TestHandler(t)
	ctx := context.Background()
	seedPublicPolicyObject(t, ctx, store, bstore, "photos", "public/hello.txt", "hello public")
	if err := store.PutBucketPolicy(ctx, metadata.BucketPolicy{Bucket: "photos", Enabled: true, Document: `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::photos/public/*"}]}`}); err != nil {
		t.Fatalf("put bucket policy failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/photos/public/hello.txt", nil)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("expected anonymous get status 200, got %d body=%s", rw.Code, rw.Body.String())
	}
	if rw.Body.String() != "hello public" {
		t.Fatalf("unexpected anonymous body %q", rw.Body.String())
	}
}

func TestAnonymousListBucketAllowedByBucketPolicy(t *testing.T) {
	t.Parallel()

	h, store, bstore := newUnauthenticatedS3TestHandler(t)
	ctx := context.Background()
	seedPublicPolicyObject(t, ctx, store, bstore, "photos", "public/hello.txt", "hello public")
	if err := store.PutBucketPolicy(ctx, metadata.BucketPolicy{Bucket: "photos", Enabled: true, Document: `{"Version":"2012-10-17","Statement":{"Effect":"Allow","Principal":{"AWS":"*"},"Action":"s3:ListBucket","Resource":"arn:aws:s3:::photos"}}`}); err != nil {
		t.Fatalf("put bucket policy failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/photos?list-type=2", nil)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("expected anonymous list status 200, got %d body=%s", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), "public/hello.txt") {
		t.Fatalf("expected listed object in anonymous response, got %s", rw.Body.String())
	}
}

func TestAnonymousPublicPolicyBlockedByPublicAccessBlock(t *testing.T) {
	t.Parallel()

	h, store, bstore := newUnauthenticatedS3TestHandler(t)
	ctx := context.Background()
	seedPublicPolicyObject(t, ctx, store, bstore, "photos", "public/hello.txt", "hello public")
	if err := store.PutBucketPolicy(ctx, metadata.BucketPolicy{Bucket: "photos", Enabled: true, Document: `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::photos/public/*"}]}`}); err != nil {
		t.Fatalf("put bucket policy failed: %v", err)
	}
	if err := store.PutBucketPublicAccessBlock(ctx, metadata.BucketPublicAccessBlock{Bucket: "photos", BlockPublicPolicy: true}); err != nil {
		t.Fatalf("put public access block failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/photos/public/hello.txt", nil)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Fatalf("expected anonymous get status 403, got %d body=%s", rw.Code, rw.Body.String())
	}
}

func newS3TestHandler(t *testing.T) http.Handler {
	return newS3TestHandlerWithOptions(t, Options{})
}

func newUnauthenticatedS3TestHandler(t *testing.T) (http.Handler, metadata.Store, *blob.Store) {
	t.Helper()
	root := t.TempDir()
	bstore, err := blob.NewStore(blob.Config{RootDir: filepath.Join(root, "blobs"), FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	store, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:" + filepath.Join(root, "meta.db")})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}
	h := NewHandler(Options{Metadata: store, Blob: bstore, MaxInFlight: 128, HeavyOpsWorkers: 4, HeavyOpsQueue: 64, BucketRegion: "us-east-1"})
	return h, store, bstore
}

func seedPublicPolicyObject(t *testing.T, ctx context.Context, store metadata.Store, bstore *blob.Store, bucket, key, payload string) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.CreateBucket(ctx, metadata.Bucket{Name: bucket, CreatedAt: now, VersioningStatus: "Suspended", Region: "us-east-1"}); err != nil {
		t.Fatalf("create bucket failed: %v", err)
	}
	ref := blob.ObjectRef{Bucket: bucket, Key: key, VersionID: "null"}
	meta, err := bstore.WriteObject(ctx, ref, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("write object failed: %v", err)
	}
	if err := store.PutObjectVersion(ctx, metadata.ObjectVersion{Bucket: bucket, Key: key, VersionID: "null", Size: meta.Size, ETag: meta.MD5Hex, ChecksumSHA256: meta.SHA256, StoragePath: meta.Path, CreatedAt: now}); err != nil {
		t.Fatalf("put object metadata failed: %v", err)
	}
}

func newS3TestHandlerWithOptions(t *testing.T, overrides Options) http.Handler {
	t.Helper()

	broot := t.TempDir()
	bstore, err := blob.NewStore(blob.Config{RootDir: broot, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}

	mstore, err := metadata.NewStore(metadata.Config{Backend: metadata.BackendSQLite, DSN: "file:test.db"})
	if err != nil {
		t.Fatalf("new metadata store failed: %v", err)
	}

	opts := Options{
		Verifier:        allowAllVerifier{},
		Metadata:        mstore,
		Blob:            bstore,
		MaxInFlight:     128,
		HeavyOpsWorkers: 4,
		HeavyOpsQueue:   64,
		BucketRegion:    "us-east-1",
	}
	if overrides.Verifier != nil {
		opts.Verifier = overrides.Verifier
	}
	if overrides.MaxInFlight > 0 {
		opts.MaxInFlight = overrides.MaxInFlight
	}
	if overrides.HeavyOpsWorkers != 0 || overrides.HeavyOpsQueue != 0 {
		opts.HeavyOpsWorkers = overrides.HeavyOpsWorkers
		opts.HeavyOpsQueue = overrides.HeavyOpsQueue
	}
	opts.SSEMasterKey = overrides.SSEMasterKey
	opts.AuditEnabled = overrides.AuditEnabled
	opts.Audit = overrides.Audit
	return NewHandler(opts)
}

func execute(t *testing.T, h http.Handler, method, target, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "test")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	return rw.Result()
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	return string(b)
}

func checksumSHA1B64(value string) string {
	sum := sha1.Sum([]byte(value))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func checksumCRC32B64(value string) string {
	sum := crc32.ChecksumIEEE([]byte(value))
	return base64.StdEncoding.EncodeToString([]byte{byte(sum >> 24), byte(sum >> 16), byte(sum >> 8), byte(sum)})
}

func waitForNotification(t *testing.T, events <-chan string) string {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for notification")
		return ""
	}
}

type listObjectsV2Result struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	Name                  string   `xml:"Name"`
	KeyCount              int      `xml:"KeyCount"`
	ContinuationToken     string   `xml:"ContinuationToken"`
	NextContinuationToken string   `xml:"NextContinuationToken"`
	Contents              []struct {
		Key string `xml:"Key"`
	} `xml:"Contents"`
	CommonPrefixes []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
}

func TestListObjectsResponseXMLShape(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	_ = execute(t, h, http.MethodPut, "/photos", "")
	_ = execute(t, h, http.MethodPut, "/photos/alpha.txt", "a")

	resp := execute(t, h, http.MethodGet, "/photos?list-type=2", "")
	body := readBody(t, resp)
	var result listObjectsV2Result
	if err := xml.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("xml decode failed: %v body=%s", err, body)
	}
	if result.Name != "photos" {
		t.Fatalf("expected bucket name photos, got %q", result.Name)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected at least one content item")
	}
}

func TestListObjectsV2WithDelimiterReturnsCommonPrefixes(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	_ = execute(t, h, http.MethodPut, "/photos", "")
	_ = execute(t, h, http.MethodPut, "/photos/a/1.txt", "1")
	_ = execute(t, h, http.MethodPut, "/photos/a/2.txt", "2")
	_ = execute(t, h, http.MethodPut, "/photos/b/3.txt", "3")
	_ = execute(t, h, http.MethodPut, "/photos/root.txt", "r")

	resp := execute(t, h, http.MethodGet, "/photos?list-type=2&delimiter=/", "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status=%d body=%s", resp.StatusCode, body)
	}

	var result listObjectsV2Result
	if err := xml.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("xml decode failed: %v body=%s", err, body)
	}

	if got, want := len(result.CommonPrefixes), 2; got != want {
		t.Fatalf("common prefixes count = %d, want %d body=%s", got, want, body)
	}
	if result.CommonPrefixes[0].Prefix != "a/" {
		t.Fatalf("first common prefix = %q", result.CommonPrefixes[0].Prefix)
	}
	if result.CommonPrefixes[1].Prefix != "b/" {
		t.Fatalf("second common prefix = %q", result.CommonPrefixes[1].Prefix)
	}
	if got, want := len(result.Contents), 1; got != want {
		t.Fatalf("contents count = %d, want %d body=%s", got, want, body)
	}
	if result.Contents[0].Key != "root.txt" {
		t.Fatalf("content key = %q, want root.txt", result.Contents[0].Key)
	}
	if result.KeyCount != 3 {
		t.Fatalf("key count = %d, want 3", result.KeyCount)
	}
}

func TestListObjectsV2PaginationUsesOpaqueContinuationToken(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	_ = execute(t, h, http.MethodPut, "/photos", "")
	_ = execute(t, h, http.MethodPut, "/photos/a/1.txt", "1")
	_ = execute(t, h, http.MethodPut, "/photos/b/1.txt", "1")
	_ = execute(t, h, http.MethodPut, "/photos/root.txt", "r")

	resp := execute(t, h, http.MethodGet, "/photos?list-type=2&delimiter=/&max-keys=2", "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list page1 status=%d body=%s", resp.StatusCode, body)
	}

	var page1 listObjectsV2Result
	if err := xml.Unmarshal([]byte(body), &page1); err != nil {
		t.Fatalf("xml decode failed: %v body=%s", err, body)
	}
	if page1.NextContinuationToken == "" {
		t.Fatalf("expected next continuation token body=%s", body)
	}
	if strings.Contains(page1.NextContinuationToken, "root.txt") {
		t.Fatalf("expected opaque token, got %q", page1.NextContinuationToken)
	}

	resp = execute(t, h, http.MethodGet, "/photos?list-type=2&delimiter=/&max-keys=2&continuation-token="+page1.NextContinuationToken, "")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list page2 status=%d body=%s", resp.StatusCode, body)
	}

	var page2 listObjectsV2Result
	if err := xml.Unmarshal([]byte(body), &page2); err != nil {
		t.Fatalf("xml decode failed: %v body=%s", err, body)
	}
	if len(page2.Contents) != 1 || page2.Contents[0].Key != "root.txt" {
		t.Fatalf("unexpected page2 contents body=%s", body)
	}
}

func TestListObjectsV2InvalidContinuationToken(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	_ = execute(t, h, http.MethodPut, "/photos", "")
	_ = execute(t, h, http.MethodPut, "/photos/alpha.txt", "a")

	resp := execute(t, h, http.MethodGet, "/photos?list-type=2&continuation-token=invalid-token", "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", resp.StatusCode, body)
	}
	var e s3ErrorBody
	if err := xml.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("xml decode error response failed: %v body=%s", err, body)
	}
	if e.Code != "InvalidArgument" {
		t.Fatalf("error code=%q want InvalidArgument body=%s", e.Code, body)
	}
}

func TestContentMD5ValidationFailure(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	_ = execute(t, h, http.MethodPut, "/photos", "")

	req := httptest.NewRequest(http.MethodPut, "/photos/hello.txt", strings.NewReader("hello"))
	req.Header.Set("Authorization", "test")
	req.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString([]byte("not-md5")))
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("expected md5 validation failure status 400, got %d", rw.Code)
	}
}

func TestMainFlowCompatibleWithFilesystemBackedBlob(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	_ = execute(t, h, http.MethodPut, "/photos", "")
	_ = execute(t, h, http.MethodPut, "/photos/file.bin", "payload")

	resp := execute(t, h, http.MethodGet, "/photos/file.bin", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get object status = %d", resp.StatusCode)
	}
	if got := readBody(t, resp); got != "payload" {
		t.Fatalf("expected payload, got %q", got)
	}
}

func TestSetupDoesNotLeakTempFiles(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	_, err := blob.NewStore(blob.Config{RootDir: tmp, FsyncMode: blob.FsyncFast})
	if err != nil {
		t.Fatalf("new blob store failed: %v", err)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read dir failed: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected initialized blob store directories")
	}
}

func TestMultipartUploadFlow(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	_ = execute(t, h, http.MethodPut, "/photos", "")

	resp := execute(t, h, http.MethodPost, "/photos/video.mp4?uploads", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create multipart upload status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	body := readBody(t, resp)
	uploadID := extractXMLValue(body, "UploadId")
	if uploadID == "" {
		t.Fatalf("missing upload id in body: %s", body)
	}

	resp = execute(t, h, http.MethodGet, "/photos?uploads", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list multipart uploads status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if !strings.Contains(readBody(t, resp), uploadID) {
		t.Fatal("expected upload id in ListMultipartUploads response")
	}

	part1Payload := strings.Repeat("a", minMultipartPartSize)
	resp = execute(t, h, http.MethodPut, "/photos/video.mp4?uploadId="+uploadID+"&partNumber=1", part1Payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload part 1 status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if resp.Header.Get("x-amz-checksum-sha256") == "" || resp.Header.Get("x-amz-checksum-crc32c") == "" {
		t.Fatalf("expected upload part checksum headers, got %v", resp.Header)
	}
	part1ETag := strings.Trim(resp.Header.Get("ETag"), "\"")

	resp = execute(t, h, http.MethodPut, "/photos/video.mp4?uploadId="+uploadID+"&partNumber=2", "world")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload part 2 status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	part2ETag := strings.Trim(resp.Header.Get("ETag"), "\"")

	resp = execute(t, h, http.MethodGet, "/photos/video.mp4?uploadId="+uploadID, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list parts status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	partsBody := readBody(t, resp)
	if !strings.Contains(partsBody, "<PartNumber>1</PartNumber>") || !strings.Contains(partsBody, "<PartNumber>2</PartNumber>") || !strings.Contains(partsBody, "<ChecksumSHA256>") {
		t.Fatalf("expected both parts in list response: %s", partsBody)
	}

	completeXML := `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"` + part1ETag + `"</ETag></Part><Part><PartNumber>2</PartNumber><ETag>"` + part2ETag + `"</ETag></Part></CompleteMultipartUpload>`
	resp = execute(t, h, http.MethodPost, "/photos/video.mp4?uploadId="+uploadID, completeXML)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete multipart upload status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	completeBody := readBody(t, resp)
	if resp.Header.Get("x-amz-checksum-sha256") == "" || !strings.Contains(completeBody, "<ChecksumCRC32>") || !strings.Contains(completeBody, "<ChecksumSHA256>") {
		t.Fatalf("expected complete multipart checksum response, headers=%v body=%s", resp.Header, completeBody)
	}

	resp = execute(t, h, http.MethodGet, "/photos/video.mp4", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get completed object status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if got := readBody(t, resp); got != part1Payload+"world" {
		t.Fatalf("expected completed payload length %d, got %d", len(part1Payload)+len("world"), len(got))
	}
}

func TestMultipartUploadPartChecksumValidation(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	_ = execute(t, h, http.MethodPut, "/photos", "")
	resp := execute(t, h, http.MethodPost, "/photos/checksum.bin?uploads", "")
	uploadID := extractXMLValue(readBody(t, resp), "UploadId")
	if uploadID == "" {
		t.Fatal("expected upload id")
	}

	req := httptest.NewRequest(http.MethodPut, "/photos/checksum.bin?uploadId="+uploadID+"&partNumber=1", strings.NewReader("checksum-part"))
	req.Header.Set("Authorization", "test")
	req.Header.Set("x-amz-checksum-sha1", checksumSHA1B64("checksum-part"))
	req.Header.Set("x-amz-checksum-crc32", checksumCRC32B64("checksum-part"))
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("expected checksum-valid upload part, got %d body=%s", rw.Code, rw.Body.String())
	}
	if rw.Header().Get("x-amz-checksum-sha1") == "" || rw.Header().Get("x-amz-checksum-crc32") == "" {
		t.Fatalf("expected checksum headers, got %v", rw.Header())
	}

	resp = execute(t, h, http.MethodPost, "/photos/bad-checksum.bin?uploads", "")
	uploadID = extractXMLValue(readBody(t, resp), "UploadId")
	req = httptest.NewRequest(http.MethodPut, "/photos/bad-checksum.bin?uploadId="+uploadID+"&partNumber=1", strings.NewReader("checksum-part"))
	req.Header.Set("Authorization", "test")
	req.Header.Set("x-amz-checksum-sha256", "bad-checksum")
	rw = httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusBadRequest || !strings.Contains(rw.Body.String(), "BadDigest") {
		t.Fatalf("expected checksum mismatch BadDigest, got %d body=%s", rw.Code, rw.Body.String())
	}
}

func TestMultipartPaginationAndSizeLimits(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	_ = execute(t, h, http.MethodPut, "/photos", "")
	resp := execute(t, h, http.MethodPost, "/photos/paged.bin?uploads", "")
	uploadID := extractXMLValue(readBody(t, resp), "UploadId")
	if uploadID == "" {
		t.Fatal("expected upload id")
	}

	resp = execute(t, h, http.MethodPut, "/photos/paged.bin?uploadId="+uploadID+"&partNumber=1", "small")
	etag1 := strings.Trim(resp.Header.Get("ETag"), "\"")
	resp = execute(t, h, http.MethodPut, "/photos/paged.bin?uploadId="+uploadID+"&partNumber=2", "final")
	etag2 := strings.Trim(resp.Header.Get("ETag"), "\"")

	resp = execute(t, h, http.MethodGet, "/photos/paged.bin?uploadId="+uploadID+"&max-parts=1", "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "<IsTruncated>true</IsTruncated>") || !strings.Contains(body, "<NextPartNumberMarker>1</NextPartNumberMarker>") {
		t.Fatalf("expected paginated list parts response, status=%d body=%s", resp.StatusCode, body)
	}
	resp = execute(t, h, http.MethodGet, "/photos/paged.bin?uploadId="+uploadID+"&part-number-marker=1&max-parts=1", "")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "<PartNumber>2</PartNumber>") {
		t.Fatalf("expected second list parts page, status=%d body=%s", resp.StatusCode, body)
	}

	completeXML := `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"` + etag1 + `"</ETag></Part><Part><PartNumber>2</PartNumber><ETag>"` + etag2 + `"</ETag></Part></CompleteMultipartUpload>`
	resp = execute(t, h, http.MethodPost, "/photos/paged.bin?uploadId="+uploadID, completeXML)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(readBody(t, resp), "EntityTooSmall") {
		t.Fatalf("expected EntityTooSmall complete failure, got status=%d", resp.StatusCode)
	}

	resp = execute(t, h, http.MethodPut, "/photos/paged.bin?uploadId="+uploadID+"&partNumber=10001", "bad")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected invalid part number status 400, got %d", resp.StatusCode)
	}
}

func TestACLCompatibilityRoutes(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	if execute(t, h, http.MethodPut, "/photos", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}
	if execute(t, h, http.MethodPut, "/photos/file.txt", "data").StatusCode != http.StatusOK {
		t.Fatal("failed to put object")
	}

	resp := execute(t, h, http.MethodGet, "/photos?acl", "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "FULL_CONTROL") || !strings.Contains(body, "AccessControlPolicy") {
		t.Fatalf("expected bucket ACL XML, status=%d body=%s", resp.StatusCode, body)
	}

	resp = execute(t, h, http.MethodGet, "/photos/file.txt?acl", "")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "FULL_CONTROL") {
		t.Fatalf("expected object ACL XML, status=%d body=%s", resp.StatusCode, body)
	}

	req := httptest.NewRequest(http.MethodPut, "/photos/file.txt?acl", nil)
	req.Header.Set("Authorization", "test")
	req.Header.Set("x-amz-acl", "public-read")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("expected canned ACL no-op success, got %d body=%s", rw.Code, rw.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/photos/file.txt?acl", nil)
	req.Header.Set("Authorization", "test")
	req.Header.Set("x-amz-acl", "unsupported-acl")
	rw = httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusNotImplemented {
		t.Fatalf("expected unsupported ACL 501, got %d body=%s", rw.Code, rw.Body.String())
	}
}

func TestSSES3SinglePartPutGetHeadAndRange(t *testing.T) {
	t.Parallel()

	h := newS3TestHandlerWithOptions(t, Options{SSEMasterKey: []byte("0123456789abcdef0123456789abcdef")})
	if execute(t, h, http.MethodPut, "/photos", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}

	req := httptest.NewRequest(http.MethodPut, "/photos/secret.txt", strings.NewReader("hello encrypted world"))
	req.Header.Set("Authorization", "test")
	req.Header.Set("x-amz-server-side-encryption", "AES256")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("put sse object status=%d body=%s", rw.Code, rw.Body.String())
	}
	if got := rw.Header().Get("x-amz-server-side-encryption"); got != "AES256" {
		t.Fatalf("expected put SSE header AES256, got %q", got)
	}

	resp := execute(t, h, http.MethodGet, "/photos/secret.txt", "")
	if resp.StatusCode != http.StatusOK || readBody(t, resp) != "hello encrypted world" {
		t.Fatalf("expected decrypted get, status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if got := resp.Header.Get("x-amz-server-side-encryption"); got != "AES256" {
		t.Fatalf("expected get SSE header AES256, got %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/photos/secret.txt", nil)
	req.Header.Set("Authorization", "test")
	req.Header.Set("Range", "bytes=6-14")
	rw = httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusPartialContent || rw.Body.String() != "encrypted" {
		t.Fatalf("expected decrypted range, status=%d body=%s", rw.Code, rw.Body.String())
	}

	req = httptest.NewRequest(http.MethodHead, "/photos/secret.txt", nil)
	req.Header.Set("Authorization", "test")
	rw = httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK || rw.Header().Get("x-amz-server-side-encryption") != "AES256" {
		t.Fatalf("expected head SSE header, status=%d headers=%v", rw.Code, rw.Header())
	}
}

func TestSSES3RejectsUnsupportedOrUnconfigured(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	if execute(t, h, http.MethodPut, "/photos", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}
	req := httptest.NewRequest(http.MethodPut, "/photos/secret.txt", strings.NewReader("data"))
	req.Header.Set("Authorization", "test")
	req.Header.Set("x-amz-server-side-encryption", "AES256")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusBadRequest || !strings.Contains(rw.Body.String(), "SSE-S3 is not configured") {
		t.Fatalf("expected unconfigured SSE rejection, status=%d body=%s", rw.Code, rw.Body.String())
	}

	h = newS3TestHandlerWithOptions(t, Options{SSEMasterKey: []byte("0123456789abcdef0123456789abcdef")})
	if execute(t, h, http.MethodPut, "/photos", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}
	req = httptest.NewRequest(http.MethodPut, "/photos/secret.txt", strings.NewReader("data"))
	req.Header.Set("Authorization", "test")
	req.Header.Set("x-amz-server-side-encryption", "aws:kms")
	rw = httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusBadRequest || !strings.Contains(rw.Body.String(), "Unsupported server-side encryption") {
		t.Fatalf("expected unsupported SSE rejection, status=%d body=%s", rw.Code, rw.Body.String())
	}
}

func TestSSES3MultipartUpload(t *testing.T) {
	t.Parallel()

	h := newS3TestHandlerWithOptions(t, Options{SSEMasterKey: []byte("0123456789abcdef0123456789abcdef")})
	if execute(t, h, http.MethodPut, "/photos", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}
	req := httptest.NewRequest(http.MethodPost, "/photos/large.txt?uploads", nil)
	req.Header.Set("Authorization", "test")
	req.Header.Set("x-amz-server-side-encryption", "AES256")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK || rw.Header().Get("x-amz-server-side-encryption") != "AES256" {
		t.Fatalf("expected SSE multipart initiate success, status=%d headers=%v body=%s", rw.Code, rw.Header(), rw.Body.String())
	}
	uploadID := extractXMLValue(rw.Body.String(), "UploadId")
	if uploadID == "" {
		t.Fatalf("missing upload id: %s", rw.Body.String())
	}

	part1 := strings.Repeat("s", minMultipartPartSize)
	resp := execute(t, h, http.MethodPut, "/photos/large.txt?uploadId="+uploadID+"&partNumber=1", part1)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload part 1 status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	part1ETag := strings.Trim(resp.Header.Get("ETag"), "\"")
	resp = execute(t, h, http.MethodPut, "/photos/large.txt?uploadId="+uploadID+"&partNumber=2", "tail")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload part 2 status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	part2ETag := strings.Trim(resp.Header.Get("ETag"), "\"")

	completeXML := `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"` + part1ETag + `"</ETag></Part><Part><PartNumber>2</PartNumber><ETag>"` + part2ETag + `"</ETag></Part></CompleteMultipartUpload>`
	resp = execute(t, h, http.MethodPost, "/photos/large.txt?uploadId="+uploadID, completeXML)
	completeBody := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("x-amz-server-side-encryption") != "AES256" {
		t.Fatalf("expected SSE multipart complete success, status=%d headers=%v body=%s", resp.StatusCode, resp.Header, completeBody)
	}
	if !strings.Contains(completeBody, "<ChecksumSHA256>") {
		t.Fatalf("expected checksum in complete response: %s", completeBody)
	}

	resp = execute(t, h, http.MethodGet, "/photos/large.txt", "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("x-amz-server-side-encryption") != "AES256" || body != part1+"tail" {
		t.Fatalf("expected decrypted SSE multipart object, status=%d headers=%v body length=%d", resp.StatusCode, resp.Header, len(body))
	}
}

func TestListMultipartUploadsPagination(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	_ = execute(t, h, http.MethodPut, "/photos", "")
	resp1 := execute(t, h, http.MethodPost, "/photos/a.bin?uploads", "")
	upload1 := extractXMLValue(readBody(t, resp1), "UploadId")
	resp2 := execute(t, h, http.MethodPost, "/photos/b.bin?uploads", "")
	upload2 := extractXMLValue(readBody(t, resp2), "UploadId")

	resp := execute(t, h, http.MethodGet, "/photos?uploads&max-uploads=1", "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "<IsTruncated>true</IsTruncated>") || !strings.Contains(body, upload1) {
		t.Fatalf("expected first uploads page, status=%d body=%s", resp.StatusCode, body)
	}
	resp = execute(t, h, http.MethodGet, "/photos?uploads&key-marker=a.bin&upload-id-marker="+upload1+"&max-uploads=1", "")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "<UploadId>"+upload2+"</UploadId>") || strings.Contains(body, "<UploadId>"+upload1+"</UploadId>") {
		t.Fatalf("expected second uploads page, status=%d body=%s", resp.StatusCode, body)
	}
}

func TestAbortMultipartUpload(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	_ = execute(t, h, http.MethodPut, "/photos", "")
	resp := execute(t, h, http.MethodPost, "/photos/abort.bin?uploads", "")
	uploadID := extractXMLValue(readBody(t, resp), "UploadId")

	resp = execute(t, h, http.MethodDelete, "/photos/abort.bin?uploadId="+uploadID, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("abort multipart upload status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}

	resp = execute(t, h, http.MethodGet, "/photos?uploads", "")
	if strings.Contains(readBody(t, resp), uploadID) {
		t.Fatal("expected aborted upload to be absent from list")
	}
}

func TestCompleteMultipartRequiresOrderedMatchingParts(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	_ = execute(t, h, http.MethodPut, "/photos", "")
	resp := execute(t, h, http.MethodPost, "/photos/order.bin?uploads", "")
	uploadID := extractXMLValue(readBody(t, resp), "UploadId")

	resp = execute(t, h, http.MethodPut, "/photos/order.bin?uploadId="+uploadID+"&partNumber=2", "world")
	etag2 := strings.Trim(resp.Header.Get("ETag"), "\"")
	resp = execute(t, h, http.MethodPut, "/photos/order.bin?uploadId="+uploadID+"&partNumber=1", "hello ")
	etag1 := strings.Trim(resp.Header.Get("ETag"), "\"")

	badOrder := `<CompleteMultipartUpload><Part><PartNumber>2</PartNumber><ETag>"` + etag2 + `"</ETag></Part><Part><PartNumber>1</PartNumber><ETag>"` + etag1 + `"</ETag></Part></CompleteMultipartUpload>`
	resp = execute(t, h, http.MethodPost, "/photos/order.bin?uploadId="+uploadID, badOrder)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected bad request for out-of-order parts, got %d", resp.StatusCode)
	}
}

func TestS3ErrorCodeParityCommonFailures(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)

	cases := []struct {
		name       string
		setup      func()
		method     string
		target     string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "missing bucket",
			method:     http.MethodGet,
			target:     "/no-bucket?location",
			wantStatus: http.StatusNotFound,
			wantCode:   "NoSuchBucket",
		},
		{
			name: "missing key",
			setup: func() {
				_ = execute(t, h, http.MethodPut, "/photos", "")
			},
			method:     http.MethodGet,
			target:     "/photos/missing.txt",
			wantStatus: http.StatusNotFound,
			wantCode:   "NoSuchKey",
		},
		{
			name: "bucket not empty",
			setup: func() {
				_ = execute(t, h, http.MethodPut, "/filled", "")
				_ = execute(t, h, http.MethodPut, "/filled/file.txt", "x")
			},
			method:     http.MethodDelete,
			target:     "/filled",
			wantStatus: http.StatusConflict,
			wantCode:   "BucketNotEmpty",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup()
			}
			resp := execute(t, h, tc.method, tc.target, tc.body)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, resp.StatusCode)
			}
			var out s3ErrorBody
			if err := xml.Unmarshal([]byte(readBody(t, resp)), &out); err != nil {
				t.Fatalf("decode s3 error xml failed: %v", err)
			}
			if out.Code != tc.wantCode {
				t.Fatalf("expected error code %q, got %q", tc.wantCode, out.Code)
			}
		})
	}
}

func TestBucketWebsiteConfigurationLifecycle(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	if execute(t, h, http.MethodPut, "/site", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}

	putBody := `<WebsiteConfiguration><IndexDocument><Suffix>index.html</Suffix></IndexDocument><ErrorDocument><Key>error.html</Key></ErrorDocument><RoutingRules><RoutingRule><Condition><KeyPrefixEquals>docs/</KeyPrefixEquals></Condition><Redirect><ReplaceKeyPrefixWith>documents/</ReplaceKeyPrefixWith><HttpRedirectCode>302</HttpRedirectCode></Redirect></RoutingRule></RoutingRules></WebsiteConfiguration>`
	put := execute(t, h, http.MethodPut, "/site?website", putBody)
	if put.StatusCode != http.StatusOK {
		t.Fatalf("expected put website status 200, got %d body=%s", put.StatusCode, readBody(t, put))
	}

	get := execute(t, h, http.MethodGet, "/site?website", "")
	if get.StatusCode != http.StatusOK {
		t.Fatalf("expected get website status 200, got %d body=%s", get.StatusCode, readBody(t, get))
	}
	body := readBody(t, get)
	if !strings.Contains(body, "<Suffix>index.html</Suffix>") || !strings.Contains(body, "<Key>error.html</Key>") || !strings.Contains(body, "<RoutingRules>") || !strings.Contains(body, "<ReplaceKeyPrefixWith>documents/</ReplaceKeyPrefixWith>") {
		t.Fatalf("expected website xml payload, got %s", body)
	}

	del := execute(t, h, http.MethodDelete, "/site?website", "")
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("expected delete website status 204, got %d", del.StatusCode)
	}

	after := execute(t, h, http.MethodGet, "/site?website", "")
	if after.StatusCode != http.StatusNotFound {
		t.Fatalf("expected get after delete status 404, got %d", after.StatusCode)
	}
	if !strings.Contains(readBody(t, after), "NoSuchWebsiteConfiguration") {
		t.Fatalf("expected NoSuchWebsiteConfiguration after delete, got %s", readBody(t, after))
	}
}

func TestPutBucketWebsiteRejectsConflictingReplaceKeyFields(t *testing.T) {
	t.Parallel()

	h := newS3TestHandler(t)
	if execute(t, h, http.MethodPut, "/site", "").StatusCode != http.StatusOK {
		t.Fatal("failed to create bucket")
	}

	putBody := `<WebsiteConfiguration><IndexDocument><Suffix>index.html</Suffix></IndexDocument><RoutingRules><RoutingRule><Redirect><ReplaceKeyPrefixWith>docs/</ReplaceKeyPrefixWith><ReplaceKeyWith>index.html</ReplaceKeyWith></Redirect></RoutingRule></RoutingRules></WebsiteConfiguration>`
	resp := execute(t, h, http.MethodPut, "/site?website", putBody)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected put website status 400, got %d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "InvalidRequest") {
		t.Fatalf("expected InvalidRequest body, got %s", body)
	}
}

func extractXMLValue(body string, key string) string {
	open := "<" + key + ">"
	close := "</" + key + ">"
	start := strings.Index(body, open)
	if start < 0 {
		return ""
	}
	start += len(open)
	end := strings.Index(body[start:], close)
	if end < 0 {
		return ""
	}
	return body[start : start+end]
}

type blockingReader struct {
	gate    chan struct{}
	payload []byte
	once    bool
}

func (b *blockingReader) Read(p []byte) (int, error) {
	if !b.once {
		<-b.gate
		b.once = true
	}
	if len(b.payload) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.payload)
	b.payload = b.payload[n:]
	return n, nil
}
