package config

import (
	"crypto/tls"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAddr                              = ":9000"
	defaultDataDir                           = "./data"
	defaultLogMode                           = "info"
	defaultShutdownTimeout                   = 10 * time.Second
	defaultMaxInFlight                       = 128
	defaultAuthMaxSkew                       = 15 * time.Minute
	defaultMetadataBackend                   = "sqlite"
	defaultMetadataDSN                       = "file:./data/s000-metadata.db"
	defaultValkeyAddr                        = "127.0.0.1:6379"
	defaultMetadataConnectTimeout            = 3 * time.Second
	defaultLifecycleInterval                 = 5 * time.Minute
	defaultLifecycleBatchSize                = 100
	defaultLifecycleMaxRetries               = 3
	defaultLifecycleBackoff                  = 200 * time.Millisecond
	defaultMetricsPath                       = "/metrics"
	defaultHTTPReadHeaderTimeout             = 5 * time.Second
	defaultHTTPReadTimeout                   = 30 * time.Second
	defaultHTTPWriteTimeout                  = 30 * time.Second
	defaultHTTPIdleTimeout                   = 60 * time.Second
	defaultHTTPMaxHeaderBytes                = 1 << 20
	defaultHeavyOpsWorkers                   = 4
	defaultHeavyOpsQueue                     = 64
	defaultTLSEnabled                        = false
	defaultTLSMinVersion                     = "1.2"
	defaultAuthFailThreshold                 = 20
	defaultAuthFailWindow                    = time.Minute
	defaultAuthBlockDuration                 = 5 * time.Minute
	defaultUITheme                           = "sysadmin90"
	defaultWebsiteEnabled                    = false
	defaultWebsiteAddr                       = ":9001"
	defaultFunctionsEnabled                  = false
	defaultFunctionsDir                      = "./functions"
	defaultFunctionsRuntime                  = "wazero"
	defaultFunctionsMemoryLimit              = 64
	defaultFunctionsCPULimit                 = 100 * time.Millisecond
	defaultFunctionsNetworkAllow             = true
	defaultFunctionsFSAllow                  = false
	defaultFunctionsHotReload                = false
	defaultFunctionsHTTPPublic               = false
	defaultFunctionsHTTPCORSAllowOrigin      = ""
	defaultFunctionsHTTPCORSAllowMethods     = "GET, POST, PUT, PATCH, DELETE, OPTIONS"
	defaultFunctionsHTTPCORSAllowHeaders     = "Content-Type, Authorization"
	defaultFunctionsHTTPCORSExposeHeaders    = ""
	defaultFunctionsHTTPCORSMaxAge           = 600
	defaultFunctionsHTTPCORSAllowCredentials = false
	defaultFunctionsReloadInterval           = 2 * time.Second
	defaultFunctionsRateLimitPerMinute       = 0
	defaultFunctionsMaxConcurrent            = 0
	defaultFunctionsDailyQuota               = 0
	defaultFunctionsAlertErrorCountThreshold = 10
)

type Config struct {
	Addr                              string
	DataDir                           string
	LogMode                           string
	Domain                            string
	ShutdownTimeout                   time.Duration
	MaxInFlight                       int
	AuthMaxSkew                       time.Duration
	AdminAccessKey                    string
	AdminSecretKey                    string
	MetadataBackend                   string
	MetadataDSN                       string
	MetadataValkeyAddr                string
	MetadataConnectTimeout            time.Duration
	LifecycleRules                    string
	LifecycleInterval                 time.Duration
	LifecycleBatchSize                int
	LifecycleMaxRetries               int
	LifecycleBackoff                  time.Duration
	LifecycleDryRun                   bool
	MetricsPath                       string
	TracingEnabled                    bool
	HTTPReadHeaderTimeout             time.Duration
	HTTPReadTimeout                   time.Duration
	HTTPWriteTimeout                  time.Duration
	HTTPIdleTimeout                   time.Duration
	HTTPMaxHeaderBytes                int
	HTTPDisableKeepAlive              bool
	HeavyOpsWorkers                   int
	HeavyOpsQueue                     int
	TLSEnabled                        bool
	TLSCertFile                       string
	TLSKeyFile                        string
	TLSMinVersion                     string
	AuthFailThreshold                 int
	AuthFailWindow                    time.Duration
	AuthBlockDuration                 time.Duration
	UITheme                           string
	WebsiteEnabled                    bool
	WebsiteAddr                       string
	WebsiteDomain                     string
	FunctionsEnabled                  bool
	FunctionsDir                      string
	FunctionsRuntime                  string
	FunctionsMemoryLimit              int
	FunctionsCPULimit                 time.Duration
	FunctionsNetworkAllow             bool
	FunctionsFSAllow                  bool
	FunctionsHotReload                bool
	FunctionsHTTPPublic               bool
	FunctionsHTTPCORSAllowOrigin      string
	FunctionsHTTPCORSAllowMethods     string
	FunctionsHTTPCORSAllowHeaders     string
	FunctionsHTTPCORSExposeHeaders    string
	FunctionsHTTPCORSMaxAge           int
	FunctionsHTTPCORSAllowCredentials bool
	FunctionsReloadInterval           time.Duration
	FunctionsRateLimitPerMinute       int
	FunctionsMaxConcurrent            int
	FunctionsDailyQuota               int
	FunctionsAlertErrorCountThreshold int
}

// Load returns configuration using process environment variables.
func Load() Config {
	return LoadFromEnv(os.Getenv)
}

// LoadFromEnv builds configuration from a getenv function.
func LoadFromEnv(getenv func(string) string) Config {
	cfg := Config{
		Addr:                              defaultAddr,
		DataDir:                           defaultDataDir,
		LogMode:                           defaultLogMode,
		ShutdownTimeout:                   defaultShutdownTimeout,
		MaxInFlight:                       defaultMaxInFlight,
		AuthMaxSkew:                       defaultAuthMaxSkew,
		MetadataBackend:                   defaultMetadataBackend,
		MetadataDSN:                       defaultMetadataDSN,
		MetadataValkeyAddr:                defaultValkeyAddr,
		MetadataConnectTimeout:            defaultMetadataConnectTimeout,
		LifecycleInterval:                 defaultLifecycleInterval,
		LifecycleBatchSize:                defaultLifecycleBatchSize,
		LifecycleMaxRetries:               defaultLifecycleMaxRetries,
		LifecycleBackoff:                  defaultLifecycleBackoff,
		MetricsPath:                       defaultMetricsPath,
		HTTPReadHeaderTimeout:             defaultHTTPReadHeaderTimeout,
		HTTPReadTimeout:                   defaultHTTPReadTimeout,
		HTTPWriteTimeout:                  defaultHTTPWriteTimeout,
		HTTPIdleTimeout:                   defaultHTTPIdleTimeout,
		HTTPMaxHeaderBytes:                defaultHTTPMaxHeaderBytes,
		HeavyOpsWorkers:                   defaultHeavyOpsWorkers,
		HeavyOpsQueue:                     defaultHeavyOpsQueue,
		TLSEnabled:                        defaultTLSEnabled,
		TLSMinVersion:                     defaultTLSMinVersion,
		AuthFailThreshold:                 defaultAuthFailThreshold,
		AuthFailWindow:                    defaultAuthFailWindow,
		AuthBlockDuration:                 defaultAuthBlockDuration,
		UITheme:                           defaultUITheme,
		WebsiteEnabled:                    defaultWebsiteEnabled,
		WebsiteAddr:                       defaultWebsiteAddr,
		FunctionsEnabled:                  defaultFunctionsEnabled,
		FunctionsDir:                      defaultFunctionsDir,
		FunctionsRuntime:                  defaultFunctionsRuntime,
		FunctionsMemoryLimit:              defaultFunctionsMemoryLimit,
		FunctionsCPULimit:                 defaultFunctionsCPULimit,
		FunctionsNetworkAllow:             defaultFunctionsNetworkAllow,
		FunctionsFSAllow:                  defaultFunctionsFSAllow,
		FunctionsHotReload:                defaultFunctionsHotReload,
		FunctionsHTTPPublic:               defaultFunctionsHTTPPublic,
		FunctionsHTTPCORSAllowOrigin:      defaultFunctionsHTTPCORSAllowOrigin,
		FunctionsHTTPCORSAllowMethods:     defaultFunctionsHTTPCORSAllowMethods,
		FunctionsHTTPCORSAllowHeaders:     defaultFunctionsHTTPCORSAllowHeaders,
		FunctionsHTTPCORSExposeHeaders:    defaultFunctionsHTTPCORSExposeHeaders,
		FunctionsHTTPCORSMaxAge:           defaultFunctionsHTTPCORSMaxAge,
		FunctionsHTTPCORSAllowCredentials: defaultFunctionsHTTPCORSAllowCredentials,
		FunctionsReloadInterval:           defaultFunctionsReloadInterval,
		FunctionsRateLimitPerMinute:       defaultFunctionsRateLimitPerMinute,
		FunctionsMaxConcurrent:            defaultFunctionsMaxConcurrent,
		FunctionsDailyQuota:               defaultFunctionsDailyQuota,
		FunctionsAlertErrorCountThreshold: defaultFunctionsAlertErrorCountThreshold,
	}

	if v := getenv("S000_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := getenv("S000_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := getenv("S000_LOG_MODE"); v != "" {
		cfg.LogMode = v
	}
	if v := getenv("S000_DOMAIN"); v != "" {
		cfg.Domain = v
	}
	if v := getenv("S000_SHUTDOWN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.ShutdownTimeout = d
		}
	}
	if v := getenv("S000_MAX_INFLIGHT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxInFlight = n
		}
	}
	if v := getenv("S000_AUTH_MAX_SKEW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.AuthMaxSkew = d
		}
	}
	if v := getenv("S000_ADMIN_ACCESS_KEY"); v != "" {
		cfg.AdminAccessKey = v
	}
	if v := getenv("S000_ADMIN_SECRET_KEY"); v != "" {
		cfg.AdminSecretKey = v
	}
	if v := getenv("S000_METADATA_BACKEND"); v != "" {
		cfg.MetadataBackend = v
	}
	if v := getenv("S000_METADATA_DSN"); v != "" {
		cfg.MetadataDSN = v
	}
	if v := getenv("S000_VALKEY_ADDR"); v != "" {
		cfg.MetadataValkeyAddr = v
	}
	if v := getenv("S000_METADATA_CONNECT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.MetadataConnectTimeout = d
		}
	}
	if v := getenv("S000_LIFECYCLE_RULES"); v != "" {
		cfg.LifecycleRules = v
	}
	if v := getenv("S000_LIFECYCLE_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.LifecycleInterval = d
		}
	}
	if v := getenv("S000_LIFECYCLE_BATCH_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.LifecycleBatchSize = n
		}
	}
	if v := getenv("S000_LIFECYCLE_MAX_RETRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.LifecycleMaxRetries = n
		}
	}
	if v := getenv("S000_LIFECYCLE_BACKOFF"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			cfg.LifecycleBackoff = d
		}
	}
	if v := getenv("S000_LIFECYCLE_DRY_RUN"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.LifecycleDryRun = b
		}
	}
	if v := getenv("S000_METRICS_PATH"); v != "" {
		cfg.MetricsPath = v
	}
	if v := getenv("S000_TRACING_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.TracingEnabled = b
		}
	}
	if v := getenv("S000_HTTP_READ_HEADER_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.HTTPReadHeaderTimeout = d
		}
	}
	if v := getenv("S000_HTTP_READ_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.HTTPReadTimeout = d
		}
	}
	if v := getenv("S000_HTTP_WRITE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.HTTPWriteTimeout = d
		}
	}
	if v := getenv("S000_HTTP_IDLE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.HTTPIdleTimeout = d
		}
	}
	if v := getenv("S000_HTTP_MAX_HEADER_BYTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.HTTPMaxHeaderBytes = n
		}
	}
	if v := getenv("S000_HTTP_DISABLE_KEEPALIVE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.HTTPDisableKeepAlive = b
		}
	}
	if v := getenv("S000_HEAVY_OPS_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.HeavyOpsWorkers = n
		}
	}
	if v := getenv("S000_HEAVY_OPS_QUEUE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.HeavyOpsQueue = n
		}
	}
	if v := getenv("S000_TLS_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.TLSEnabled = b
		}
	}
	if v := getenv("S000_TLS_CERT_FILE"); v != "" {
		cfg.TLSCertFile = v
	}
	if v := getenv("S000_TLS_KEY_FILE"); v != "" {
		cfg.TLSKeyFile = v
	}
	if v := getenv("S000_TLS_MIN_VERSION"); v != "" {
		cfg.TLSMinVersion = v
	}
	if v := getenv("S000_AUTH_FAIL_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.AuthFailThreshold = n
		}
	}
	if v := getenv("S000_AUTH_FAIL_WINDOW"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.AuthFailWindow = d
		}
	}
	if v := getenv("S000_AUTH_BLOCK_DURATION"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.AuthBlockDuration = d
		}
	}
	if v := getenv("S000_UI_THEME"); v != "" {
		cfg.UITheme = strings.ToLower(strings.TrimSpace(v))
	}
	if v := getenv("S000_WEBSITE_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.WebsiteEnabled = b
		}
	}
	if v := getenv("S000_WEBSITE_ADDR"); v != "" {
		cfg.WebsiteAddr = v
	}
	if v := getenv("S000_WEBSITE_DOMAIN"); v != "" {
		cfg.WebsiteDomain = v
	}
	if v := getenv("S000_FUNCTIONS_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.FunctionsEnabled = b
		}
	}
	if v := getenv("S000_FUNCTIONS_DIR"); v != "" {
		cfg.FunctionsDir = v
	}
	if v := getenv("S000_FUNCTIONS_RUNTIME"); v != "" {
		cfg.FunctionsRuntime = strings.ToLower(strings.TrimSpace(v))
	}
	if v := getenv("S000_FUNCTIONS_MEMORY_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.FunctionsMemoryLimit = n
		}
	}
	if v := getenv("S000_FUNCTIONS_CPU_LIMIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.FunctionsCPULimit = d
		}
	}
	if v := getenv("S000_FUNCTIONS_NETWORK_ALLOW"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.FunctionsNetworkAllow = b
		}
	}
	if v := getenv("S000_FUNCTIONS_FS_ALLOW"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.FunctionsFSAllow = b
		}
	}
	if v := getenv("S000_FUNCTIONS_HOT_RELOAD"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.FunctionsHotReload = b
		}
	}
	if v := getenv("S000_FUNCTIONS_HTTP_PUBLIC"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.FunctionsHTTPPublic = b
		}
	}
	if v := getenv("S000_FUNCTIONS_HTTP_CORS_ALLOW_ORIGIN"); v != "" {
		cfg.FunctionsHTTPCORSAllowOrigin = v
	}
	if v := getenv("S000_FUNCTIONS_HTTP_CORS_ALLOW_METHODS"); v != "" {
		cfg.FunctionsHTTPCORSAllowMethods = v
	}
	if v := getenv("S000_FUNCTIONS_HTTP_CORS_ALLOW_HEADERS"); v != "" {
		cfg.FunctionsHTTPCORSAllowHeaders = v
	}
	if v := getenv("S000_FUNCTIONS_HTTP_CORS_EXPOSE_HEADERS"); v != "" {
		cfg.FunctionsHTTPCORSExposeHeaders = v
	}
	if v := getenv("S000_FUNCTIONS_HTTP_CORS_MAX_AGE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.FunctionsHTTPCORSMaxAge = n
		}
	}
	if v := getenv("S000_FUNCTIONS_HTTP_CORS_ALLOW_CREDENTIALS"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.FunctionsHTTPCORSAllowCredentials = b
		}
	}
	if v := getenv("S000_FUNCTIONS_RELOAD_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.FunctionsReloadInterval = d
		}
	}
	if v := getenv("S000_FUNCTIONS_RATE_LIMIT_PER_MINUTE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.FunctionsRateLimitPerMinute = n
		}
	}
	if v := getenv("S000_FUNCTIONS_MAX_CONCURRENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.FunctionsMaxConcurrent = n
		}
	}
	if v := getenv("S000_FUNCTIONS_DAILY_QUOTA"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.FunctionsDailyQuota = n
		}
	}
	if v := getenv("S000_FUNCTIONS_ALERT_ERROR_COUNT_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.FunctionsAlertErrorCountThreshold = n
		}
	}

	return cfg
}

// ValidateTLSEnabledSettings validates required TLS fields when TLS is enabled.
func ValidateTLSEnabledSettings(cfg Config) error {
	if !cfg.TLSEnabled {
		return nil
	}
	if strings.TrimSpace(cfg.TLSCertFile) == "" {
		return fmt.Errorf("tls cert file is required when tls is enabled")
	}
	if strings.TrimSpace(cfg.TLSKeyFile) == "" {
		return fmt.Errorf("tls key file is required when tls is enabled")
	}
	_, err := ParseTLSMinVersion(cfg.TLSMinVersion)
	return err
}

// ParseTLSMinVersion converts configured TLS min version text to tls constant.
func ParseTLSMinVersion(value string) (uint16, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", "1.2", "tls1.2", "tls12":
		return tls.VersionTLS12, nil
	case "1.3", "tls1.3", "tls13":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("unsupported tls min version %q", value)
	}
}
