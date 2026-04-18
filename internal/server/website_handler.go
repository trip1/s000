package server

import (
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"

	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/metadata"
)

// WebsiteHandler serves anonymous bucket website content.
type WebsiteHandler struct {
	store  metadata.Store
	blob   *blob.Store
	domain string
}

// NewWebsiteHandler builds website endpoint handler.
func NewWebsiteHandler(store metadata.Store, bstore *blob.Store, domain string) http.Handler {
	return &WebsiteHandler{store: store, blob: bstore, domain: strings.TrimSpace(domain)}
}

func (h *WebsiteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.store == nil || h.blob == nil {
		http.Error(w, "website service unavailable", http.StatusServiceUnavailable)
		return
	}

	bucket, key, ok := h.resolveBucketAndKey(r)
	if !ok {
		http.NotFound(w, r)
		return
	}

	cfg, err := h.store.GetBucketWebsite(r.Context(), bucket)
	if err != nil || !cfg.Enabled || !cfg.PublicRead {
		http.NotFound(w, r)
		return
	}

	if cfg.RedirectAllHost != "" {
		target := h.redirectURL(r, cfg)
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	resolved := h.resolveWebsiteKey(key, cfg.IndexDocument)
	obj, keyErr := h.lookupObject(r, bucket, resolved)
	if keyErr != nil && resolved != key {
		obj, keyErr = h.lookupObject(r, bucket, key)
	}
	if keyErr != nil {
		if cfg.ErrorDocument != "" {
			errObj, errLookup := h.lookupObject(r, bucket, cfg.ErrorDocument)
			if errLookup == nil {
				h.writeObject(w, r, cfg.ErrorDocument, errObj, http.StatusNotFound)
				return
			}
		}
		http.NotFound(w, r)
		return
	}

	h.writeObject(w, r, resolved, obj, http.StatusOK)
}

func (h *WebsiteHandler) resolveBucketAndKey(r *http.Request) (bucket, key string, ok bool) {
	host := r.Host
	if parsedHost, _, err := net.SplitHostPort(r.Host); err == nil {
		host = parsedHost
	}
	host = strings.TrimSpace(strings.ToLower(host))

	if h.domain != "" {
		d := strings.ToLower(h.domain)
		suffix := "." + d
		if strings.HasSuffix(host, suffix) {
			bucket = strings.TrimSuffix(host, suffix)
			if bucket != "" {
				key = strings.TrimPrefix(r.URL.EscapedPath(), "/")
				key = decodePath(key)
				return bucket, key, true
			}
		}
	}

	p := strings.TrimPrefix(r.URL.EscapedPath(), "/")
	if p == "" {
		return "", "", false
	}
	parts := strings.SplitN(p, "/", 2)
	bucket = decodePath(parts[0])
	if bucket == "" {
		return "", "", false
	}
	if len(parts) > 1 {
		key = decodePath(parts[1])
	}
	return bucket, key, true
}

func (h *WebsiteHandler) resolveWebsiteKey(key, index string) string {
	index = strings.TrimSpace(index)
	if index == "" {
		index = "index.html"
	}
	if key == "" {
		return index
	}
	if strings.HasSuffix(key, "/") {
		return key + index
	}
	return key
}

func (h *WebsiteHandler) lookupObject(r *http.Request, bucket, key string) (metadata.ObjectVersion, error) {
	if key == "" {
		return metadata.ObjectVersion{}, metadata.ErrNotFound
	}
	obj, err := h.store.GetLatestObjectVersion(r.Context(), bucket, key)
	if err == nil {
		return obj, nil
	}
	if strings.HasSuffix(key, "/") {
		return metadata.ObjectVersion{}, err
	}
	// directory-style fallback
	return h.store.GetLatestObjectVersion(r.Context(), bucket, key+"/index.html")
}

func (h *WebsiteHandler) writeObject(w http.ResponseWriter, r *http.Request, key string, obj metadata.ObjectVersion, status int) {
	ctype := "application/octet-stream"
	if obj.Metadata != nil {
		if v := strings.TrimSpace(obj.Metadata["content-type"]); v != "" {
			ctype = v
		}
	}
	if ctype == "application/octet-stream" {
		if ext := strings.ToLower(path.Ext(key)); ext != "" {
			if inferred := mime.TypeByExtension(ext); inferred != "" {
				ctype = inferred
			}
		}
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", obj.Size))
	w.Header().Set("ETag", quotedETag(obj.ETag))
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	meta := blob.ObjectMeta{Ref: blob.ObjectRef{Bucket: obj.Bucket, Key: obj.Key, VersionID: obj.VersionID}, Path: obj.StoragePath, Size: obj.Size, SHA256: obj.ChecksumSHA256, MD5Hex: obj.ETag, CreatedAt: obj.CreatedAt}
	_, _ = h.blob.ReadObject(r.Context(), meta, nil, w)
}

func (h *WebsiteHandler) redirectURL(r *http.Request, cfg metadata.BucketWebsiteConfig) string {
	protocol := strings.TrimSpace(cfg.RedirectAllProtocol)
	if protocol == "" {
		if r.TLS != nil {
			protocol = "https"
		} else {
			protocol = "http"
		}
	}
	pathPart := r.URL.EscapedPath()
	if pathPart == "" {
		pathPart = "/"
	}
	if raw := r.URL.RawQuery; raw != "" {
		return protocol + "://" + cfg.RedirectAllHost + pathPart + "?" + raw
	}
	return protocol + "://" + cfg.RedirectAllHost + pathPart
}

func decodePath(s string) string {
	decoded, err := url.PathUnescape(s)
	if err != nil {
		return s
	}
	return decoded
}
