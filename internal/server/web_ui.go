package server

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ds9labs.com/s000/internal/auth"
	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
	"ds9labs.com/s000/internal/observability"
)

//go:embed web/templates/*.html web/partials/*.html web/static/*
var webUIFS embed.FS

const (
	uiSessionCookie = "s000_ui_session"
	uiThemeCookie   = "s000_ui_theme"
	uiSessionTTL    = 12 * time.Hour

	defaultUIDashboardSSEInterval    = 2 * time.Second
	defaultUIDashboardTableInterval  = 10 * time.Second
	defaultUIDashboardObjectInterval = 10 * time.Second
	defaultUIDashboardTokenInterval  = 10 * time.Second
)

type webUI struct {
	templates *template.Template
	staticFS  fs.FS
	routeMap  []webRoute
	store     metadata.Store
	blob      *blob.Store
	metrics   *observability.Collector

	accessKey string
	secretKey string
	uiTheme   string
	pat       *auth.PersonalAccessTokenManager

	dashboardStatsSSEInterval time.Duration
	bucketsSSEInterval        time.Duration
	tokensSSEInterval         time.Duration
	objectsSSEInterval        time.Duration
	objectMetadataSSEInterval time.Duration

	mu       sync.Mutex
	sessions map[string]uiSession
}

type uiSession struct {
	ID        string
	CSRFToken string
	ExpiresAt time.Time
}

type webRoute struct {
	Path    string
	Purpose string
}

type webPageData struct {
	Title                 string
	Page                  string
	RouteMap              []webRoute
	BucketSummaries       []uiBucketSummary
	APIRequestStats       uiAPIRequestStats
	Buckets               []metadata.Bucket
	Bucket                *metadata.Bucket
	BucketName            string
	BucketWebsite         metadata.BucketWebsiteConfig
	BucketCORS            metadata.BucketCORSConfig
	BucketPolicy          metadata.BucketPolicy
	BucketPublicAccess    metadata.BucketPublicAccessBlock
	ObjectKey             string
	Object                *metadata.ObjectVersion
	Objects               []metadata.ObjectVersion
	Prefix                string
	Delimiter             string
	MaxKeys               int
	ContinuationToken     string
	NextContinuationToken string
	IsTruncated           bool
	CommonPrefixes        []string
	Breadcrumbs           []uiBreadcrumb
	GeneratedAt           string
	Error                 string
	Flash                 string
	CSRFToken             string
	Theme                 string
	ThemeOptions          []string
	GeneratedToken        string
	Tokens                []auth.IssuedPersonalAccessToken
}

type uiBucketSummary struct {
	Name             string
	Region           string
	VersioningStatus string
	ObjectCount      int
	TotalSizeBytes   int64
	CreatedAt        time.Time
}

type uiAPIRequestStats struct {
	TotalRequests      uint64
	ErrorRequests      uint64
	Error4xxRequests   uint64
	Error5xxRequests   uint64
	SuccessRatePct     float64
	BytesInTotal       uint64
	BytesOutTotal      uint64
	AvgLatencyMS       float64
	P95LatencyMS       float64
	TrendPoints        string
	TrendMax           uint64
	TrendWindow        int
	ErrorTrendPoints   string
	ErrorTrendMax      uint64
	ErrorRateTrendMax  float64
	ErrorRateTrendLast float64
}

type uiBreadcrumb struct {
	Label  string
	Prefix string
}

var supportedThemes = map[string]struct{}{
	"sysadmin90": {},
	"cyberpunk":  {},
	"solarized":  {},
	"dracula":    {},
}

var orderedThemes = []string{"sysadmin90", "cyberpunk", "solarized", "dracula"}

func newWebUI(opts Options) (*webUI, error) {
	funcs := template.FuncMap{
		"q":         url.QueryEscape,
		"p":         url.PathEscape,
		"hasSuffix": strings.HasSuffix,
	}
	tpls, err := template.New("web-ui").Funcs(funcs).ParseFS(webUIFS, "web/templates/*.html", "web/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse web ui templates: %w", err)
	}
	assets, err := fs.Sub(webUIFS, "web/static")
	if err != nil {
		return nil, fmt.Errorf("open web ui static assets: %w", err)
	}

	return &webUI{
		templates: tpls,
		staticFS:  assets,
		store:     opts.Metadata,
		blob:      opts.Blob,
		metrics:   opts.Metrics,
		accessKey: opts.UIAccessKey,
		secretKey: opts.UISecretKey,
		uiTheme:   normalizeTheme(opts.UITheme),
		pat:       opts.PATManager,
		dashboardStatsSSEInterval: positiveOrDefaultDuration(opts.UIDashboardStatsSSE, defaultUIDashboardSSEInterval),
		bucketsSSEInterval:        positiveOrDefaultDuration(opts.UIBucketsSSE, defaultUIDashboardTableInterval),
		tokensSSEInterval:         positiveOrDefaultDuration(opts.UITokensSSE, defaultUIDashboardTokenInterval),
		objectsSSEInterval:        positiveOrDefaultDuration(opts.UIObjectsSSE, defaultUIDashboardObjectInterval),
		objectMetadataSSEInterval: positiveOrDefaultDuration(opts.UIObjectMetadataSSE, defaultUIDashboardObjectInterval),
		sessions:  map[string]uiSession{},
		routeMap: []webRoute{
			{Path: "/app/login", Purpose: "operator sign-in shell"},
			{Path: "/app", Purpose: "dashboard and quick actions"},
			{Path: "/app/buckets", Purpose: "bucket list and creation"},
			{Path: "/app/buckets/:bucket", Purpose: "bucket details and navigation"},
			{Path: "/app/buckets/:bucket/objects", Purpose: "object browser"},
			{Path: "/app/buckets/:bucket/objects/:key", Purpose: "object details and metadata"},
			{Path: "/app/uploads", Purpose: "multipart upload monitoring"},
			{Path: "/app/settings", Purpose: "client settings and endpoint info"},
			{Path: "/app/tokens", Purpose: "personal access token management"},
			{Path: "/app/audit", Purpose: "recent security and destructive events"},
			{Path: "/app/partials/buckets", Purpose: "htmx bucket table fragment"},
			{Path: "/app/partials/objects", Purpose: "htmx object table fragment"},
			{Path: "/app/partials/object-metadata", Purpose: "htmx object metadata fragment"},
			{Path: "/app/partials/flash", Purpose: "htmx flash/toast fragment"},
			{Path: "/app/partials/pagination", Purpose: "htmx pagination controls fragment"},
			{Path: "/app/partials/tokens", Purpose: "htmx token table fragment"},
			{Path: "/app/partials/dashboard-stats", Purpose: "htmx dashboard stats fragment"},
			{Path: "/app/events/dashboard-stats", Purpose: "sse dashboard stats updates"},
			{Path: "/app/events/buckets", Purpose: "sse bucket table updates"},
			{Path: "/app/events/tokens", Purpose: "sse token table updates"},
			{Path: "/app/events/objects", Purpose: "sse object table updates"},
			{Path: "/app/events/object-metadata", Purpose: "sse object metadata updates"},
			{Path: "/app/actions/update-bucket-versioning", Purpose: "update bucket versioning setting"},
			{Path: "/app/actions/update-bucket-public-access", Purpose: "update public access block toggles"},
			{Path: "/app/actions/update-bucket-website", Purpose: "update bucket website settings"},
			{Path: "/app/actions/update-bucket-cors", Purpose: "update bucket CORS policy"},
			{Path: "/app/actions/update-bucket-policy", Purpose: "update bucket policy document"},
			{Path: "/app/actions/create-folder", Purpose: "create folder marker object"},
			{Path: "/app/actions/delete-folder-marker", Purpose: "delete folder marker object"},
			{Path: "/app/actions/tokens/create", Purpose: "create personal access token"},
			{Path: "/app/actions/tokens/revoke", Purpose: "revoke personal access token"},
		},
	}, nil
}

func webUIHandler(opts Options) http.Handler {
	ui, err := newWebUI(opts)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeS3Error(w, r, s3ErrorSpec{StatusCode: http.StatusInternalServerError, Code: "InternalError", Message: "Web UI initialization failed.", Resource: r.URL.Path})
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/app/login", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			ui.renderPage(w, r, "login", webPageData{Title: "Login", Page: "login", RouteMap: ui.routeMap, Error: r.URL.Query().Get("error")})
		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				http.Redirect(w, r, "/app/login?error=parse+failed", http.StatusSeeOther)
				return
			}
			if ui.accessKey == "" || ui.secretKey == "" {
				http.Redirect(w, r, "/app/login?error=ui+auth+is+not+configured", http.StatusSeeOther)
				return
			}
			if r.FormValue("access_key") != ui.accessKey || r.FormValue("secret_key") != ui.secretKey {
				http.Redirect(w, r, "/app/login?error=invalid+credentials", http.StatusSeeOther)
				return
			}
			s := ui.createSession()
			http.SetCookie(w, &http.Cookie{Name: uiSessionCookie, Value: s.ID, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: s.ExpiresAt, Secure: r.TLS != nil})
			http.Redirect(w, r, "/app", http.StatusSeeOther)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/app/actions/create-bucket", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !ui.validateCSRF(r, s) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/app/buckets?flash=parse+failed", http.StatusSeeOther)
			return
		}
		name := strings.TrimSpace(r.FormValue("bucket"))
		if name == "" {
			http.Redirect(w, r, "/app/buckets?flash=bucket+name+is+required", http.StatusSeeOther)
			return
		}
		if ui.store == nil {
			http.Redirect(w, r, "/app/buckets?flash=metadata+store+unavailable", http.StatusSeeOther)
			return
		}
		err := ui.store.CreateBucket(r.Context(), metadata.Bucket{Name: name, CreatedAt: time.Now().UTC(), Region: "us-east-1", VersioningStatus: "Suspended"})
		if err != nil {
			http.Redirect(w, r, "/app/buckets?flash=create+bucket+failed", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/app/buckets?flash=bucket+created", http.StatusSeeOther)
	})

	mux.HandleFunc("/app/actions/update-bucket-versioning", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !ui.validateCSRF(r, s) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/app/buckets?flash=parse+failed", http.StatusSeeOther)
			return
		}
		bucket := strings.TrimSpace(r.FormValue("bucket"))
		status := strings.TrimSpace(r.FormValue("versioning"))
		if bucket == "" || (status != "Enabled" && status != "Suspended") {
			http.Redirect(w, r, "/app/buckets?flash=invalid+versioning+settings", http.StatusSeeOther)
			return
		}
		if ui.store == nil {
			http.Redirect(w, r, "/app/buckets?flash=metadata+store+unavailable", http.StatusSeeOther)
			return
		}
		if err := ui.store.UpdateBucketVersioning(r.Context(), bucket, status); err != nil {
			http.Redirect(w, r, "/app/buckets/"+url.PathEscape(bucket)+"?flash=update+versioning+failed", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/app/buckets/"+url.PathEscape(bucket)+"?flash=versioning+updated", http.StatusSeeOther)
	})

	mux.HandleFunc("/app/actions/update-bucket-public-access", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !ui.validateCSRF(r, s) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/app/buckets?flash=parse+failed", http.StatusSeeOther)
			return
		}
		bucket := strings.TrimSpace(r.FormValue("bucket"))
		if bucket == "" {
			http.Redirect(w, r, "/app/buckets?flash=bucket+name+is+required", http.StatusSeeOther)
			return
		}
		if ui.store == nil {
			http.Redirect(w, r, "/app/buckets?flash=metadata+store+unavailable", http.StatusSeeOther)
			return
		}
		cfg := metadata.BucketPublicAccessBlock{
			Bucket:                bucket,
			BlockPublicACLs:       r.FormValue("block_public_acls") == "on",
			IgnorePublicACLs:      r.FormValue("ignore_public_acls") == "on",
			BlockPublicPolicy:     r.FormValue("block_public_policy") == "on",
			RestrictPublicBuckets: r.FormValue("restrict_public_buckets") == "on",
		}
		if err := ui.store.PutBucketPublicAccessBlock(r.Context(), cfg); err != nil {
			http.Redirect(w, r, "/app/buckets/"+url.PathEscape(bucket)+"?flash=public+access+update+failed", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/app/buckets/"+url.PathEscape(bucket)+"?flash=public+access+updated", http.StatusSeeOther)
	})

	mux.HandleFunc("/app/actions/update-bucket-website", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !ui.validateCSRF(r, s) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/app/buckets?flash=parse+failed", http.StatusSeeOther)
			return
		}
		bucket := strings.TrimSpace(r.FormValue("bucket"))
		if bucket == "" {
			http.Redirect(w, r, "/app/buckets?flash=bucket+name+is+required", http.StatusSeeOther)
			return
		}
		if ui.store == nil {
			http.Redirect(w, r, "/app/buckets?flash=metadata+store+unavailable", http.StatusSeeOther)
			return
		}
		cfg := metadata.BucketWebsiteConfig{
			Bucket:              bucket,
			IndexDocument:       strings.TrimSpace(r.FormValue("index_document")),
			ErrorDocument:       strings.TrimSpace(r.FormValue("error_document")),
			RedirectAllHost:     strings.TrimSpace(r.FormValue("redirect_all_host")),
			RedirectAllProtocol: strings.TrimSpace(r.FormValue("redirect_all_protocol")),
			Enabled:             r.FormValue("website_enabled") == "on",
			PublicRead:          r.FormValue("website_public_read") == "on",
		}
		if err := ui.store.PutBucketWebsite(r.Context(), cfg); err != nil {
			http.Redirect(w, r, "/app/buckets/"+url.PathEscape(bucket)+"?flash=website+update+failed", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/app/buckets/"+url.PathEscape(bucket)+"?flash=website+updated", http.StatusSeeOther)
	})

	mux.HandleFunc("/app/actions/update-bucket-cors", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !ui.validateCSRF(r, s) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/app/buckets?flash=parse+failed", http.StatusSeeOther)
			return
		}
		bucket := strings.TrimSpace(r.FormValue("bucket"))
		if bucket == "" {
			http.Redirect(w, r, "/app/buckets?flash=bucket+name+is+required", http.StatusSeeOther)
			return
		}
		if ui.store == nil {
			http.Redirect(w, r, "/app/buckets?flash=metadata+store+unavailable", http.StatusSeeOther)
			return
		}
		maxAge, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("max_age_seconds")))
		cfg := metadata.BucketCORSConfig{
			Bucket:         bucket,
			AllowedOrigins: strings.TrimSpace(r.FormValue("allowed_origins")),
			AllowedMethods: strings.TrimSpace(r.FormValue("allowed_methods")),
			AllowedHeaders: strings.TrimSpace(r.FormValue("allowed_headers")),
			ExposeHeaders:  strings.TrimSpace(r.FormValue("expose_headers")),
			MaxAgeSeconds:  maxAge,
			Enabled:        r.FormValue("cors_enabled") == "on",
		}
		if err := ui.store.PutBucketCORS(r.Context(), cfg); err != nil {
			http.Redirect(w, r, "/app/buckets/"+url.PathEscape(bucket)+"?flash=cors+update+failed", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/app/buckets/"+url.PathEscape(bucket)+"?flash=cors+updated", http.StatusSeeOther)
	})

	mux.HandleFunc("/app/actions/update-bucket-policy", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !ui.validateCSRF(r, s) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/app/buckets?flash=parse+failed", http.StatusSeeOther)
			return
		}
		bucket := strings.TrimSpace(r.FormValue("bucket"))
		if bucket == "" {
			http.Redirect(w, r, "/app/buckets?flash=bucket+name+is+required", http.StatusSeeOther)
			return
		}
		if ui.store == nil {
			http.Redirect(w, r, "/app/buckets?flash=metadata+store+unavailable", http.StatusSeeOther)
			return
		}
		cfg := metadata.BucketPolicy{
			Bucket:   bucket,
			Document: strings.TrimSpace(r.FormValue("policy_document")),
			Enabled:  r.FormValue("policy_enabled") == "on",
		}
		if err := ui.store.PutBucketPolicy(r.Context(), cfg); err != nil {
			http.Redirect(w, r, "/app/buckets/"+url.PathEscape(bucket)+"?flash=policy+update+failed", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/app/buckets/"+url.PathEscape(bucket)+"?flash=policy+updated", http.StatusSeeOther)
	})

	mux.HandleFunc("/app/actions/upload-object", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			http.Redirect(w, r, "/app/buckets?flash=upload+parse+failed", http.StatusSeeOther)
			return
		}
		if !ui.validateCSRF(r, s) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		bucket := strings.TrimSpace(r.FormValue("bucket"))
		key := strings.TrimSpace(r.FormValue("key"))
		prefix := normalizeUIPrefix(r.FormValue("prefix"))
		delimiter := normalizeUIDelimiter(r.FormValue("delimiter"))
		if bucket == "" || key == "" {
			http.Redirect(w, r, "/app/buckets?flash=bucket+and+key+required", http.StatusSeeOther)
			return
		}
		if ui.store == nil || ui.blob == nil {
			http.Redirect(w, r, "/app/buckets?flash=storage+unavailable", http.StatusSeeOther)
			return
		}
		f, fh, err := r.FormFile("file")
		if err != nil {
			http.Redirect(w, r, objectBrowserURL(bucket, prefix, delimiter, "file+required"), http.StatusSeeOther)
			return
		}
		defer func() { _ = f.Close() }()

		b, err := ui.store.GetBucket(r.Context(), bucket)
		if err != nil {
			http.Redirect(w, r, "/app/buckets?flash=bucket+not+found", http.StatusSeeOther)
			return
		}
		versionID := "null"
		if b.VersioningStatus == "Enabled" {
			versionID = newVersionID()
		}
		ref := blob.ObjectRef{Bucket: bucket, Key: key, VersionID: versionID}
		meta, err := ui.blob.WriteObject(r.Context(), ref, f)
		if err != nil {
			http.Redirect(w, r, objectBrowserURL(bucket, prefix, delimiter, "upload+failed"), http.StatusSeeOther)
			return
		}
		contentType := strings.TrimSpace(fh.Header.Get("Content-Type"))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		err = ui.store.PutObjectVersion(r.Context(), metadata.ObjectVersion{Bucket: bucket, Key: key, VersionID: versionID, Size: meta.Size, ETag: meta.MD5Hex, ChecksumSHA256: meta.SHA256, StoragePath: meta.Path, Metadata: map[string]string{"content-type": contentType}, CreatedAt: meta.CreatedAt})
		if err != nil {
			_ = ui.blob.DeleteObject(r.Context(), ref, true)
			http.Redirect(w, r, objectBrowserURL(bucket, prefix, delimiter, "metadata+write+failed"), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, objectBrowserURL(bucket, prefix, delimiter, "upload+complete"), http.StatusSeeOther)
	})

	mux.HandleFunc("/app/actions/create-folder", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !ui.validateCSRF(r, s) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/app/buckets?flash=parse+failed", http.StatusSeeOther)
			return
		}
		bucket := strings.TrimSpace(r.FormValue("bucket"))
		prefix := normalizeUIPrefix(r.FormValue("prefix"))
		delimiter := normalizeUIDelimiter(r.FormValue("delimiter"))
		folder := strings.TrimSpace(r.FormValue("folder"))
		if bucket == "" || folder == "" {
			http.Redirect(w, r, "/app/buckets?flash=bucket+and+folder+required", http.StatusSeeOther)
			return
		}
		key := normalizeFolderMarkerKey(prefix, folder)
		if key == "" {
			http.Redirect(w, r, objectBrowserURL(bucket, prefix, delimiter, "invalid+folder+name"), http.StatusSeeOther)
			return
		}
		if ui.store == nil || ui.blob == nil {
			http.Redirect(w, r, "/app/buckets?flash=storage+unavailable", http.StatusSeeOther)
			return
		}
		b, err := ui.store.GetBucket(r.Context(), bucket)
		if err != nil {
			http.Redirect(w, r, "/app/buckets?flash=bucket+not+found", http.StatusSeeOther)
			return
		}
		versionID := "null"
		if b.VersioningStatus == "Enabled" {
			versionID = newVersionID()
		}
		ref := blob.ObjectRef{Bucket: bucket, Key: key, VersionID: versionID}
		meta, err := ui.blob.WriteObject(r.Context(), ref, strings.NewReader(""))
		if err != nil {
			http.Redirect(w, r, objectBrowserURL(bucket, prefix, delimiter, "folder+create+failed"), http.StatusSeeOther)
			return
		}
		err = ui.store.PutObjectVersion(r.Context(), metadata.ObjectVersion{Bucket: bucket, Key: key, VersionID: versionID, Size: meta.Size, ETag: meta.MD5Hex, ChecksumSHA256: meta.SHA256, StoragePath: meta.Path, Metadata: map[string]string{"content-type": "application/x-directory"}, CreatedAt: meta.CreatedAt})
		if err != nil {
			_ = ui.blob.DeleteObject(r.Context(), ref, true)
			http.Redirect(w, r, objectBrowserURL(bucket, prefix, delimiter, "metadata+write+failed"), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, objectBrowserURL(bucket, prefix, delimiter, "folder+created"), http.StatusSeeOther)
	})

	mux.HandleFunc("/app/actions/delete-object", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !ui.validateCSRF(r, s) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/app/buckets?flash=parse+failed", http.StatusSeeOther)
			return
		}
		bucket := strings.TrimSpace(r.FormValue("bucket"))
		key := strings.TrimSpace(r.FormValue("key"))
		prefix := normalizeUIPrefix(r.FormValue("prefix"))
		delimiter := normalizeUIDelimiter(r.FormValue("delimiter"))
		if bucket == "" || key == "" {
			http.Redirect(w, r, "/app/buckets?flash=bucket+and+key+required", http.StatusSeeOther)
			return
		}
		if ui.store == nil {
			http.Redirect(w, r, "/app/buckets?flash=metadata+store+unavailable", http.StatusSeeOther)
			return
		}
		b, err := ui.store.GetBucket(r.Context(), bucket)
		if err != nil {
			http.Redirect(w, r, "/app/buckets?flash=bucket+not+found", http.StatusSeeOther)
			return
		}
		if b.VersioningStatus == "Enabled" {
			_ = ui.store.DeleteObject(r.Context(), bucket, key, newVersionID(), time.Now().UTC())
			http.Redirect(w, r, objectBrowserURL(bucket, prefix, delimiter, "delete+marker+created"), http.StatusSeeOther)
			return
		}
		removed, err := ui.store.DeleteAllObjectVersions(r.Context(), bucket, key)
		if err != nil && err != metadata.ErrNotFound {
			http.Redirect(w, r, objectBrowserURL(bucket, prefix, delimiter, "delete+failed"), http.StatusSeeOther)
			return
		}
		if ui.blob != nil {
			for _, obj := range removed {
				_ = ui.blob.DeleteObject(r.Context(), blob.ObjectRef{Bucket: obj.Bucket, Key: obj.Key, VersionID: obj.VersionID}, true)
			}
		}
		http.Redirect(w, r, objectBrowserURL(bucket, prefix, delimiter, "object+deleted"), http.StatusSeeOther)
	})

	mux.HandleFunc("/app/actions/delete-folder-marker", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !ui.validateCSRF(r, s) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/app/buckets?flash=parse+failed", http.StatusSeeOther)
			return
		}
		bucket := strings.TrimSpace(r.FormValue("bucket"))
		key := strings.TrimSpace(r.FormValue("key"))
		prefix := normalizeUIPrefix(r.FormValue("prefix"))
		delimiter := normalizeUIDelimiter(r.FormValue("delimiter"))
		if bucket == "" || key == "" {
			http.Redirect(w, r, "/app/buckets?flash=bucket+and+key+required", http.StatusSeeOther)
			return
		}
		if !strings.HasSuffix(key, "/") {
			http.Redirect(w, r, objectBrowserURL(bucket, prefix, delimiter, "folder+marker+key+must+end+with+slash"), http.StatusSeeOther)
			return
		}
		if ui.store == nil {
			http.Redirect(w, r, "/app/buckets?flash=metadata+store+unavailable", http.StatusSeeOther)
			return
		}
		b, err := ui.store.GetBucket(r.Context(), bucket)
		if err != nil {
			http.Redirect(w, r, "/app/buckets?flash=bucket+not+found", http.StatusSeeOther)
			return
		}
		if b.VersioningStatus == "Enabled" {
			_ = ui.store.DeleteObject(r.Context(), bucket, key, newVersionID(), time.Now().UTC())
			http.Redirect(w, r, objectBrowserURL(bucket, prefix, delimiter, "folder+delete+marker+created"), http.StatusSeeOther)
			return
		}
		removed, err := ui.store.DeleteAllObjectVersions(r.Context(), bucket, key)
		if err != nil && err != metadata.ErrNotFound {
			http.Redirect(w, r, objectBrowserURL(bucket, prefix, delimiter, "folder+marker+delete+failed"), http.StatusSeeOther)
			return
		}
		if ui.blob != nil {
			for _, obj := range removed {
				_ = ui.blob.DeleteObject(r.Context(), blob.ObjectRef{Bucket: obj.Bucket, Key: obj.Key, VersionID: obj.VersionID}, true)
			}
		}
		http.Redirect(w, r, objectBrowserURL(bucket, prefix, delimiter, "folder+marker+deleted"), http.StatusSeeOther)
	})

	mux.HandleFunc("/app/actions/theme", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !ui.validateCSRF(r, s) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		theme := normalizeTheme(r.FormValue("theme"))
		if _, ok := supportedThemes[theme]; !ok {
			http.Redirect(w, r, "/app/settings?flash=invalid+theme", http.StatusSeeOther)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: uiThemeCookie, Value: theme, Path: "/", HttpOnly: false, SameSite: http.SameSiteLaxMode, Expires: time.Now().UTC().Add(365 * 24 * time.Hour), Secure: r.TLS != nil})
		http.Redirect(w, r, "/app/settings?flash=theme+updated", http.StatusSeeOther)
	})

	mux.HandleFunc("/app/actions/tokens/create", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !ui.validateCSRF(r, s) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			ui.respondActionResult(w, r, false, "parse failed", "/app/tokens", "")
			return
		}
		if ui.pat == nil {
			ui.respondActionResult(w, r, false, "token management unavailable", "/app/tokens", "")
			return
		}
		subject := strings.TrimSpace(r.FormValue("subject"))
		label := strings.TrimSpace(r.FormValue("label"))
		ttlRaw := strings.TrimSpace(r.FormValue("ttl"))
		if subject == "" {
			ui.respondActionResult(w, r, false, "subject is required", "/app/tokens", "")
			return
		}
		ttl := 24 * time.Hour
		if ttlRaw != "" {
			parsed, err := time.ParseDuration(ttlRaw)
			if err != nil || parsed <= 0 {
				ui.respondActionResult(w, r, false, "invalid ttl duration", "/app/tokens", "")
				return
			}
			ttl = parsed
		}
		token, _, err := ui.pat.Issue(subject, ttl, label)
		if err != nil {
			ui.respondActionResult(w, r, false, "token creation failed", "/app/tokens", "")
			return
		}
		if isHTMXRequest(r) {
			w.Header().Set("HX-Trigger", "tokens-changed")
			ui.renderPartial(w, r, "partials/token_create_result", webPageData{Flash: "token created", GeneratedToken: token})
			return
		}
		tokens := ui.listTokens()
		ui.renderPage(w, r, "tokens", webPageData{Title: "Tokens", Page: "tokens", RouteMap: ui.routeMap, Tokens: tokens, CSRFToken: s.CSRFToken, Flash: "token created", GeneratedToken: token})
	})

	mux.HandleFunc("/app/actions/tokens/revoke", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !ui.validateCSRF(r, s) {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			ui.respondActionResult(w, r, false, "parse failed", "/app/tokens", "")
			return
		}
		if ui.pat == nil {
			ui.respondActionResult(w, r, false, "token management unavailable", "/app/tokens", "")
			return
		}
		tokenID := strings.TrimSpace(r.FormValue("token_id"))
		if tokenID == "" {
			ui.respondActionResult(w, r, false, "token id is required", "/app/tokens", "")
			return
		}
		if err := ui.pat.Revoke(tokenID); err != nil {
			ui.respondActionResult(w, r, false, "token revoke failed", "/app/tokens", "")
			return
		}
		ui.respondActionResult(w, r, true, "token revoked", "/app/tokens", "tokens-changed")
	})

	mux.HandleFunc("/app/actions/download-object", func(w http.ResponseWriter, r *http.Request) {
		_, ok := ui.requireSession(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		bucket := strings.TrimSpace(r.URL.Query().Get("bucket"))
		key := strings.TrimSpace(r.URL.Query().Get("key"))
		if bucket == "" || key == "" {
			http.Redirect(w, r, "/app/buckets?flash=bucket+and+key+required", http.StatusSeeOther)
			return
		}
		if ui.store == nil || ui.blob == nil {
			http.Redirect(w, r, "/app/buckets?flash=storage+unavailable", http.StatusSeeOther)
			return
		}
		obj, err := ui.store.GetLatestObjectVersion(r.Context(), bucket, key)
		if err != nil {
			http.Redirect(w, r, "/app/buckets/"+bucket+"/objects?flash=object+not+found", http.StatusSeeOther)
			return
		}
		meta := blob.ObjectMeta{Ref: blob.ObjectRef{Bucket: obj.Bucket, Key: obj.Key, VersionID: obj.VersionID}, Path: obj.StoragePath, Size: obj.Size, SHA256: obj.ChecksumSHA256, MD5Hex: obj.ETag, CreatedAt: obj.CreatedAt}
		filename := path.Base(key)
		w.Header().Set("Content-Disposition", contentDispositionAttachment(filename))
		contentType := "application/octet-stream"
		if obj.Metadata != nil {
			if v := strings.TrimSpace(obj.Metadata["content-type"]); v != "" {
				contentType = v
			}
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("ETag", quotedETag(obj.ETag))
		w.Header().Set("x-amz-checksum-sha256", obj.ChecksumSHA256)
		if _, err := ui.blob.ReadObject(r.Context(), meta, nil, w); err != nil {
			http.Redirect(w, r, "/app/buckets/"+bucket+"/objects/"+key+"?flash=download+failed", http.StatusSeeOther)
			return
		}
	})

	mux.HandleFunc("/app", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		data := ui.loadDashboardData(r)
		data.Title = "Dashboard"
		data.Page = "dashboard"
		data.RouteMap = ui.routeMap
		data.CSRFToken = s.CSRFToken
		data.Flash = r.URL.Query().Get("flash")
		ui.renderPage(w, r, "dashboard", data)
	})
	mux.HandleFunc("/app/buckets", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		buckets, loadErr := ui.loadBuckets(r)
		data := webPageData{Title: "Buckets", Page: "buckets", RouteMap: ui.routeMap, Buckets: buckets, GeneratedAt: time.Now().UTC().Format(time.RFC3339), CSRFToken: s.CSRFToken, Flash: r.URL.Query().Get("flash")}
		if loadErr != nil {
			data.Error = loadErr.Error()
		}
		ui.renderPage(w, r, "buckets", data)
	})
	mux.HandleFunc("/app/uploads", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		buckets, loadErr := ui.loadBuckets(r)
		data := webPageData{Title: "Uploads", Page: "uploads", RouteMap: ui.routeMap, Buckets: buckets, CSRFToken: s.CSRFToken}
		if loadErr != nil {
			data.Error = loadErr.Error()
		}
		ui.renderPage(w, r, "uploads", data)
	})
	mux.HandleFunc("/app/settings", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		ui.renderPage(w, r, "settings", webPageData{Title: "Settings", Page: "settings", RouteMap: ui.routeMap, CSRFToken: s.CSRFToken, Flash: r.URL.Query().Get("flash")})
	})
	mux.HandleFunc("/app/tokens", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		data := webPageData{Title: "Tokens", Page: "tokens", RouteMap: ui.routeMap, CSRFToken: s.CSRFToken, Flash: r.URL.Query().Get("flash"), GeneratedToken: r.URL.Query().Get("token")}
		if ui.pat == nil {
			data.Error = "token management unavailable"
		} else {
			data.Tokens = ui.listTokens()
		}
		ui.renderPage(w, r, "tokens", data)
	})
	mux.HandleFunc("/app/audit", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		ui.renderPage(w, r, "audit", webPageData{Title: "Audit", Page: "audit", RouteMap: ui.routeMap, CSRFToken: s.CSRFToken})
	})
	mux.HandleFunc("/app/buckets/", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		bucket, key, mode := parseUIBucketObjectPath(r.URL.Path)
		switch mode {
		case "bucket":
			data := webPageData{Title: "Bucket", Page: "bucket-detail", RouteMap: ui.routeMap, BucketName: bucket, CSRFToken: s.CSRFToken, Flash: r.URL.Query().Get("flash")}
			if ui.store == nil {
				data.Error = "metadata store unavailable"
				ui.renderPage(w, r, "bucket_detail", data)
				return
			}
			b, err := ui.store.GetBucket(r.Context(), bucket)
			if err != nil {
				if errors.Is(err, metadata.ErrNotFound) {
					http.NotFound(w, r)
					return
				}
				data.Error = err.Error()
				ui.renderPage(w, r, "bucket_detail", data)
				return
			}
			data.Bucket = &b
			if cfg, err := ui.store.GetBucketWebsite(r.Context(), bucket); err == nil {
				data.BucketWebsite = cfg
			} else {
				data.BucketWebsite = metadata.BucketWebsiteConfig{Bucket: bucket}
			}
			if cfg, err := ui.store.GetBucketCORS(r.Context(), bucket); err == nil {
				data.BucketCORS = cfg
			} else {
				data.BucketCORS = metadata.BucketCORSConfig{Bucket: bucket}
			}
			if cfg, err := ui.store.GetBucketPolicy(r.Context(), bucket); err == nil {
				data.BucketPolicy = cfg
			} else {
				data.BucketPolicy = metadata.BucketPolicy{Bucket: bucket}
			}
			if cfg, err := ui.store.GetBucketPublicAccessBlock(r.Context(), bucket); err == nil {
				data.BucketPublicAccess = cfg
			} else {
				data.BucketPublicAccess = metadata.BucketPublicAccessBlock{Bucket: bucket}
			}
			ui.renderPage(w, r, "bucket_detail", data)
		case "objects":
			data, listErr := ui.listObjectsData(r, bucket)
			data.Title = "Objects"
			data.Page = "objects"
			data.RouteMap = ui.routeMap
			data.CSRFToken = s.CSRFToken
			data.Flash = r.URL.Query().Get("flash")
			if listErr != nil {
				data.Error = listErr.Error()
			}
			ui.renderPage(w, r, "objects", data)
		case "object":
			obj, err := ui.loadObject(r, bucket, key)
			data := webPageData{Title: "Object", Page: "object-detail", RouteMap: ui.routeMap, BucketName: bucket, ObjectKey: key, Object: obj, CSRFToken: s.CSRFToken}
			if err != nil {
				data.Error = err.Error()
			}
			ui.renderPage(w, r, "object_detail", data)
		default:
			http.NotFound(w, r)
		}
	})

	mux.HandleFunc("/app/partials/buckets", func(w http.ResponseWriter, r *http.Request) {
		_, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		buckets, err := ui.loadBuckets(r)
		data := webPageData{Buckets: buckets, GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
		if err != nil {
			data.Error = err.Error()
		}
		ui.renderPartial(w, r, "partials/buckets_table", data)
	})
	mux.HandleFunc("/app/partials/objects", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		bucket := r.URL.Query().Get("bucket")
		if bucket == "" {
			ui.renderPartial(w, r, "partials/flash", webPageData{Error: "bucket query is required"})
			return
		}
		data, err := ui.listObjectsData(r, bucket)
		data.CSRFToken = s.CSRFToken
		if err != nil {
			data.Error = err.Error()
		}
		ui.renderPartial(w, r, "partials/objects_table", data)
	})
	mux.HandleFunc("/app/partials/object-metadata", func(w http.ResponseWriter, r *http.Request) {
		_, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		bucket := r.URL.Query().Get("bucket")
		key := r.URL.Query().Get("key")
		if bucket == "" || key == "" {
			ui.renderPartial(w, r, "partials/flash", webPageData{Error: "bucket and key queries are required"})
			return
		}
		obj, err := ui.loadObject(r, bucket, key)
		data := webPageData{BucketName: bucket, ObjectKey: key, Object: obj}
		if err != nil {
			data.Error = err.Error()
		}
		ui.renderPartial(w, r, "partials/object_metadata", data)
	})
	mux.HandleFunc("/app/partials/flash", func(w http.ResponseWriter, r *http.Request) {
		_, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		ui.renderPartial(w, r, "partials/flash", webPageData{GeneratedAt: time.Now().UTC().Format(time.RFC3339), Error: r.URL.Query().Get("error")})
	})
	mux.HandleFunc("/app/partials/pagination", func(w http.ResponseWriter, r *http.Request) {
		_, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		bucket := r.URL.Query().Get("bucket")
		data, _ := ui.listObjectsData(r, bucket)
		ui.renderPartial(w, r, "partials/pagination", data)
	})
	mux.HandleFunc("/app/partials/tokens", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		if ui.pat == nil {
			ui.renderPartial(w, r, "partials/flash", webPageData{Error: "token management unavailable"})
			return
		}
		ui.renderPartial(w, r, "partials/tokens_table", webPageData{Tokens: ui.listTokens(), CSRFToken: s.CSRFToken})
	})
	mux.HandleFunc("/app/partials/dashboard-stats", func(w http.ResponseWriter, r *http.Request) {
		_, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		ui.renderPartial(w, r, "partials/dashboard_api_stats", webPageData{APIRequestStats: ui.loadAPIRequestStats()})
	})
	mux.HandleFunc("/app/events/dashboard-stats", func(w http.ResponseWriter, r *http.Request) {
		_, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		ui.streamSSE(w, r, "dashboard-stats", ui.dashboardStatsSSEInterval, func() (string, error) {
			return ui.renderPartialToString(r, "partials/dashboard_api_stats", webPageData{APIRequestStats: ui.loadAPIRequestStats()})
		})
	})
	mux.HandleFunc("/app/events/buckets", func(w http.ResponseWriter, r *http.Request) {
		_, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		ui.streamSSE(w, r, "buckets-updated", ui.bucketsSSEInterval, func() (string, error) {
			buckets, err := ui.loadBuckets(r)
			data := webPageData{Buckets: buckets, GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
			if err != nil {
				data.Error = err.Error()
			}
			return ui.renderPartialToString(r, "partials/buckets_table", data)
		})
	})
	mux.HandleFunc("/app/events/tokens", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		if ui.pat == nil {
			http.Error(w, "token management unavailable", http.StatusServiceUnavailable)
			return
		}
		ui.streamSSE(w, r, "tokens-updated", ui.tokensSSEInterval, func() (string, error) {
			return ui.renderPartialToString(r, "partials/tokens_table", webPageData{Tokens: ui.listTokens(), CSRFToken: s.CSRFToken})
		})
	})
	mux.HandleFunc("/app/events/objects", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		bucket := strings.TrimSpace(r.URL.Query().Get("bucket"))
		if bucket == "" {
			http.Error(w, "bucket query is required", http.StatusBadRequest)
			return
		}
		ui.streamSSE(w, r, "objects-updated", ui.objectsSSEInterval, func() (string, error) {
			data, err := ui.listObjectsData(r, bucket)
			data.CSRFToken = s.CSRFToken
			if err != nil {
				data.Error = err.Error()
			}
			return ui.renderPartialToString(r, "partials/objects_table", data)
		})
	})
	mux.HandleFunc("/app/events/object-metadata", func(w http.ResponseWriter, r *http.Request) {
		_, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		bucket := strings.TrimSpace(r.URL.Query().Get("bucket"))
		key := strings.TrimSpace(r.URL.Query().Get("key"))
		if bucket == "" || key == "" {
			http.Error(w, "bucket and key queries are required", http.StatusBadRequest)
			return
		}
		ui.streamSSE(w, r, "object-metadata-updated", ui.objectMetadataSSEInterval, func() (string, error) {
			obj, err := ui.loadObject(r, bucket, key)
			data := webPageData{BucketName: bucket, ObjectKey: key, Object: obj}
			if err != nil {
				data.Error = err.Error()
			}
			return ui.renderPartialToString(r, "partials/object_metadata", data)
		})
	})

	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(ui.staticFS))))

	return mux
}

func (u *webUI) createSession() uiSession {
	id := randomHex(16)
	s := uiSession{ID: id, CSRFToken: randomHex(16), ExpiresAt: time.Now().UTC().Add(uiSessionTTL)}
	u.mu.Lock()
	u.sessions[id] = s
	u.mu.Unlock()
	return s
}

func (u *webUI) requireSession(w http.ResponseWriter, r *http.Request) (uiSession, bool) {
	cookie, err := r.Cookie(uiSessionCookie)
	if err != nil {
		http.Redirect(w, r, "/app/login", http.StatusSeeOther)
		return uiSession{}, false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	s, ok := u.sessions[cookie.Value]
	if !ok || time.Now().UTC().After(s.ExpiresAt) {
		delete(u.sessions, cookie.Value)
		http.Redirect(w, r, "/app/login", http.StatusSeeOther)
		return uiSession{}, false
	}
	return s, true
}

func (u *webUI) validateCSRF(r *http.Request, s uiSession) bool {
	if strings.EqualFold(r.Header.Get("HX-Request"), "true") {
		token := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
		return token != "" && token == s.CSRFToken
	}
	token := strings.TrimSpace(r.FormValue("_csrf"))
	return token != "" && token == s.CSRFToken
}

func isHTMXRequest(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("HX-Request")), "true")
}

func (u *webUI) respondActionResult(w http.ResponseWriter, r *http.Request, success bool, flash string, redirectPath string, trigger string) {
	if isHTMXRequest(r) {
		if trigger != "" {
			w.Header().Set("HX-Trigger", trigger)
		}
		data := webPageData{}
		if success {
			data.Flash = flash
		} else {
			w.WriteHeader(http.StatusBadRequest)
			data.Error = flash
		}
		u.renderPartial(w, r, "partials/flash", data)
		return
	}
	if flash == "" {
		http.Redirect(w, r, redirectPath, http.StatusSeeOther)
		return
	}
	sep := "?"
	if strings.Contains(redirectPath, "?") {
		sep = "&"
	}
	if success {
		http.Redirect(w, r, redirectPath+sep+"flash="+url.QueryEscape(flash), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, redirectPath+sep+"flash="+url.QueryEscape(flash), http.StatusSeeOther)
}

func parseUIBucketObjectPath(path string) (bucket, key, mode string) {
	trimmed := strings.TrimPrefix(path, "/app/buckets/")
	parts := strings.Split(trimmed, "/")
	if len(parts) == 1 && parts[0] != "" {
		return parts[0], "", "bucket"
	}
	if len(parts) == 2 && parts[1] == "objects" {
		return parts[0], "", "objects"
	}
	if len(parts) >= 3 && parts[1] == "objects" {
		return parts[0], strings.Join(parts[2:], "/"), "object"
	}
	return "", "", ""
}

func (u *webUI) loadBuckets(r *http.Request) ([]metadata.Bucket, error) {
	if u.store == nil {
		return nil, fmt.Errorf("metadata store unavailable")
	}
	buckets, err := u.store.ListBuckets(r.Context())
	if err != nil {
		return nil, err
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Name < buckets[j].Name })
	return buckets, nil
}

func (u *webUI) listTokens() []auth.IssuedPersonalAccessToken {
	if u.pat == nil {
		return nil
	}
	return u.pat.List()
}

func (u *webUI) loadDashboardData(r *http.Request) webPageData {
	data := webPageData{}
	buckets, err := u.loadBuckets(r)
	if err != nil {
		data.Error = err.Error()
		data.BucketSummaries = []uiBucketSummary{}
	} else {
		data.BucketSummaries = make([]uiBucketSummary, 0, len(buckets))
		for _, bucket := range buckets {
			objects, listErr := u.store.ListObjects(r.Context(), bucket.Name)
			if listErr != nil {
				if data.Error == "" {
					data.Error = listErr.Error()
				}
				continue
			}
			summary := uiBucketSummary{
				Name:             bucket.Name,
				Region:           bucket.Region,
				VersioningStatus: bucket.VersioningStatus,
				CreatedAt:        bucket.CreatedAt,
			}
			for _, obj := range objects {
				summary.ObjectCount++
				summary.TotalSizeBytes += obj.Size
			}
			data.BucketSummaries = append(data.BucketSummaries, summary)
		}
	}

	if u.metrics != nil {
		data.APIRequestStats = u.loadAPIRequestStats()
	}

	return data
}

func (u *webUI) loadAPIRequestStats() uiAPIRequestStats {
	if u.metrics == nil {
		return uiAPIRequestStats{}
	}
	snapshot := u.metrics.Snapshot()
	stats := uiAPIRequestStats{
		TotalRequests:    snapshot.RequestTotal,
		ErrorRequests:    snapshot.RequestErrorTotal,
		Error4xxRequests: snapshot.Request4xxTotal,
		Error5xxRequests: snapshot.Request5xxTotal,
		BytesInTotal:     snapshot.RequestBytesIn,
		BytesOutTotal:    snapshot.RequestBytesOut,
		TrendWindow:      len(snapshot.RequestsPerMinute),
		P95LatencyMS:     snapshot.LatencyP95Seconds * 1000,
	}
	for _, count := range snapshot.RequestsPerMinute {
		if count > stats.TrendMax {
			stats.TrendMax = count
		}
	}
	for _, count := range snapshot.ErrorsPerMinute {
		if count > stats.ErrorTrendMax {
			stats.ErrorTrendMax = count
		}
	}
	stats.TrendPoints = sparklinePoints(snapshot.RequestsPerMinute, 280, 64)
	stats.ErrorTrendPoints = sparklinePoints(snapshot.ErrorsPerMinute, 280, 64)
	errorRateTrend := errorRateTrend(snapshot.RequestsPerMinute, snapshot.ErrorsPerMinute)
	for _, rate := range errorRateTrend {
		if rate > stats.ErrorRateTrendMax {
			stats.ErrorRateTrendMax = rate
		}
	}
	if len(errorRateTrend) > 0 {
		stats.ErrorRateTrendLast = errorRateTrend[len(errorRateTrend)-1]
	}
	if snapshot.RequestTotal > 0 {
		stats.SuccessRatePct = float64(snapshot.RequestTotal-snapshot.RequestErrorTotal) * 100 / float64(snapshot.RequestTotal)
	}
	if snapshot.LatencyCount > 0 {
		stats.AvgLatencyMS = (snapshot.LatencySumSeconds * 1000) / float64(snapshot.LatencyCount)
	}
	return stats
}

func sparklinePoints(counts []uint64, width, height float64) string {
	if len(counts) == 0 {
		return ""
	}
	maxCount := uint64(0)
	for _, count := range counts {
		if count > maxCount {
			maxCount = count
		}
	}
	if maxCount == 0 {
		maxCount = 1
	}
	if len(counts) == 1 {
		return fmt.Sprintf("0,%.2f", height)
	}

	points := make([]string, 0, len(counts))
	for i, count := range counts {
		x := (float64(i) / float64(len(counts)-1)) * width
		scaled := float64(count) / float64(maxCount)
		y := height - (scaled * height)
		y = math.Max(0, math.Min(height, y))
		points = append(points, fmt.Sprintf("%.2f,%.2f", x, y))
	}
	return strings.Join(points, " ")
}

func errorRateTrend(requests []uint64, errors []uint64) []float64 {
	if len(requests) == 0 {
		return nil
	}
	out := make([]float64, len(requests))
	for i := range requests {
		if i >= len(errors) || requests[i] == 0 {
			out[i] = 0
			continue
		}
		out[i] = float64(errors[i]) * 100 / float64(requests[i])
	}
	return out
}

func (u *webUI) loadObject(r *http.Request, bucket, key string) (*metadata.ObjectVersion, error) {
	if u.store == nil {
		return nil, fmt.Errorf("metadata store unavailable")
	}
	obj, err := u.store.GetLatestObjectVersion(r.Context(), bucket, key)
	if err != nil {
		return nil, err
	}
	return &obj, nil
}

func (u *webUI) listObjectsData(r *http.Request, bucket string) (webPageData, error) {
	data := webPageData{
		BucketName:        bucket,
		Prefix:            normalizeUIPrefix(r.URL.Query().Get("prefix")),
		Delimiter:         normalizeUIDelimiter(r.URL.Query().Get("delimiter")),
		ContinuationToken: r.URL.Query().Get("continuation-token"),
		MaxKeys:           100,
	}
	if u.store == nil {
		return data, fmt.Errorf("metadata store unavailable")
	}
	if raw := r.URL.Query().Get("max-keys"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 1000 {
			data.MaxKeys = n
		}
	}

	objects, err := u.store.ListObjects(r.Context(), bucket)
	if err != nil {
		return data, err
	}

	startAfter, err := decodeListContinuationToken(data.ContinuationToken, data.Prefix, data.Delimiter)
	if err != nil {
		return data, fmt.Errorf("invalid continuation token")
	}

	entries := listV2EntriesFromObjects(objects, data.Prefix, data.Delimiter)
	startIdx := 0
	if startAfter != "" {
		startIdx = len(entries)
		for i, entry := range entries {
			if entry.Value > startAfter {
				startIdx = i
				break
			}
		}
	}

	for i := startIdx; i < len(entries) && len(data.Objects)+len(data.CommonPrefixes) < data.MaxKeys; i++ {
		entry := entries[i]
		if entry.Object != nil {
			data.Objects = append(data.Objects, *entry.Object)
		} else {
			data.CommonPrefixes = append(data.CommonPrefixes, entry.Value)
		}
		if len(data.Objects)+len(data.CommonPrefixes) == data.MaxKeys && i+1 < len(entries) {
			token, err := encodeListContinuationToken(data.Prefix, data.Delimiter, entry.Value)
			if err != nil {
				return data, err
			}
			data.IsTruncated = true
			data.NextContinuationToken = token
		}
	}

	data.Breadcrumbs = buildUIBreadcrumbs(data.Prefix)
	return data, nil
}

func (u *webUI) renderPartial(w http.ResponseWriter, r *http.Request, name string, data webPageData) {
	if data.Theme == "" {
		data.Theme = u.resolveTheme(r)
	}
	if len(data.ThemeOptions) == 0 {
		data.ThemeOptions = orderedThemes
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := u.templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "partial render failed", http.StatusInternalServerError)
	}
}

func (u *webUI) renderPartialToString(r *http.Request, name string, data webPageData) (string, error) {
	if data.Theme == "" {
		data.Theme = u.resolveTheme(r)
	}
	if len(data.ThemeOptions) == 0 {
		data.ThemeOptions = orderedThemes
	}
	var b strings.Builder
	if err := u.templates.ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

func writeSSEEvent(w http.ResponseWriter, event, payload string) {
	if event != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", event)
	}
	for _, line := range strings.Split(payload, "\n") {
		_, _ = fmt.Fprintf(w, "data: %s\n", line)
	}
	_, _ = w.Write([]byte("\n"))
}

func (u *webUI) streamSSE(w http.ResponseWriter, r *http.Request, event string, interval time.Duration, render func() (string, error)) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	send := func() bool {
		html, err := render()
		if err != nil {
			return false
		}
		writeSSEEvent(w, event, html)
		flusher.Flush()
		return true
	}
	if !send() {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !send() {
				return
			}
		}
	}
}

func (u *webUI) renderPage(w http.ResponseWriter, r *http.Request, page string, data webPageData) {
	if data.Theme == "" {
		data.Theme = u.resolveTheme(r)
	}
	if len(data.ThemeOptions) == 0 {
		data.ThemeOptions = orderedThemes
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	templateName := filepath.ToSlash("pages/" + page)
	if err := u.templates.ExecuteTemplate(w, templateName, data); err != nil {
		http.Error(w, "page render failed", http.StatusInternalServerError)
	}
}

func (u *webUI) resolveTheme(r *http.Request) string {
	if c, err := r.Cookie(uiThemeCookie); err == nil {
		if _, ok := supportedThemes[normalizeTheme(c.Value)]; ok {
			return normalizeTheme(c.Value)
		}
	}
	if _, ok := supportedThemes[normalizeTheme(u.uiTheme)]; ok {
		return normalizeTheme(u.uiTheme)
	}
	return "sysadmin90"
}

func normalizeTheme(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return "sysadmin90"
	}
	if _, ok := supportedThemes[v]; ok {
		return v
	}
	return "sysadmin90"
}

func positiveOrDefaultDuration(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func normalizeUIPrefix(value string) string {
	return strings.TrimSpace(value)
}

func normalizeUIDelimiter(value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return "/"
	}
	return v
}

func normalizeFolderMarkerKey(prefix, folder string) string {
	p := normalizeUIPrefix(prefix)
	f := strings.TrimSpace(folder)
	f = strings.TrimPrefix(f, "/")
	f = strings.TrimSuffix(f, "/")
	if f == "" {
		return ""
	}
	if p != "" && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p + f + "/"
}

func buildUIBreadcrumbs(prefix string) []uiBreadcrumb {
	if prefix == "" {
		return nil
	}
	trimmed := strings.TrimSuffix(prefix, "/")
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, "/")
	breadcrumbs := make([]uiBreadcrumb, 0, len(parts))
	acc := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		acc += part + "/"
		breadcrumbs = append(breadcrumbs, uiBreadcrumb{Label: part, Prefix: acc})
	}
	return breadcrumbs
}

func objectBrowserURL(bucket, prefix, delimiter, flash string) string {
	q := url.Values{}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if delimiter != "" {
		q.Set("delimiter", delimiter)
	}
	if flash != "" {
		q.Set("flash", flash)
	}
	encoded := q.Encode()
	base := "/app/buckets/" + url.PathEscape(bucket) + "/objects"
	if encoded == "" {
		return base
	}
	return base + "?" + encoded
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UTC().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

func contentDispositionAttachment(filename string) string {
	escaped := url.QueryEscape(filename)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	return "attachment; filename=\"" + filename + "\"; filename*=UTF-8''" + escaped
}
