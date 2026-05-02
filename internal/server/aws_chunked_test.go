package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRequestObjectBodyDecodesAWSChunkedTrailers(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{
		"5;chunk-signature=abc",
		"hello",
		"6;chunk-signature=def",
		" world",
		"0;chunk-signature=final",
		"x-amz-checksum-crc32: deadbeef",
		"",
		"",
	}, "\r\n")
	req, err := http.NewRequest(http.MethodPut, "/bucket/key", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Content-Encoding", "aws-chunked")

	decoded, err := io.ReadAll(requestObjectBody(req))
	if err != nil {
		t.Fatalf("read decoded body failed: %v", err)
	}
	if got, want := string(decoded), "hello world"; got != want {
		t.Fatalf("decoded body = %q, want %q", got, want)
	}
}

func TestRequestObjectBodyPassesPlainBodyThrough(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequest(http.MethodPut, "/bucket/key", strings.NewReader("plain"))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	decoded, err := io.ReadAll(requestObjectBody(req))
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	if got, want := string(decoded), "plain"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}
