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

func TestFunctionsCommands(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/functions":
			_, _ = w.Write([]byte(`{"functions":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/functions/templates":
			_, _ = w.Write([]byte(`{"templates":[{"name":"hello-json"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/functions/alerts":
			_, _ = w.Write([]byte(`{"alerts":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/functions/fn/invoke":
			_, _ = w.Write([]byte(`{"result":{"continue":true}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	var out bytes.Buffer
	var errOut bytes.Buffer
	if exit := run([]string{"functions-list", "--endpoint", ts.URL}, &out, &errOut); exit != 0 {
		t.Fatalf("functions-list expected exit 0, got %d (%s)", exit, errOut.String())
	}
	if !strings.Contains(out.String(), "functions") {
		t.Fatalf("expected functions-list output, got %q", out.String())
	}

	out.Reset()
	errOut.Reset()
	if exit := run([]string{"functions-templates", "--endpoint", ts.URL}, &out, &errOut); exit != 0 {
		t.Fatalf("functions-templates expected exit 0, got %d (%s)", exit, errOut.String())
	}
	if !strings.Contains(out.String(), "hello-json") {
		t.Fatalf("expected templates output, got %q", out.String())
	}

	out.Reset()
	errOut.Reset()
	if exit := run([]string{"functions-alerts", "--endpoint", ts.URL}, &out, &errOut); exit != 0 {
		t.Fatalf("functions-alerts expected exit 0, got %d (%s)", exit, errOut.String())
	}
	if !strings.Contains(out.String(), "alerts") {
		t.Fatalf("expected alerts output, got %q", out.String())
	}

	out.Reset()
	errOut.Reset()
	if exit := run([]string{"functions-invoke", "--endpoint", ts.URL, "--name", "fn", "--payload", `{"k":"v"}`}, &out, &errOut); exit != 0 {
		t.Fatalf("functions-invoke expected exit 0, got %d (%s)", exit, errOut.String())
	}
	if !strings.Contains(out.String(), "continue") {
		t.Fatalf("expected invoke output, got %q", out.String())
	}
}
