package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	for _, command := range []string{"backup-create", "restore-validate", "health-inspect", "completion"} {
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
