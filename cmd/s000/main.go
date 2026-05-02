package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ds9labs.com/s000/internal/auth"
	"ds9labs.com/s000/internal/blob"
	"ds9labs.com/s000/internal/bootstrap"
	"ds9labs.com/s000/internal/config"
	"ds9labs.com/s000/internal/lifecycle"
	"ds9labs.com/s000/internal/metadata"
	"ds9labs.com/s000/internal/observability"
	"ds9labs.com/s000/internal/server"
)

func main() {
	cfg := config.Load()
	importDirectory := flag.String("import-directory", cfg.ImportDirectory, "import existing directory tree into buckets at startup")
	flag.Parse()
	cfg.ImportDirectory = strings.TrimSpace(*importDirectory)
	if err := config.ValidateTLSEnabledSettings(cfg); err != nil {
		log.Fatal(err)
	}
	tlsMinVersion, err := config.ParseTLSMinVersion(cfg.TLSMinVersion)
	if err != nil {
		log.Fatal(err)
	}
	credentialStore := auth.NewCredentialStore(timeNow)
	if cfg.AdminAccessKey != "" && cfg.AdminSecretKey != "" {
		if err := credentialStore.BootstrapAdminCredential(cfg.AdminAccessKey, cfg.AdminSecretKey); err != nil {
			log.Fatal(err)
		}
	}
	verifier := auth.NewVerifier(credentialStore, auth.VerifierOptions{MaxSkew: cfg.AuthMaxSkew})

	metadataBackend, err := metadata.ParseBackend(cfg.MetadataBackend)
	if err != nil {
		log.Fatal(err)
	}

	connectCtx, cancelConnect := context.WithTimeout(context.Background(), cfg.MetadataConnectTimeout)
	defer cancelConnect()
	metadataConnections, err := metadata.OpenConnections(connectCtx, metadata.Config{
		Backend:    metadataBackend,
		DSN:        cfg.MetadataDSN,
		ValkeyAddr: cfg.MetadataValkeyAddr,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := metadataConnections.Close(); err != nil {
			slog.Error("metadata connection close failed", "error", err)
		}
	}()

	metadataStore, err := metadata.NewStore(metadata.Config{
		Backend:    metadataBackend,
		DSN:        cfg.MetadataDSN,
		ValkeyAddr: cfg.MetadataValkeyAddr,
		SQLDB:      metadataConnections.SQLDB,
		Valkey:     metadataConnections.Valkey,
	})
	if err != nil {
		log.Fatal(err)
	}
	bucketCount, objectCount, err := metadataCatalogCounts(context.Background(), metadataStore)
	if err != nil {
		log.Fatal(err)
	}
	if bucketCount > 0 || objectCount > 0 {
		slog.Info("startup metadata reindex complete", "buckets", bucketCount, "objects", objectCount)
	}

	blobStore, err := blob.NewStore(blob.Config{
		RootDir:   cfg.DataDir,
		FsyncMode: blob.FsyncSafe,
	})
	if err != nil {
		log.Fatal(err)
	}
	if cfg.ImportDirectory != "" {
		importResult, err := bootstrap.ImportDirectory(context.Background(), bootstrap.ImportOptions{
			Directory: cfg.ImportDirectory,
			Region:    "us-east-1",
			Metadata:  metadataStore,
			Blob:      blobStore,
		})
		if err != nil {
			log.Fatal(err)
		}
		slog.Info("startup import completed", "directory", cfg.ImportDirectory, "buckets_created", importResult.BucketsCreated, "objects_added", importResult.ObjectsAdded, "objects_skipped", importResult.ObjectsSkipped)
	}

	metrics := observability.NewCollector()

	lifecycleRules, err := lifecycle.ParseRules(cfg.LifecycleRules)
	if err != nil {
		log.Fatal(err)
	}
	var lifecycleWorker *lifecycle.Worker
	lifecycleWorker, err = lifecycle.NewWorker(lifecycle.Options{
		Metadata:     metadataStore,
		Blob:         blobStore,
		Rules:        lifecycleRules,
		Interval:     cfg.LifecycleInterval,
		BatchSize:    cfg.LifecycleBatchSize,
		MaxRetries:   cfg.LifecycleMaxRetries,
		RetryBackoff: cfg.LifecycleBackoff,
		DryRun:       cfg.LifecycleDryRun,
		QueueDepthFn: metrics.SetWorkerQueueDepth,
	})
	if err != nil {
		log.Fatal(err)
	}

	appCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-appCtx.Done():
				return
			case <-ticker.C:
			}
			removed, gcErr := blobStore.RemoveStaleMultipartUploads(context.Background())
			if gcErr != nil {
				slog.Error("multipart gc failed", "error", gcErr)
				continue
			}
			if removed > 0 {
				slog.Info("multipart gc removed stale uploads", "count", removed)
			}
		}
	}()
	if lifecycleWorker != nil {
		go func() {
			if err := lifecycleWorker.Run(appCtx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("lifecycle worker exited", "error", err)
			}
		}()
	}

	patSigningKey := cfg.PATSigningKey
	if patSigningKey == "" {
		patSigningKey = cfg.AdminSecretKey
	}
	var patManager *auth.PersonalAccessTokenManager
	if patSigningKey != "" {
		patManager = auth.NewPersonalAccessTokenManager([]byte(patSigningKey), timeNow)
	}
	var sseMasterKey []byte
	if cfg.SSEMasterKey != "" {
		decoded, err := base64.StdEncoding.DecodeString(cfg.SSEMasterKey)
		if err != nil || len(decoded) != 32 {
			log.Fatal("S000_SSE_MASTER_KEY must be a base64-encoded 32-byte key")
		}
		sseMasterKey = decoded
	}

	handler := server.NewHandler(server.Options{
		Domain:              cfg.Domain,
		MaxInFlight:         cfg.MaxInFlight,
		Verifier:            verifier,
		PATSigningKey:       patSigningKey,
		PATManager:          patManager,
		Metadata:            metadataStore,
		Blob:                blobStore,
		Lifecycle:           lifecycleWorker,
		Metrics:             metrics,
		MetricsPath:         cfg.MetricsPath,
		HeavyOpsWorkers:     cfg.HeavyOpsWorkers,
		HeavyOpsQueue:       cfg.HeavyOpsQueue,
		AuditEnabled:        true,
		AuthFailThreshold:   cfg.AuthFailThreshold,
		AuthFailWindow:      cfg.AuthFailWindow,
		AuthBlockDuration:   cfg.AuthBlockDuration,
		UIAccessKey:         cfg.AdminAccessKey,
		UISecretKey:         cfg.AdminSecretKey,
		UITheme:             cfg.UITheme,
		UIDashboardStatsSSE: cfg.UIDashboardStatsSSE,
		UIBucketsSSE:        cfg.UIBucketsSSE,
		UITokensSSE:         cfg.UITokensSSE,
		UIObjectsSSE:        cfg.UIObjectsSSE,
		UIObjectMetadataSSE: cfg.UIObjectMetadataSSE,
		SSEMasterKey:        sseMasterKey,
		ReadyCheck: func(ctx context.Context) error {
			return metadataConnections.Ping(ctx)
		},
		Tracing:      observability.NoopTraceHooks(),
		TracingOn:    cfg.TracingEnabled,
		BucketRegion: "us-east-1",
	})
	httpServer := server.NewHTTPServerWithOptions(cfg.Addr, handler, server.HTTPServerOptions{
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		ReadTimeout:       cfg.HTTPReadTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
		MaxHeaderBytes:    cfg.HTTPMaxHeaderBytes,
		DisableKeepAlive:  cfg.HTTPDisableKeepAlive,
		EnableTLS:         cfg.TLSEnabled,
		TLSMinVersion:     tlsMinVersion,
	})

	var websiteServer *http.Server
	if cfg.WebsiteEnabled {
		websiteServer = server.NewHTTPServerWithOptions(cfg.WebsiteAddr, server.NewWebsiteHandlerWithSSE(metadataStore, blobStore, cfg.WebsiteDomain, sseMasterKey), server.HTTPServerOptions{
			ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
			ReadTimeout:       cfg.HTTPReadTimeout,
			WriteTimeout:      cfg.HTTPWriteTimeout,
			IdleTimeout:       cfg.HTTPIdleTimeout,
			MaxHeaderBytes:    cfg.HTTPMaxHeaderBytes,
			DisableKeepAlive:  cfg.HTTPDisableKeepAlive,
			EnableTLS:         cfg.TLSEnabled,
			TLSMinVersion:     tlsMinVersion,
		})
	}

	go func() {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		<-ctx.Done()
		cancelWorkers()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("server shutdown failed", "error", err)
		}
		if websiteServer != nil {
			if err := websiteServer.Shutdown(shutdownCtx); err != nil {
				slog.Error("website server shutdown failed", "error", err)
			}
		}
	}()

	if websiteServer != nil {
		go func() {
			var err error
			if cfg.TLSEnabled {
				err = websiteServer.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
			} else {
				err = websiteServer.ListenAndServe()
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatal(err)
			}
		}()
	}

	var serveErr error
	if cfg.TLSEnabled {
		serveErr = httpServer.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
	} else {
		serveErr = httpServer.ListenAndServe()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		log.Fatal(serveErr)
	}
}

func timeNow() time.Time {
	return time.Now().UTC()
}

func metadataCatalogCounts(ctx context.Context, store metadata.Store) (int, int, error) {
	buckets, err := store.ListBuckets(ctx)
	if err != nil {
		return 0, 0, err
	}
	objectCount := 0
	for _, bucket := range buckets {
		objects, err := store.ListObjects(ctx, bucket.Name)
		if err != nil {
			return 0, 0, err
		}
		objectCount += len(objects)
	}
	return len(buckets), objectCount, nil
}
