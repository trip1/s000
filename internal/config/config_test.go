package config

import (
	"testing"
	"time"
)

func TestLoadFromEnvDefaults(t *testing.T) {
	t.Parallel()

	cfg := LoadFromEnv(func(string) string { return "" })

	if cfg.Addr != defaultAddr {
		t.Fatalf("expected default addr %q, got %q", defaultAddr, cfg.Addr)
	}
	if cfg.DataDir != defaultDataDir {
		t.Fatalf("expected default data dir %q, got %q", defaultDataDir, cfg.DataDir)
	}
	if cfg.LogMode != defaultLogMode {
		t.Fatalf("expected default log mode %q, got %q", defaultLogMode, cfg.LogMode)
	}
	if cfg.Domain != "" {
		t.Fatalf("expected empty default domain, got %q", cfg.Domain)
	}
	if cfg.ShutdownTimeout != defaultShutdownTimeout {
		t.Fatalf("expected default shutdown timeout %s, got %s", defaultShutdownTimeout, cfg.ShutdownTimeout)
	}
	if cfg.MaxInFlight != defaultMaxInFlight {
		t.Fatalf("expected default max in flight %d, got %d", defaultMaxInFlight, cfg.MaxInFlight)
	}
	if cfg.AuthMaxSkew != defaultAuthMaxSkew {
		t.Fatalf("expected default auth max skew %s, got %s", defaultAuthMaxSkew, cfg.AuthMaxSkew)
	}
	if cfg.AdminAccessKey != "" {
		t.Fatalf("expected empty default admin access key, got %q", cfg.AdminAccessKey)
	}
	if cfg.AdminSecretKey != "" {
		t.Fatalf("expected empty default admin secret key, got %q", cfg.AdminSecretKey)
	}
	if cfg.MetadataBackend != "sqlite" {
		t.Fatalf("expected default metadata backend %q, got %q", "sqlite", cfg.MetadataBackend)
	}
	if cfg.MetadataDSN == "" {
		t.Fatal("expected non-empty default metadata dsn")
	}
	if cfg.MetadataValkeyAddr != "127.0.0.1:6379" {
		t.Fatalf("expected default valkey addr %q, got %q", "127.0.0.1:6379", cfg.MetadataValkeyAddr)
	}
	if cfg.LifecycleRules != "" {
		t.Fatalf("expected empty lifecycle rules by default, got %q", cfg.LifecycleRules)
	}
	if cfg.LifecycleInterval != defaultLifecycleInterval {
		t.Fatalf("expected default lifecycle interval %s, got %s", defaultLifecycleInterval, cfg.LifecycleInterval)
	}
	if cfg.LifecycleBatchSize != defaultLifecycleBatchSize {
		t.Fatalf("expected default lifecycle batch size %d, got %d", defaultLifecycleBatchSize, cfg.LifecycleBatchSize)
	}
	if cfg.LifecycleMaxRetries != defaultLifecycleMaxRetries {
		t.Fatalf("expected default lifecycle retries %d, got %d", defaultLifecycleMaxRetries, cfg.LifecycleMaxRetries)
	}
	if cfg.LifecycleBackoff != defaultLifecycleBackoff {
		t.Fatalf("expected default lifecycle backoff %s, got %s", defaultLifecycleBackoff, cfg.LifecycleBackoff)
	}
	if cfg.LifecycleDryRun {
		t.Fatal("expected lifecycle dry-run to be false by default")
	}
	if cfg.MetricsPath != defaultMetricsPath {
		t.Fatalf("expected default metrics path %q, got %q", defaultMetricsPath, cfg.MetricsPath)
	}
	if cfg.TracingEnabled {
		t.Fatal("expected tracing disabled by default")
	}
	if cfg.HTTPReadHeaderTimeout != defaultHTTPReadHeaderTimeout {
		t.Fatalf("expected default read header timeout %s, got %s", defaultHTTPReadHeaderTimeout, cfg.HTTPReadHeaderTimeout)
	}
	if cfg.HTTPReadTimeout != defaultHTTPReadTimeout {
		t.Fatalf("expected default read timeout %s, got %s", defaultHTTPReadTimeout, cfg.HTTPReadTimeout)
	}
	if cfg.HTTPWriteTimeout != defaultHTTPWriteTimeout {
		t.Fatalf("expected default write timeout %s, got %s", defaultHTTPWriteTimeout, cfg.HTTPWriteTimeout)
	}
	if cfg.HTTPIdleTimeout != defaultHTTPIdleTimeout {
		t.Fatalf("expected default idle timeout %s, got %s", defaultHTTPIdleTimeout, cfg.HTTPIdleTimeout)
	}
	if cfg.HTTPMaxHeaderBytes != defaultHTTPMaxHeaderBytes {
		t.Fatalf("expected default max header bytes %d, got %d", defaultHTTPMaxHeaderBytes, cfg.HTTPMaxHeaderBytes)
	}
	if cfg.HTTPDisableKeepAlive {
		t.Fatal("expected keepalive enabled by default")
	}
	if cfg.HeavyOpsWorkers != defaultHeavyOpsWorkers {
		t.Fatalf("expected default heavy workers %d, got %d", defaultHeavyOpsWorkers, cfg.HeavyOpsWorkers)
	}
	if cfg.HeavyOpsQueue != defaultHeavyOpsQueue {
		t.Fatalf("expected default heavy queue %d, got %d", defaultHeavyOpsQueue, cfg.HeavyOpsQueue)
	}
	if cfg.TLSEnabled {
		t.Fatal("expected TLS disabled by default")
	}
	if cfg.TLSMinVersion != defaultTLSMinVersion {
		t.Fatalf("expected default TLS min version %q, got %q", defaultTLSMinVersion, cfg.TLSMinVersion)
	}
	if cfg.AuthFailThreshold != defaultAuthFailThreshold {
		t.Fatalf("expected default auth fail threshold %d, got %d", defaultAuthFailThreshold, cfg.AuthFailThreshold)
	}
	if cfg.UITheme != defaultUITheme {
		t.Fatalf("expected default ui theme %q, got %q", defaultUITheme, cfg.UITheme)
	}
	if cfg.WebsiteEnabled != defaultWebsiteEnabled {
		t.Fatalf("expected default website enabled %v, got %v", defaultWebsiteEnabled, cfg.WebsiteEnabled)
	}
	if cfg.WebsiteAddr != defaultWebsiteAddr {
		t.Fatalf("expected default website addr %q, got %q", defaultWebsiteAddr, cfg.WebsiteAddr)
	}
	if cfg.WebsiteDomain != "" {
		t.Fatalf("expected empty default website domain, got %q", cfg.WebsiteDomain)
	}
	if cfg.FunctionsEnabled {
		t.Fatal("expected functions runtime disabled by default")
	}
	if cfg.FunctionsDir != "./functions" {
		t.Fatalf("expected default functions dir %q, got %q", "./functions", cfg.FunctionsDir)
	}
	if cfg.FunctionsRuntime != "wazero" {
		t.Fatalf("expected default functions runtime %q, got %q", "wazero", cfg.FunctionsRuntime)
	}
	if cfg.FunctionsMemoryLimit != 64 {
		t.Fatalf("expected default functions memory limit 64, got %d", cfg.FunctionsMemoryLimit)
	}
	if cfg.FunctionsCPULimit != 100*time.Millisecond {
		t.Fatalf("expected default functions cpu limit 100ms, got %s", cfg.FunctionsCPULimit)
	}
	if !cfg.FunctionsNetworkAllow {
		t.Fatal("expected functions networking allowed by default")
	}
	if cfg.FunctionsFSAllow {
		t.Fatal("expected functions fs access disabled by default")
	}
	if cfg.FunctionsHotReload {
		t.Fatal("expected functions hot reload disabled by default")
	}
	if cfg.FunctionsReloadInterval != 2*time.Second {
		t.Fatalf("expected default functions reload interval 2s, got %s", cfg.FunctionsReloadInterval)
	}
}

func TestLoadFromEnvOverrides(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		"S000_ADDR":                      ":7777",
		"S000_DATA_DIR":                  "/tmp/s000-data",
		"S000_LOG_MODE":                  "debug",
		"S000_DOMAIN":                    "s000.local",
		"S000_SHUTDOWN_TIMEOUT":          "3s",
		"S000_MAX_INFLIGHT":              "42",
		"S000_AUTH_MAX_SKEW":             "20m",
		"S000_ADMIN_ACCESS_KEY":          "admin",
		"S000_ADMIN_SECRET_KEY":          "super-secret",
		"S000_METADATA_BACKEND":          "postgresql",
		"S000_METADATA_DSN":              "postgres://user:pass@localhost:5432/s000?sslmode=disable",
		"S000_VALKEY_ADDR":               "10.0.0.2:6379",
		"S000_LIFECYCLE_RULES":           "prefix=tmp/,age=24h",
		"S000_LIFECYCLE_INTERVAL":        "45s",
		"S000_LIFECYCLE_BATCH_SIZE":      "75",
		"S000_LIFECYCLE_MAX_RETRIES":     "5",
		"S000_LIFECYCLE_BACKOFF":         "150ms",
		"S000_LIFECYCLE_DRY_RUN":         "true",
		"S000_METRICS_PATH":              "/internal-metrics",
		"S000_TRACING_ENABLED":           "true",
		"S000_HTTP_READ_HEADER_TIMEOUT":  "2s",
		"S000_HTTP_READ_TIMEOUT":         "7s",
		"S000_HTTP_WRITE_TIMEOUT":        "9s",
		"S000_HTTP_IDLE_TIMEOUT":         "15s",
		"S000_HTTP_MAX_HEADER_BYTES":     "8192",
		"S000_HTTP_DISABLE_KEEPALIVE":    "true",
		"S000_HEAVY_OPS_WORKERS":         "8",
		"S000_HEAVY_OPS_QUEUE":           "32",
		"S000_TLS_ENABLED":               "true",
		"S000_TLS_CERT_FILE":             "/etc/s000/tls.crt",
		"S000_TLS_KEY_FILE":              "/etc/s000/tls.key",
		"S000_TLS_MIN_VERSION":           "1.3",
		"S000_AUTH_FAIL_THRESHOLD":       "4",
		"S000_AUTH_FAIL_WINDOW":          "90s",
		"S000_AUTH_BLOCK_DURATION":       "2m",
		"S000_UI_THEME":                  "graphite",
		"S000_WEBSITE_ENABLED":           "true",
		"S000_WEBSITE_ADDR":              ":9080",
		"S000_WEBSITE_DOMAIN":            "website.local",
		"S000_FUNCTIONS_ENABLED":         "true",
		"S000_FUNCTIONS_DIR":             "/var/lib/s000/functions",
		"S000_FUNCTIONS_RUNTIME":         "wasmer",
		"S000_FUNCTIONS_MEMORY_LIMIT":    "128",
		"S000_FUNCTIONS_CPU_LIMIT":       "250ms",
		"S000_FUNCTIONS_NETWORK_ALLOW":   "false",
		"S000_FUNCTIONS_FS_ALLOW":        "true",
		"S000_FUNCTIONS_HOT_RELOAD":      "true",
		"S000_FUNCTIONS_RELOAD_INTERVAL": "5s",
	}

	cfg := LoadFromEnv(func(key string) string { return values[key] })

	if cfg.Addr != values["S000_ADDR"] {
		t.Fatalf("expected overridden addr %q, got %q", values["S000_ADDR"], cfg.Addr)
	}
	if cfg.DataDir != values["S000_DATA_DIR"] {
		t.Fatalf("expected overridden data dir %q, got %q", values["S000_DATA_DIR"], cfg.DataDir)
	}
	if cfg.LogMode != values["S000_LOG_MODE"] {
		t.Fatalf("expected overridden log mode %q, got %q", values["S000_LOG_MODE"], cfg.LogMode)
	}
	if cfg.Domain != values["S000_DOMAIN"] {
		t.Fatalf("expected overridden domain %q, got %q", values["S000_DOMAIN"], cfg.Domain)
	}
	if cfg.ShutdownTimeout != 3*time.Second {
		t.Fatalf("expected overridden shutdown timeout %s, got %s", 3*time.Second, cfg.ShutdownTimeout)
	}
	if cfg.MaxInFlight != 42 {
		t.Fatalf("expected overridden max in flight %d, got %d", 42, cfg.MaxInFlight)
	}
	if cfg.AuthMaxSkew != 20*time.Minute {
		t.Fatalf("expected overridden auth max skew %s, got %s", 20*time.Minute, cfg.AuthMaxSkew)
	}
	if cfg.AdminAccessKey != "admin" {
		t.Fatalf("expected overridden admin access key %q, got %q", "admin", cfg.AdminAccessKey)
	}
	if cfg.AdminSecretKey != "super-secret" {
		t.Fatalf("expected overridden admin secret key %q, got %q", "super-secret", cfg.AdminSecretKey)
	}
	if cfg.MetadataBackend != "postgresql" {
		t.Fatalf("expected overridden metadata backend %q, got %q", "postgresql", cfg.MetadataBackend)
	}
	if cfg.MetadataDSN != "postgres://user:pass@localhost:5432/s000?sslmode=disable" {
		t.Fatalf("expected overridden metadata dsn, got %q", cfg.MetadataDSN)
	}
	if cfg.MetadataValkeyAddr != "10.0.0.2:6379" {
		t.Fatalf("expected overridden valkey addr %q, got %q", "10.0.0.2:6379", cfg.MetadataValkeyAddr)
	}
	if cfg.LifecycleRules != "prefix=tmp/,age=24h" {
		t.Fatalf("expected overridden lifecycle rules, got %q", cfg.LifecycleRules)
	}
	if cfg.LifecycleInterval != 45*time.Second {
		t.Fatalf("expected overridden lifecycle interval %s, got %s", 45*time.Second, cfg.LifecycleInterval)
	}
	if cfg.LifecycleBatchSize != 75 {
		t.Fatalf("expected overridden lifecycle batch size %d, got %d", 75, cfg.LifecycleBatchSize)
	}
	if cfg.LifecycleMaxRetries != 5 {
		t.Fatalf("expected overridden lifecycle retries %d, got %d", 5, cfg.LifecycleMaxRetries)
	}
	if cfg.LifecycleBackoff != 150*time.Millisecond {
		t.Fatalf("expected overridden lifecycle backoff %s, got %s", 150*time.Millisecond, cfg.LifecycleBackoff)
	}
	if !cfg.LifecycleDryRun {
		t.Fatal("expected lifecycle dry-run to be true")
	}
	if cfg.MetricsPath != "/internal-metrics" {
		t.Fatalf("expected overridden metrics path %q, got %q", "/internal-metrics", cfg.MetricsPath)
	}
	if !cfg.TracingEnabled {
		t.Fatal("expected tracing to be enabled")
	}
	if cfg.HTTPReadHeaderTimeout != 2*time.Second {
		t.Fatalf("expected overridden read header timeout 2s, got %s", cfg.HTTPReadHeaderTimeout)
	}
	if cfg.HTTPReadTimeout != 7*time.Second {
		t.Fatalf("expected overridden read timeout 7s, got %s", cfg.HTTPReadTimeout)
	}
	if cfg.HTTPWriteTimeout != 9*time.Second {
		t.Fatalf("expected overridden write timeout 9s, got %s", cfg.HTTPWriteTimeout)
	}
	if cfg.HTTPIdleTimeout != 15*time.Second {
		t.Fatalf("expected overridden idle timeout 15s, got %s", cfg.HTTPIdleTimeout)
	}
	if cfg.HTTPMaxHeaderBytes != 8192 {
		t.Fatalf("expected overridden max header bytes 8192, got %d", cfg.HTTPMaxHeaderBytes)
	}
	if !cfg.HTTPDisableKeepAlive {
		t.Fatal("expected keepalive disabled")
	}
	if cfg.HeavyOpsWorkers != 8 {
		t.Fatalf("expected overridden heavy workers 8, got %d", cfg.HeavyOpsWorkers)
	}
	if cfg.HeavyOpsQueue != 32 {
		t.Fatalf("expected overridden heavy queue 32, got %d", cfg.HeavyOpsQueue)
	}
	if !cfg.TLSEnabled {
		t.Fatal("expected TLS enabled")
	}
	if cfg.TLSCertFile != "/etc/s000/tls.crt" {
		t.Fatalf("expected overridden tls cert path, got %q", cfg.TLSCertFile)
	}
	if cfg.TLSKeyFile != "/etc/s000/tls.key" {
		t.Fatalf("expected overridden tls key path, got %q", cfg.TLSKeyFile)
	}
	if cfg.TLSMinVersion != "1.3" {
		t.Fatalf("expected overridden tls min version 1.3, got %q", cfg.TLSMinVersion)
	}
	if cfg.AuthFailThreshold != 4 {
		t.Fatalf("expected overridden auth fail threshold 4, got %d", cfg.AuthFailThreshold)
	}
	if cfg.AuthFailWindow != 90*time.Second {
		t.Fatalf("expected overridden auth fail window 90s, got %s", cfg.AuthFailWindow)
	}
	if cfg.AuthBlockDuration != 2*time.Minute {
		t.Fatalf("expected overridden auth block duration 2m, got %s", cfg.AuthBlockDuration)
	}
	if cfg.UITheme != "graphite" {
		t.Fatalf("expected overridden ui theme graphite, got %q", cfg.UITheme)
	}
	if !cfg.WebsiteEnabled {
		t.Fatal("expected website endpoint enabled")
	}
	if cfg.WebsiteAddr != ":9080" {
		t.Fatalf("expected overridden website addr :9080, got %q", cfg.WebsiteAddr)
	}
	if cfg.WebsiteDomain != "website.local" {
		t.Fatalf("expected overridden website domain website.local, got %q", cfg.WebsiteDomain)
	}
	if !cfg.FunctionsEnabled {
		t.Fatal("expected functions runtime enabled")
	}
	if cfg.FunctionsDir != "/var/lib/s000/functions" {
		t.Fatalf("expected overridden functions dir, got %q", cfg.FunctionsDir)
	}
	if cfg.FunctionsRuntime != "wasmer" {
		t.Fatalf("expected overridden functions runtime wasmer, got %q", cfg.FunctionsRuntime)
	}
	if cfg.FunctionsMemoryLimit != 128 {
		t.Fatalf("expected overridden functions memory limit 128, got %d", cfg.FunctionsMemoryLimit)
	}
	if cfg.FunctionsCPULimit != 250*time.Millisecond {
		t.Fatalf("expected overridden functions cpu limit 250ms, got %s", cfg.FunctionsCPULimit)
	}
	if cfg.FunctionsNetworkAllow {
		t.Fatal("expected overridden functions networking false")
	}
	if !cfg.FunctionsFSAllow {
		t.Fatal("expected overridden functions fs allow true")
	}
	if !cfg.FunctionsHotReload {
		t.Fatal("expected overridden functions hot reload true")
	}
	if cfg.FunctionsReloadInterval != 5*time.Second {
		t.Fatalf("expected overridden functions reload interval 5s, got %s", cfg.FunctionsReloadInterval)
	}
}

func TestLoadFromEnvInvalidNumericAndDurationFallback(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		"S000_SHUTDOWN_TIMEOUT":          "oops",
		"S000_MAX_INFLIGHT":              "-1",
		"S000_AUTH_MAX_SKEW":             "nope",
		"S000_LIFECYCLE_INTERVAL":        "invalid",
		"S000_LIFECYCLE_BATCH_SIZE":      "0",
		"S000_LIFECYCLE_MAX_RETRIES":     "-1",
		"S000_LIFECYCLE_BACKOFF":         "bad",
		"S000_LIFECYCLE_DRY_RUN":         "no-bool",
		"S000_TRACING_ENABLED":           "not-bool",
		"S000_HTTP_READ_TIMEOUT":         "bad",
		"S000_HTTP_WRITE_TIMEOUT":        "bad",
		"S000_HTTP_MAX_HEADER_BYTES":     "0",
		"S000_HEAVY_OPS_WORKERS":         "0",
		"S000_HEAVY_OPS_QUEUE":           "-1",
		"S000_TLS_ENABLED":               "not-bool",
		"S000_AUTH_FAIL_THRESHOLD":       "0",
		"S000_AUTH_FAIL_WINDOW":          "bad",
		"S000_AUTH_BLOCK_DURATION":       "bad",
		"S000_UI_THEME":                  "",
		"S000_WEBSITE_ENABLED":           "bad-bool",
		"S000_FUNCTIONS_ENABLED":         "bad-bool",
		"S000_FUNCTIONS_MEMORY_LIMIT":    "0",
		"S000_FUNCTIONS_CPU_LIMIT":       "bad",
		"S000_FUNCTIONS_NETWORK_ALLOW":   "bad-bool",
		"S000_FUNCTIONS_FS_ALLOW":        "bad-bool",
		"S000_FUNCTIONS_HOT_RELOAD":      "bad-bool",
		"S000_FUNCTIONS_RELOAD_INTERVAL": "bad",
	}

	cfg := LoadFromEnv(func(key string) string { return values[key] })

	if cfg.ShutdownTimeout != defaultShutdownTimeout {
		t.Fatalf("expected fallback shutdown timeout %s, got %s", defaultShutdownTimeout, cfg.ShutdownTimeout)
	}
	if cfg.MaxInFlight != defaultMaxInFlight {
		t.Fatalf("expected fallback max in flight %d, got %d", defaultMaxInFlight, cfg.MaxInFlight)
	}
	if cfg.AuthMaxSkew != defaultAuthMaxSkew {
		t.Fatalf("expected fallback auth max skew %s, got %s", defaultAuthMaxSkew, cfg.AuthMaxSkew)
	}
	if cfg.LifecycleInterval != defaultLifecycleInterval {
		t.Fatalf("expected fallback lifecycle interval %s, got %s", defaultLifecycleInterval, cfg.LifecycleInterval)
	}
	if cfg.LifecycleBatchSize != defaultLifecycleBatchSize {
		t.Fatalf("expected fallback lifecycle batch size %d, got %d", defaultLifecycleBatchSize, cfg.LifecycleBatchSize)
	}
	if cfg.LifecycleMaxRetries != defaultLifecycleMaxRetries {
		t.Fatalf("expected fallback lifecycle retries %d, got %d", defaultLifecycleMaxRetries, cfg.LifecycleMaxRetries)
	}
	if cfg.LifecycleBackoff != defaultLifecycleBackoff {
		t.Fatalf("expected fallback lifecycle backoff %s, got %s", defaultLifecycleBackoff, cfg.LifecycleBackoff)
	}
	if cfg.LifecycleDryRun {
		t.Fatal("expected fallback lifecycle dry-run false")
	}
	if cfg.MetricsPath != defaultMetricsPath {
		t.Fatalf("expected fallback metrics path %q, got %q", defaultMetricsPath, cfg.MetricsPath)
	}
	if cfg.TracingEnabled {
		t.Fatal("expected fallback tracing disabled")
	}
	if cfg.HTTPReadTimeout != defaultHTTPReadTimeout {
		t.Fatalf("expected fallback read timeout %s, got %s", defaultHTTPReadTimeout, cfg.HTTPReadTimeout)
	}
	if cfg.HTTPWriteTimeout != defaultHTTPWriteTimeout {
		t.Fatalf("expected fallback write timeout %s, got %s", defaultHTTPWriteTimeout, cfg.HTTPWriteTimeout)
	}
	if cfg.HTTPMaxHeaderBytes != defaultHTTPMaxHeaderBytes {
		t.Fatalf("expected fallback max header bytes %d, got %d", defaultHTTPMaxHeaderBytes, cfg.HTTPMaxHeaderBytes)
	}
	if cfg.HeavyOpsWorkers != defaultHeavyOpsWorkers {
		t.Fatalf("expected fallback heavy workers %d, got %d", defaultHeavyOpsWorkers, cfg.HeavyOpsWorkers)
	}
	if cfg.HeavyOpsQueue != defaultHeavyOpsQueue {
		t.Fatalf("expected fallback heavy queue %d, got %d", defaultHeavyOpsQueue, cfg.HeavyOpsQueue)
	}
	if cfg.TLSEnabled {
		t.Fatal("expected fallback tls disabled")
	}
	if cfg.AuthFailThreshold != defaultAuthFailThreshold {
		t.Fatalf("expected fallback auth fail threshold %d, got %d", defaultAuthFailThreshold, cfg.AuthFailThreshold)
	}
	if cfg.AuthFailWindow != defaultAuthFailWindow {
		t.Fatalf("expected fallback auth fail window %s, got %s", defaultAuthFailWindow, cfg.AuthFailWindow)
	}
	if cfg.AuthBlockDuration != defaultAuthBlockDuration {
		t.Fatalf("expected fallback auth block duration %s, got %s", defaultAuthBlockDuration, cfg.AuthBlockDuration)
	}
	if cfg.UITheme != defaultUITheme {
		t.Fatalf("expected fallback ui theme %q, got %q", defaultUITheme, cfg.UITheme)
	}
	if cfg.WebsiteEnabled != defaultWebsiteEnabled {
		t.Fatalf("expected fallback website enabled %v, got %v", defaultWebsiteEnabled, cfg.WebsiteEnabled)
	}
	if cfg.WebsiteAddr != defaultWebsiteAddr {
		t.Fatalf("expected fallback website addr %q, got %q", defaultWebsiteAddr, cfg.WebsiteAddr)
	}
	if cfg.FunctionsEnabled {
		t.Fatal("expected fallback functions runtime disabled")
	}
	if cfg.FunctionsMemoryLimit != 64 {
		t.Fatalf("expected fallback functions memory limit 64, got %d", cfg.FunctionsMemoryLimit)
	}
	if cfg.FunctionsCPULimit != 100*time.Millisecond {
		t.Fatalf("expected fallback functions cpu limit 100ms, got %s", cfg.FunctionsCPULimit)
	}
	if !cfg.FunctionsNetworkAllow {
		t.Fatal("expected fallback functions networking allowed")
	}
	if cfg.FunctionsFSAllow {
		t.Fatal("expected fallback functions fs allow false")
	}
	if cfg.FunctionsHotReload {
		t.Fatal("expected fallback functions hot reload false")
	}
	if cfg.FunctionsReloadInterval != 2*time.Second {
		t.Fatalf("expected fallback functions reload interval 2s, got %s", cfg.FunctionsReloadInterval)
	}
}

func TestValidateTLSEnabledSettings(t *testing.T) {
	t.Parallel()

	if err := ValidateTLSEnabledSettings(Config{}); err != nil {
		t.Fatalf("unexpected validation error for tls-disabled config: %v", err)
	}

	err := ValidateTLSEnabledSettings(Config{TLSEnabled: true, TLSKeyFile: "/tmp/key", TLSMinVersion: "1.2"})
	if err == nil {
		t.Fatal("expected tls cert validation error")
	}

	err = ValidateTLSEnabledSettings(Config{TLSEnabled: true, TLSCertFile: "/tmp/cert", TLSKeyFile: "/tmp/key", TLSMinVersion: "1.3"})
	if err != nil {
		t.Fatalf("expected valid tls settings, got %v", err)
	}
}

func TestParseTLSMinVersion(t *testing.T) {
	t.Parallel()

	if _, err := ParseTLSMinVersion("1.2"); err != nil {
		t.Fatalf("expected 1.2 parse success, got %v", err)
	}
	if _, err := ParseTLSMinVersion("1.3"); err != nil {
		t.Fatalf("expected 1.3 parse success, got %v", err)
	}
	if _, err := ParseTLSMinVersion("1.1"); err == nil {
		t.Fatal("expected unsupported tls version error")
	}
}
