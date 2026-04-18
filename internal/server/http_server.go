package server

import (
	"crypto/tls"
	"net/http"
	"time"
)

const defaultMaxHeaderBytes = 1 << 20

// HTTPServerOptions configures low-level net/http server tuning.
type HTTPServerOptions struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	DisableKeepAlive  bool
	EnableTLS         bool
	TLSMinVersion     uint16
}

// NewHTTPServer creates an HTTP server with safe baseline timeouts.
func NewHTTPServer(addr string, handler http.Handler) *http.Server {
	return NewHTTPServerWithOptions(addr, handler, HTTPServerOptions{})
}

// NewHTTPServerWithOptions creates an HTTP server with custom tuning.
func NewHTTPServerWithOptions(addr string, handler http.Handler, opts HTTPServerOptions) *http.Server {
	if opts.ReadHeaderTimeout <= 0 {
		opts.ReadHeaderTimeout = 5 * time.Second
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = 60 * time.Second
	}
	if opts.MaxHeaderBytes <= 0 {
		opts.MaxHeaderBytes = defaultMaxHeaderBytes
	}
	if opts.TLSMinVersion == 0 {
		opts.TLSMinVersion = tls.VersionTLS12
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: opts.ReadHeaderTimeout,
		IdleTimeout:       opts.IdleTimeout,
		WriteTimeout:      opts.WriteTimeout,
		ReadTimeout:       opts.ReadTimeout,
		MaxHeaderBytes:    opts.MaxHeaderBytes,
	}
	if opts.DisableKeepAlive {
		srv.SetKeepAlivesEnabled(false)
	}
	if opts.EnableTLS {
		srv.TLSConfig = &tls.Config{MinVersion: opts.TLSMinVersion}
	}
	return srv
}
