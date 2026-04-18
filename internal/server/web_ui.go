package server

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/functions"
	"ds9labs.com/s000/internal/metadata"
)

//go:embed web/templates/*.html web/partials/*.html web/static/*
var webUIFS embed.FS

const (
	uiSessionCookie = "s000_ui_session"
	uiThemeCookie   = "s000_ui_theme"
	uiSessionTTL    = 12 * time.Hour
)

type webUI struct {
	templates *template.Template
	staticFS  fs.FS
	routeMap  []webRoute
	store     metadata.Store
	blob      *blob.Store
	functions *functions.Manager

	accessKey string
	secretKey string
	uiTheme   string

	functionsHTTPPublic               bool
	functionsHTTPCORSAllowOrigin      string
	functionsHTTPCORSAllowMethods     string
	functionsHTTPCORSAllowHeaders     string
	functionsHTTPCORSExposeHeaders    string
	functionsHTTPCORSMaxAge           int
	functionsHTTPCORSAllowCredentials bool

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
	Title                             string
	Page                              string
	RouteMap                          []webRoute
	Buckets                           []metadata.Bucket
	Bucket                            *metadata.Bucket
	BucketName                        string
	BucketWebsite                     metadata.BucketWebsiteConfig
	BucketCORS                        metadata.BucketCORSConfig
	BucketPolicy                      metadata.BucketPolicy
	BucketPublicAccess                metadata.BucketPublicAccessBlock
	ObjectKey                         string
	Object                            *metadata.ObjectVersion
	Objects                           []metadata.ObjectVersion
	Prefix                            string
	Delimiter                         string
	MaxKeys                           int
	ContinuationToken                 string
	NextContinuationToken             string
	IsTruncated                       bool
	CommonPrefixes                    []string
	GeneratedAt                       string
	Error                             string
	Flash                             string
	CSRFToken                         string
	Theme                             string
	ThemeOptions                      []string
	Functions                         []functions.FunctionSummary
	Function                          *functions.Function
	FunctionName                      string
	FunctionVersions                  []functions.FunctionVersionSummary
	FunctionMetrics                   []functions.FunctionMetric
	FunctionAlerts                    []functions.FunctionAlert
	FunctionLogs                      []functions.FunctionLogEntry
	FunctionTemplates                 []functions.Template
	InvokePayload                     string
	InvokeResult                      string
	FunctionsConfig                   functions.Config
	FunctionsHTTPPublic               bool
	FunctionsHTTPCORSAllowOrigin      string
	FunctionsHTTPCORSAllowMethods     string
	FunctionsHTTPCORSAllowHeaders     string
	FunctionsHTTPCORSExposeHeaders    string
	FunctionsHTTPCORSMaxAge           int
	FunctionsHTTPCORSAllowCredentials bool
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
		"q": url.QueryEscape,
		"p": url.PathEscape,
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
		templates:                         tpls,
		staticFS:                          assets,
		store:                             opts.Metadata,
		blob:                              opts.Blob,
		functions:                         opts.Functions,
		accessKey:                         opts.UIAccessKey,
		secretKey:                         opts.UISecretKey,
		uiTheme:                           normalizeTheme(opts.UITheme),
		functionsHTTPPublic:               opts.FunctionsHTTPPublic,
		functionsHTTPCORSAllowOrigin:      opts.FunctionsHTTPCORSAllowOrigin,
		functionsHTTPCORSAllowMethods:     opts.FunctionsHTTPCORSAllowMethods,
		functionsHTTPCORSAllowHeaders:     opts.FunctionsHTTPCORSAllowHeaders,
		functionsHTTPCORSExposeHeaders:    opts.FunctionsHTTPCORSExposeHeaders,
		functionsHTTPCORSMaxAge:           opts.FunctionsHTTPCORSMaxAge,
		functionsHTTPCORSAllowCredentials: opts.FunctionsHTTPCORSAllowCredentials,
		sessions:                          map[string]uiSession{},
		routeMap: []webRoute{
			{Path: "/app/login", Purpose: "operator sign-in shell"},
			{Path: "/app", Purpose: "dashboard and quick actions"},
			{Path: "/app/buckets", Purpose: "bucket list and creation"},
			{Path: "/app/buckets/:bucket", Purpose: "bucket details and navigation"},
			{Path: "/app/buckets/:bucket/objects", Purpose: "object browser"},
			{Path: "/app/buckets/:bucket/objects/:key", Purpose: "object details and metadata"},
			{Path: "/app/uploads", Purpose: "multipart upload monitoring"},
			{Path: "/app/settings", Purpose: "client settings and endpoint info"},
			{Path: "/app/audit", Purpose: "recent security and destructive events"},
			{Path: "/app/functions", Purpose: "functions lifecycle and operations"},
			{Path: "/app/functions/:name", Purpose: "function detail, versions, and invoke"},
			{Path: "/app/partials/functions", Purpose: "htmx function table fragment"},
			{Path: "/app/partials/function-versions", Purpose: "htmx function versions fragment"},
			{Path: "/app/partials/function-metrics", Purpose: "htmx function metrics fragment"},
			{Path: "/app/partials/function-alerts", Purpose: "htmx function alerts fragment"},
			{Path: "/app/partials/function-logs", Purpose: "htmx function logs fragment"},
			{Path: "/app/actions/functions/create", Purpose: "create function"},
			{Path: "/app/actions/functions/update", Purpose: "update function"},
			{Path: "/app/actions/functions/delete", Purpose: "delete function"},
			{Path: "/app/actions/functions/activate", Purpose: "activate function version"},
			{Path: "/app/actions/functions/invoke", Purpose: "invoke function manually"},
			{Path: "/app/partials/buckets", Purpose: "htmx bucket table fragment"},
			{Path: "/app/partials/objects", Purpose: "htmx object table fragment"},
			{Path: "/app/partials/object-metadata", Purpose: "htmx object metadata fragment"},
			{Path: "/app/partials/flash", Purpose: "htmx flash/toast fragment"},
			{Path: "/app/partials/pagination", Purpose: "htmx pagination controls fragment"},
			{Path: "/app/actions/update-bucket-versioning", Purpose: "update bucket versioning setting"},
			{Path: "/app/actions/update-bucket-public-access", Purpose: "update public access block toggles"},
			{Path: "/app/actions/update-bucket-website", Purpose: "update bucket website settings"},
			{Path: "/app/actions/update-bucket-cors", Purpose: "update bucket CORS policy"},
			{Path: "/app/actions/update-bucket-policy", Purpose: "update bucket policy document"},
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
		bucket := r.FormValue("bucket")
		key := r.FormValue("key")
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
			http.Redirect(w, r, "/app/buckets/"+bucket+"/objects?flash=file+required", http.StatusSeeOther)
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
			http.Redirect(w, r, "/app/buckets/"+bucket+"/objects?flash=upload+failed", http.StatusSeeOther)
			return
		}
		contentType := strings.TrimSpace(fh.Header.Get("Content-Type"))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		err = ui.store.PutObjectVersion(r.Context(), metadata.ObjectVersion{Bucket: bucket, Key: key, VersionID: versionID, Size: meta.Size, ETag: meta.MD5Hex, ChecksumSHA256: meta.SHA256, StoragePath: meta.Path, Metadata: map[string]string{"content-type": contentType}, CreatedAt: meta.CreatedAt})
		if err != nil {
			_ = ui.blob.DeleteObject(r.Context(), ref, true)
			http.Redirect(w, r, "/app/buckets/"+bucket+"/objects?flash=metadata+write+failed", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/app/buckets/"+bucket+"/objects?flash=upload+complete", http.StatusSeeOther)
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
		bucket := r.FormValue("bucket")
		key := r.FormValue("key")
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
			http.Redirect(w, r, "/app/buckets/"+bucket+"/objects?flash=delete+marker+created", http.StatusSeeOther)
			return
		}
		removed, err := ui.store.DeleteAllObjectVersions(r.Context(), bucket, key)
		if err != nil && err != metadata.ErrNotFound {
			http.Redirect(w, r, "/app/buckets/"+bucket+"/objects?flash=delete+failed", http.StatusSeeOther)
			return
		}
		if ui.blob != nil {
			for _, obj := range removed {
				_ = ui.blob.DeleteObject(r.Context(), blob.ObjectRef{Bucket: obj.Bucket, Key: obj.Key, VersionID: obj.VersionID}, true)
			}
		}
		http.Redirect(w, r, "/app/buckets/"+bucket+"/objects?flash=object+deleted", http.StatusSeeOther)
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

	mux.HandleFunc("/app/actions/functions/create", func(w http.ResponseWriter, r *http.Request) {
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
		if ui.functions == nil || !ui.functions.Enabled() {
			ui.respondActionResult(w, r, false, "functions runtime disabled", "/app/functions", "")
			return
		}
		if err := parseFunctionActionForm(r); err != nil {
			ui.respondActionResult(w, r, false, "parse failed", "/app/functions", "")
			return
		}
		priority, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("priority")))
		module, err := parseFunctionModule(r, true)
		if err != nil {
			ui.respondActionResult(w, r, false, err.Error(), "/app/functions", "")
			return
		}
		err = ui.functions.CreateFunction(functions.Function{
			Name:     strings.TrimSpace(r.FormValue("name")),
			Runtime:  strings.TrimSpace(r.FormValue("runtime")),
			Trigger:  strings.TrimSpace(r.FormValue("trigger")),
			Priority: priority,
			Enabled:  r.FormValue("enabled") == "on",
			Module:   module,
		})
		if err != nil {
			ui.respondActionResult(w, r, false, "create failed", "/app/functions", "")
			return
		}
		ui.respondActionResult(w, r, true, "function created", "/app/functions", "functions-changed")
	})

	mux.HandleFunc("/app/actions/functions/update", func(w http.ResponseWriter, r *http.Request) {
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
		if ui.functions == nil || !ui.functions.Enabled() {
			ui.respondActionResult(w, r, false, "functions runtime disabled", "/app/functions", "")
			return
		}
		if err := parseFunctionActionForm(r); err != nil {
			ui.respondActionResult(w, r, false, "parse failed", "/app/functions", "")
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		priority, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("priority")))
		module, err := parseFunctionModule(r, false)
		if err != nil {
			ui.respondActionResult(w, r, false, err.Error(), "/app/functions/"+url.PathEscape(name), "")
			return
		}
		err = ui.functions.UpdateFunction(name, functions.Function{
			Runtime:  strings.TrimSpace(r.FormValue("runtime")),
			Trigger:  strings.TrimSpace(r.FormValue("trigger")),
			Priority: priority,
			Enabled:  r.FormValue("enabled") == "on",
			Module:   module,
		})
		if err != nil {
			ui.respondActionResult(w, r, false, "update failed", "/app/functions/"+url.PathEscape(name), "")
			return
		}
		ui.respondActionResult(w, r, true, "function updated", "/app/functions/"+url.PathEscape(name), "functions-changed")
	})

	mux.HandleFunc("/app/actions/functions/delete", func(w http.ResponseWriter, r *http.Request) {
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
		if ui.functions == nil || !ui.functions.Enabled() {
			ui.respondActionResult(w, r, false, "functions runtime disabled", "/app/functions", "")
			return
		}
		if err := r.ParseForm(); err != nil {
			ui.respondActionResult(w, r, false, "parse failed", "/app/functions", "")
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		if err := ui.functions.DeleteFunction(name); err != nil {
			ui.respondActionResult(w, r, false, "delete failed", "/app/functions/"+url.PathEscape(name), "")
			return
		}
		ui.respondActionResult(w, r, true, "function deleted", "/app/functions", "functions-changed")
	})

	mux.HandleFunc("/app/actions/functions/activate", func(w http.ResponseWriter, r *http.Request) {
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
		if ui.functions == nil || !ui.functions.Enabled() {
			ui.respondActionResult(w, r, false, "functions runtime disabled", "/app/functions", "")
			return
		}
		if err := r.ParseForm(); err != nil {
			ui.respondActionResult(w, r, false, "parse failed", "/app/functions", "")
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		version, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("version")))
		if err := ui.functions.ActivateFunctionVersion(name, version); err != nil {
			ui.respondActionResult(w, r, false, "activate failed", "/app/functions/"+url.PathEscape(name), "")
			return
		}
		ui.respondActionResult(w, r, true, "version activated", "/app/functions/"+url.PathEscape(name), "functions-changed")
	})

	mux.HandleFunc("/app/actions/functions/invoke", func(w http.ResponseWriter, r *http.Request) {
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
		if ui.functions == nil || !ui.functions.Enabled() {
			ui.respondActionResult(w, r, false, "functions runtime disabled", "/app/functions", "")
			return
		}
		if err := r.ParseForm(); err != nil {
			ui.respondActionResult(w, r, false, "parse failed", "/app/functions", "")
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		payload := strings.TrimSpace(r.FormValue("payload"))
		if payload == "" {
			payload = "{}"
		}
		if !json.Valid([]byte(payload)) {
			ui.respondActionResult(w, r, false, "invalid JSON payload", "/app/functions/"+url.PathEscape(name), "")
			return
		}
		result, err := ui.functions.InvokeFunction(r.Context(), name, json.RawMessage(payload))
		if err != nil {
			ui.respondActionResult(w, r, false, "invoke failed", "/app/functions/"+url.PathEscape(name), "")
			return
		}
		flash := "invoke complete"
		if !result.Continue {
			flash = "invoke denied"
		}
		ui.respondActionResult(w, r, true, flash, "/app/functions/"+url.PathEscape(name), "functions-changed")
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
		ui.renderPage(w, r, "dashboard", webPageData{Title: "Dashboard", Page: "dashboard", RouteMap: ui.routeMap, CSRFToken: s.CSRFToken, Flash: r.URL.Query().Get("flash")})
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
		data := webPageData{Title: "Buckets", Page: "buckets", RouteMap: ui.routeMap, Buckets: buckets, CSRFToken: s.CSRFToken, Flash: r.URL.Query().Get("flash")}
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
	mux.HandleFunc("/app/functions", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		data := webPageData{Title: "Functions", Page: "functions", RouteMap: ui.routeMap, CSRFToken: s.CSRFToken, Flash: r.URL.Query().Get("flash")}
		if ui.functions == nil || !ui.functions.Enabled() {
			data.Error = "functions runtime disabled"
			ui.renderPage(w, r, "functions", data)
			return
		}
		data.Functions = ui.functions.ListFunctions()
		data.FunctionTemplates = functions.BuiltinTemplates()
		data.FunctionMetrics = ui.functions.Metrics()
		data.FunctionAlerts = ui.functions.Alerts()
		data.FunctionsConfig = ui.functions.ConfigSnapshot()
		data.FunctionsHTTPPublic = ui.functionsHTTPPublic
		data.FunctionsHTTPCORSAllowOrigin = ui.functionsHTTPCORSAllowOrigin
		data.FunctionsHTTPCORSAllowMethods = ui.functionsHTTPCORSAllowMethods
		data.FunctionsHTTPCORSAllowHeaders = ui.functionsHTTPCORSAllowHeaders
		data.FunctionsHTTPCORSExposeHeaders = ui.functionsHTTPCORSExposeHeaders
		data.FunctionsHTTPCORSMaxAge = ui.functionsHTTPCORSMaxAge
		data.FunctionsHTTPCORSAllowCredentials = ui.functionsHTTPCORSAllowCredentials
		ui.renderPage(w, r, "functions", data)
	})
	mux.HandleFunc("/app/functions/", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/app/functions/")
		name = strings.TrimSpace(strings.Trim(name, "/"))
		if name == "" {
			http.NotFound(w, r)
			return
		}
		data := webPageData{Title: "Function", Page: "function-detail", RouteMap: ui.routeMap, CSRFToken: s.CSRFToken, Flash: r.URL.Query().Get("flash")}
		if ui.functions == nil || !ui.functions.Enabled() {
			data.Error = "functions runtime disabled"
			ui.renderPage(w, r, "function_detail", data)
			return
		}
		def, err := ui.functions.GetFunction(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		versions, _ := ui.functions.ListFunctionVersions(name)
		data.Function = &def
		data.FunctionName = name
		data.FunctionVersions = versions
		data.FunctionMetrics = ui.functions.Metrics()
		data.FunctionAlerts = ui.functions.Alerts()
		data.FunctionLogs = ui.functions.RecentLogs(50)
		data.FunctionsConfig = ui.functions.ConfigSnapshot()
		ui.renderPage(w, r, "function_detail", data)
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
		_, ok := ui.requireSession(w, r)
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
	mux.HandleFunc("/app/partials/functions", func(w http.ResponseWriter, r *http.Request) {
		_, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		data := webPageData{GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
		if ui.functions == nil || !ui.functions.Enabled() {
			data.Error = "functions runtime disabled"
			ui.renderPartial(w, r, "partials/functions_table", data)
			return
		}
		data.Functions = ui.functions.ListFunctions()
		ui.renderPartial(w, r, "partials/functions_table", data)
	})
	mux.HandleFunc("/app/partials/function-versions", func(w http.ResponseWriter, r *http.Request) {
		s, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		data := webPageData{FunctionName: name, CSRFToken: s.CSRFToken}
		if ui.functions == nil || !ui.functions.Enabled() {
			data.Error = "functions runtime disabled"
			ui.renderPartial(w, r, "partials/function_versions", data)
			return
		}
		versions, err := ui.functions.ListFunctionVersions(name)
		if err != nil {
			data.Error = err.Error()
		}
		data.FunctionVersions = versions
		ui.renderPartial(w, r, "partials/function_versions", data)
	})
	mux.HandleFunc("/app/partials/function-metrics", func(w http.ResponseWriter, r *http.Request) {
		_, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		data := webPageData{}
		if ui.functions == nil || !ui.functions.Enabled() {
			data.Error = "functions runtime disabled"
			ui.renderPartial(w, r, "partials/function_metrics", data)
			return
		}
		data.FunctionMetrics = ui.functions.Metrics()
		ui.renderPartial(w, r, "partials/function_metrics", data)
	})
	mux.HandleFunc("/app/partials/function-alerts", func(w http.ResponseWriter, r *http.Request) {
		_, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		data := webPageData{}
		if ui.functions == nil || !ui.functions.Enabled() {
			data.Error = "functions runtime disabled"
			ui.renderPartial(w, r, "partials/function_alerts", data)
			return
		}
		data.FunctionAlerts = ui.functions.Alerts()
		ui.renderPartial(w, r, "partials/function_alerts", data)
	})
	mux.HandleFunc("/app/partials/function-logs", func(w http.ResponseWriter, r *http.Request) {
		_, ok := ui.requireSession(w, r)
		if !ok || r.Method != http.MethodGet {
			if ok {
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		limit := 50
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				limit = n
			}
		}
		data := webPageData{}
		if ui.functions == nil || !ui.functions.Enabled() {
			data.Error = "functions runtime disabled"
			ui.renderPartial(w, r, "partials/function_logs", data)
			return
		}
		logs := ui.functions.RecentLogs(limit)
		nameFilter := strings.TrimSpace(r.URL.Query().Get("name"))
		triggerFilter := strings.TrimSpace(r.URL.Query().Get("trigger"))
		outcomeFilter := strings.TrimSpace(r.URL.Query().Get("outcome"))
		if nameFilter != "" || triggerFilter != "" || outcomeFilter != "" {
			filtered := make([]functions.FunctionLogEntry, 0, len(logs))
			for _, entry := range logs {
				if nameFilter != "" && entry.Function != nameFilter {
					continue
				}
				if triggerFilter != "" && entry.Trigger != triggerFilter {
					continue
				}
				if outcomeFilter != "" && entry.Outcome != outcomeFilter {
					continue
				}
				filtered = append(filtered, entry)
			}
			logs = filtered
		}
		data.FunctionLogs = logs
		ui.renderPartial(w, r, "partials/function_logs", data)
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
	data := webPageData{BucketName: bucket, Prefix: r.URL.Query().Get("prefix"), Delimiter: r.URL.Query().Get("delimiter"), ContinuationToken: r.URL.Query().Get("continuation-token"), MaxKeys: 100}
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
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })

	start := 0
	if data.ContinuationToken != "" {
		for i, o := range objects {
			if o.Key == data.ContinuationToken {
				start = i + 1
				break
			}
		}
	}

	prefixSet := map[string]struct{}{}
	count := 0
	for i := start; i < len(objects); i++ {
		o := objects[i]
		if data.Prefix != "" && !strings.HasPrefix(o.Key, data.Prefix) {
			continue
		}
		if data.Delimiter != "" {
			tail := strings.TrimPrefix(o.Key, data.Prefix)
			if idx := strings.Index(tail, data.Delimiter); idx >= 0 {
				prefix := data.Prefix + tail[:idx+len(data.Delimiter)]
				prefixSet[prefix] = struct{}{}
				continue
			}
		}
		data.Objects = append(data.Objects, o)
		count++
		if count >= data.MaxKeys {
			if i+1 < len(objects) {
				data.IsTruncated = true
				data.NextContinuationToken = o.Key
			}
			break
		}
	}
	for cp := range prefixSet {
		data.CommonPrefixes = append(data.CommonPrefixes, cp)
	}
	sort.Strings(data.CommonPrefixes)
	return data, nil
}

func parseFunctionActionForm(r *http.Request) error {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return r.ParseMultipartForm(32 << 20)
	}
	return r.ParseForm()
}

func parseFunctionModule(r *http.Request, required bool) ([]byte, error) {
	if file, header, err := r.FormFile("module_file"); err == nil {
		defer func() { _ = file.Close() }()
		if header == nil || strings.TrimSpace(header.Filename) == "" {
			return nil, fmt.Errorf("invalid module file")
		}
		if ext := strings.ToLower(filepath.Ext(strings.TrimSpace(header.Filename))); ext != ".wasm" {
			return nil, fmt.Errorf("module file must be .wasm")
		}
		module, err := io.ReadAll(io.LimitReader(file, 32<<20+1))
		if err != nil {
			return nil, fmt.Errorf("module file read failed")
		}
		if len(module) > 32<<20 {
			return nil, fmt.Errorf("module file exceeds 32MB limit")
		}
		if len(module) == 0 {
			return nil, fmt.Errorf("module file is empty")
		}
		return module, nil
	}

	if raw := strings.TrimSpace(r.FormValue("module_base64")); raw != "" {
		module, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid module base64")
		}
		if len(module) == 0 {
			return nil, fmt.Errorf("module payload is required")
		}
		return module, nil
	}

	if required {
		return nil, fmt.Errorf("module file or module base64 is required")
	}
	return nil, nil
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
