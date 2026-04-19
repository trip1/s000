package config

import (
	"testing"
	"time"
)

func TestLoadFromEnvDefaults(t *testing.T) {
	t.Parallel()

	cfg := LoadFromEnv(func(string) string { return "" })
	if cfg.Addr != ":9000" {
		t.Fatalf("expected default addr :9000, got %q", cfg.Addr)
	}
	if cfg.DataDir != "./data" {
		t.Fatalf("expected default data dir ./data, got %q", cfg.DataDir)
	}
	if cfg.MetadataBackend != "sqlite" {
		t.Fatalf("expected default metadata backend sqlite, got %q", cfg.MetadataBackend)
	}
	if cfg.MetricsPath != "/metrics" {
		t.Fatalf("expected default metrics path /metrics, got %q", cfg.MetricsPath)
	}
	if cfg.UITheme != "sysadmin90" {
		t.Fatalf("expected default ui theme sysadmin90, got %q", cfg.UITheme)
	}
	if cfg.WebsiteEnabled {
		t.Fatal("expected website disabled by default")
	}
}

func TestLoadFromEnvOverrides(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"S000_ADDR":                    ":19000",
		"S000_DATA_DIR":                "/srv/s000",
		"S000_METADATA_BACKEND":        "postgresql",
		"S000_METADATA_DSN":            "postgres://localhost/s000",
		"S000_TRACING_ENABLED":         "true",
		"S000_METRICS_PATH":            "/internal/metrics",
		"S000_HTTP_READ_TIMEOUT":       "45s",
		"S000_AUTH_FAIL_THRESHOLD":     "10",
		"S000_UI_THEME":                "solarized",
		"S000_WEBSITE_ENABLED":         "true",
		"S000_WEBSITE_ADDR":            ":9100",
		"S000_WEBSITE_DOMAIN":          "example.test",
		"S000_SHUTDOWN_TIMEOUT":        "25s",
		"S000_METADATA_CONNECT_TIMEOUT": "4s",
	}
	cfg := LoadFromEnv(func(key string) string { return env[key] })

	if cfg.Addr != ":19000" || cfg.DataDir != "/srv/s000" {
		t.Fatalf("unexpected addr/data overrides: %#v", cfg)
	}
	if cfg.MetadataBackend != "postgresql" || cfg.MetadataDSN != "postgres://localhost/s000" {
		t.Fatalf("unexpected metadata overrides: %#v", cfg)
	}
	if !cfg.TracingEnabled {
		t.Fatal("expected tracing enabled override")
	}
	if cfg.MetricsPath != "/internal/metrics" {
		t.Fatalf("unexpected metrics path override: %q", cfg.MetricsPath)
	}
	if cfg.HTTPReadTimeout != 45*time.Second {
		t.Fatalf("expected read timeout 45s, got %s", cfg.HTTPReadTimeout)
	}
	if cfg.AuthFailThreshold != 10 {
		t.Fatalf("expected auth fail threshold 10, got %d", cfg.AuthFailThreshold)
	}
	if cfg.UITheme != "solarized" {
		t.Fatalf("expected ui theme solarized, got %q", cfg.UITheme)
	}
	if !cfg.WebsiteEnabled || cfg.WebsiteAddr != ":9100" || cfg.WebsiteDomain != "example.test" {
		t.Fatalf("unexpected website overrides: %#v", cfg)
	}
	if cfg.ShutdownTimeout != 25*time.Second || cfg.MetadataConnectTimeout != 4*time.Second {
		t.Fatalf("unexpected duration overrides: shutdown=%s connect=%s", cfg.ShutdownTimeout, cfg.MetadataConnectTimeout)
	}
}

func TestParseTLSMinVersion(t *testing.T) {
	t.Parallel()

	if _, err := ParseTLSMinVersion("1.2"); err != nil {
		t.Fatalf("expected tls 1.2 parse success, got %v", err)
	}
	if _, err := ParseTLSMinVersion("1.3"); err != nil {
		t.Fatalf("expected tls 1.3 parse success, got %v", err)
	}
	if _, err := ParseTLSMinVersion("1.1"); err == nil {
		t.Fatal("expected unsupported tls version error")
	}
}
