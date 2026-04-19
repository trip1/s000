package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ds9labs.com/s000/internal/auth"
)

func TestRunHelpIncludesCommandSurface(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	var errOut bytes.Buffer
	exit := run([]string{"help"}, &out, &errOut)
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d (%s)", exit, errOut.String())
	}
	text := out.String()
	for _, command := range []string{"backup-create", "restore-validate", "health-inspect", "token-create", "put-object", "completion"} {
		if !strings.Contains(text, command) {
			t.Fatalf("expected help output to include %q, got %q", command, text)
		}
	}
}

func TestRunUnknownCommandReturnsUsageError(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	var errOut bytes.Buffer
	exit := run([]string{"unknown-cmd"}, &out, &errOut)
	if exit != 2 {
		t.Fatalf("expected exit 2, got %d", exit)
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("expected unknown command error, got %q", errOut.String())
	}
}

func TestCompletionBashOutput(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	var errOut bytes.Buffer
	exit := run([]string{"completion", "--shell", "bash"}, &out, &errOut)
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d (%s)", exit, errOut.String())
	}
	if !strings.Contains(out.String(), "complete -W") {
		t.Fatalf("expected bash completion output, got %q", out.String())
	}
}

func TestSubcommandHelpTextCoverage(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	var errOut bytes.Buffer
	exit := run([]string{"backup-create", "--help"}, &out, &errOut)
	if exit != 0 {
		t.Fatalf("expected help exit 0, got %d", exit)
	}
	if !strings.Contains(errOut.String(), "data-dir") || !strings.Contains(errOut.String(), "metadata-dsn") {
		t.Fatalf("expected backup-create help flags, got %q", errOut.String())
	}
}

func TestHealthInspectSuccess(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	var out bytes.Buffer
	var errOut bytes.Buffer
	exit := run([]string{"health-inspect", "--endpoint", ts.URL}, &out, &errOut)
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d (%s)", exit, errOut.String())
	}
	if !strings.Contains(out.String(), "health: ok") || !strings.Contains(out.String(), "ready: ok") {
		t.Fatalf("expected successful health output, got %q", out.String())
	}
}

func TestHealthInspectFailure(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/readyz":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	var out bytes.Buffer
	var errOut bytes.Buffer
	exit := run([]string{"health-inspect", "--endpoint", ts.URL}, &out, &errOut)
	if exit != 1 {
		t.Fatalf("expected exit 1, got %d", exit)
	}
	if !strings.Contains(errOut.String(), "readyz") {
		t.Fatalf("expected readiness failure in stderr, got %q", errOut.String())
	}
}

func TestBackupCreateAndRestoreValidateWorkflow(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "objects"), 0o755); err != nil {
		t.Fatalf("mkdir data objects failed: %v", err)
	}
	metadataPath := filepath.Join(dataDir, "s000-metadata.db")
	if err := os.WriteFile(metadataPath, []byte("sqlite"), 0o600); err != nil {
		t.Fatalf("write metadata db failed: %v", err)
	}

	backupDir := filepath.Join(root, "backup")
	var out bytes.Buffer
	var errOut bytes.Buffer
	exit := run([]string{"backup-create", "--data-dir", dataDir, "--metadata-dsn", fmt.Sprintf("file:%s", metadataPath), "--out", backupDir}, &out, &errOut)
	if exit != 0 {
		t.Fatalf("backup-create expected exit 0, got %d (%s)", exit, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	exit = run([]string{"restore-validate", "--path", backupDir}, &out, &errOut)
	if exit != 0 {
		t.Fatalf("restore-validate expected exit 0, got %d (%s)", exit, errOut.String())
	}
}

func TestTokenCreateProducesVerifiableToken(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	var errOut bytes.Buffer
	exit := run([]string{"token-create", "--subject", "ci", "--ttl", "1h", "--signing-key", "token-signing-key"}, &out, &errOut)
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d (%s)", exit, errOut.String())
	}
	token := strings.TrimSpace(out.String())
	if token == "" {
		t.Fatal("expected token output")
	}

	subject, err := auth.VerifyPersonalAccessToken(token, []byte("token-signing-key"), time.Now().UTC())
	if err != nil {
		t.Fatalf("verify token failed: %v", err)
	}
	if subject != "ci" {
		t.Fatalf("expected subject ci, got %q", subject)
	}
}

func TestPutObjectUploadsWithBearerToken(t *testing.T) {
	t.Parallel()

	token := "s000pat.token.signature"
	uploaded := ""
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		b, _ := io.ReadAll(r.Body)
		uploaded = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "hello.txt")
	if err := os.WriteFile(filePath, []byte("hello from cli"), 0o600); err != nil {
		t.Fatalf("write test file failed: %v", err)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	exit := run([]string{"put-object", "--endpoint", ts.URL, "--bucket", "photos", "--key", "album/hello.txt", "--file", filePath, "--token", token}, &out, &errOut)
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d (%s)", exit, errOut.String())
	}
	if uploaded != "hello from cli" {
		t.Fatalf("expected uploaded body, got %q", uploaded)
	}
	if !strings.Contains(out.String(), "uploaded hello.txt") {
		t.Fatalf("expected upload success output, got %q", out.String())
	}
}
