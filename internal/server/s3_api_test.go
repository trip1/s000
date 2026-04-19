package server

import (
	"encoding/base64"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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

func newS3TestHandler(t *testing.T) http.Handler {
	return newS3TestHandlerWithOptions(t, Options{})
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

	resp = execute(t, h, http.MethodPut, "/photos/video.mp4?uploadId="+uploadID+"&partNumber=1", "hello ")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload part 1 status=%d body=%s", resp.StatusCode, readBody(t, resp))
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
	if !strings.Contains(partsBody, "<PartNumber>1</PartNumber>") || !strings.Contains(partsBody, "<PartNumber>2</PartNumber>") {
		t.Fatalf("expected both parts in list response: %s", partsBody)
	}

	completeXML := `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"` + part1ETag + `"</ETag></Part><Part><PartNumber>2</PartNumber><ETag>"` + part2ETag + `"</ETag></Part></CompleteMultipartUpload>`
	resp = execute(t, h, http.MethodPost, "/photos/video.mp4?uploadId="+uploadID, completeXML)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete multipart upload status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}

	resp = execute(t, h, http.MethodGet, "/photos/video.mp4", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get completed object status=%d body=%s", resp.StatusCode, readBody(t, resp))
	}
	if got := readBody(t, resp); got != "hello world" {
		t.Fatalf("expected completed payload %q, got %q", "hello world", got)
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
