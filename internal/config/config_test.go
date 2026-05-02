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
	if cfg.MetadataDSN != "file:./data/s000-metadata.db" {
		t.Fatalf("expected metadata dsn file:./data/s000-metadata.db, got %q", cfg.MetadataDSN)
	}
	if cfg.MetricsPath != "/metrics" {
		t.Fatalf("expected default metrics path /metrics, got %q", cfg.MetricsPath)
	}
	if cfg.UITheme != "sysadmin90" {
		t.Fatalf("expected default ui theme sysadmin90, got %q", cfg.UITheme)
	}
	if cfg.UIDashboardStatsSSE != 2*time.Second {
		t.Fatalf("expected default dashboard stats sse interval 2s, got %s", cfg.UIDashboardStatsSSE)
	}
	if cfg.UIBucketsSSE != 10*time.Second || cfg.UITokensSSE != 10*time.Second || cfg.UIObjectsSSE != 10*time.Second || cfg.UIObjectMetadataSSE != 10*time.Second {
		t.Fatalf("unexpected default ui sse intervals: buckets=%s tokens=%s objects=%s metadata=%s", cfg.UIBucketsSSE, cfg.UITokensSSE, cfg.UIObjectsSSE, cfg.UIObjectMetadataSSE)
	}
	if cfg.WebsiteEnabled {
		t.Fatal("expected website disabled by default")
	}
}

func TestLoadFromEnvUsesDataDirForDefaultMetadataDSN(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"S000_DATA_DIR": "/srv/s000-data",
	}
	cfg := LoadFromEnv(func(key string) string { return env[key] })
	if cfg.MetadataDSN != "file:/srv/s000-data/s000-metadata.db" {
		t.Fatalf("expected metadata dsn to follow data dir, got %q", cfg.MetadataDSN)
	}
}

func TestLoadFromEnvOverrides(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"S000_ADDR":                            ":19000",
		"S000_DATA_DIR":                        "/srv/s000",
		"S000_IMPORT_DIRECTORY":                "/srv/import",
		"S000_METADATA_BACKEND":                "postgresql",
		"S000_METADATA_DSN":                    "postgres://localhost/s000",
		"S000_TRACING_ENABLED":                 "true",
		"S000_METRICS_PATH":                    "/internal/metrics",
		"S000_HTTP_READ_TIMEOUT":               "45s",
		"S000_AUTH_FAIL_THRESHOLD":             "10",
		"S000_PAT_SIGNING_KEY":                 "pat-key",
		"S000_UI_THEME":                        "solarized",
		"S000_UI_SSE_DASHBOARD_STATS_INTERVAL": "3s",
		"S000_UI_SSE_BUCKETS_INTERVAL":         "11s",
		"S000_UI_SSE_TOKENS_INTERVAL":          "12s",
		"S000_UI_SSE_OBJECTS_INTERVAL":         "13s",
		"S000_UI_SSE_OBJECT_METADATA_INTERVAL": "14s",
		"S000_SSE_MASTER_KEY":                  "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"S000_WEBSITE_ENABLED":                 "true",
		"S000_WEBSITE_ADDR":                    ":9100",
		"S000_WEBSITE_DOMAIN":                  "example.test",
		"S000_SHUTDOWN_TIMEOUT":                "25s",
		"S000_METADATA_CONNECT_TIMEOUT":        "4s",
	}
	cfg := LoadFromEnv(func(key string) string { return env[key] })

	if cfg.Addr != ":19000" || cfg.DataDir != "/srv/s000" {
		t.Fatalf("unexpected addr/data overrides: %#v", cfg)
	}
	if cfg.ImportDirectory != "/srv/import" {
		t.Fatalf("expected import directory override, got %q", cfg.ImportDirectory)
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
	if cfg.PATSigningKey != "pat-key" {
		t.Fatalf("expected pat signing key override, got %q", cfg.PATSigningKey)
	}
	if cfg.UITheme != "solarized" {
		t.Fatalf("expected ui theme solarized, got %q", cfg.UITheme)
	}
	if cfg.UIDashboardStatsSSE != 3*time.Second || cfg.UIBucketsSSE != 11*time.Second || cfg.UITokensSSE != 12*time.Second || cfg.UIObjectsSSE != 13*time.Second || cfg.UIObjectMetadataSSE != 14*time.Second {
		t.Fatalf("unexpected ui sse interval overrides: stats=%s buckets=%s tokens=%s objects=%s metadata=%s", cfg.UIDashboardStatsSSE, cfg.UIBucketsSSE, cfg.UITokensSSE, cfg.UIObjectsSSE, cfg.UIObjectMetadataSSE)
	}
	if cfg.SSEMasterKey != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" {
		t.Fatalf("expected sse master key override, got %q", cfg.SSEMasterKey)
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
