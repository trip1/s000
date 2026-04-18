//go:build integration

package integration_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIHealthInspectAgainstLocalEndpoint(t *testing.T) {
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

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", ".."))

	cmd := exec.Command("go", "run", "./cmd/s000ctl", "health-inspect", "--endpoint", ts.URL)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("s000ctl health-inspect failed: %v (%s)", err, string(out))
	}
	text := string(out)
	if !strings.Contains(text, "health: ok") || !strings.Contains(text, "ready: ok") {
		t.Fatalf("unexpected output: %s", text)
	}
}
