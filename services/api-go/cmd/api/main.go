package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"tile-server/api-go/internal/basemap"
	"tile-server/api-go/internal/basemapapi"
	"tile-server/api-go/internal/config"
)

const shutdownGrace = 15 * time.Second

type cliArgs struct {
	command string
	envFile string
}

func main() {
	args, err := parseArgs(os.Args[1:])
	if err != nil {
		log.Fatalf("parse arguments: %v", err)
	}
	if args.envFile != "" {
		if err := os.Setenv("APP_ENV_FILE", args.envFile); err != nil {
			log.Fatalf("set APP_ENV_FILE: %v", err)
		}
	}

	// Service management verbs (install / uninstall / start / stop / status).
	if handled, err := handleServiceArgs(args.command, args.envFile); handled {
		if err != nil {
			log.Fatalf("%v", err)
		}
		return
	}

	// When the SCM launched us there is no console and no SIGINT, so the service
	// handler owns the lifecycle instead of the signal handler.
	if runAsWindowsService() {
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatalf("%v", err)
	}
}

func parseArgs(argv []string) (cliArgs, error) {
	var args cliArgs
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--env-file":
			if i+1 >= len(argv) {
				return args, errors.New("--env-file requires a path")
			}
			args.envFile = argv[i+1]
			i++
		default:
			if args.command == "" {
				args.command = argv[i]
			}
		}
	}
	return args, nil
}

// run boots the service and blocks until ctx is cancelled or the listener dies.
// It owns every resource it creates.
func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// The admin surface can create tokens that read every basemap, so it must
	// never be reachable off-host without credentials.
	if cfg.BindsToNonLoopback() && !cfg.HasAdminCredentials() {
		return fmt.Errorf("refusing to listen on %s without admin credentials: set ADMIN_TOKEN or ADMIN_USER/ADMIN_PASSWORD", cfg.ListenAddr())
	}

	store, err := basemap.OpenStore(basemap.StoreConfig{
		SQLitePath:          cfg.SQLitePath,
		ScanRoot:            cfg.BasemapScanRoot,
		VectorScanRoot:      cfg.VectorScanRoot,
		APIBaseURL:          cfg.APIBaseURL,
		TileBaseURL:         cfg.TilePublicBaseURL,
		VectorBaseURL:       cfg.VectorPublicBaseURL,
		AutoScanOnStart:     cfg.AutoScanOnStart,
		TokenDefaultTTL:     cfg.TokenDefaultTTL,
		TileJobCommand:      cfg.TileJobCommand,
		TileJobVRTCommand:   cfg.TileJobVRTCommand,
		TileJobLogDir:       cfg.TileJobLogDir,
		HealthCheckInterval: cfg.HealthCheckInterval,
	})
	if err != nil {
		return fmt.Errorf("open sqlite store: %w", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("close store: %v", err)
		}
	}()

	systemInfo := basemap.SystemInfo{
		ServiceName:    cfg.ServiceName,
		ServiceVersion: cfg.ServiceVersion,
		Commit:         cfg.Commit,
		Env:            cfg.Env,
		APIBaseURL:     cfg.APIBaseURL,
		TileBaseURL:    cfg.TilePublicBaseURL,
		VectorBaseURL:  cfg.VectorPublicBaseURL,
		SQLitePath:     cfg.SQLitePath,
		ScanRoot:       cfg.BasemapScanRoot,
		VectorScanRoot: cfg.VectorScanRoot,
		TileJobCommand: cfg.TileJobCommand,
		StartedAt:      time.Now().UTC(),
	}

	if cfg.AutoScanOnStart && strings.TrimSpace(cfg.BasemapScanRoot) != "" {
		if _, err := store.RunScan(context.Background(), "local-basemaps"); err != nil {
			log.Printf("startup scan skipped: %v", err)
		} else {
			log.Printf("startup scan completed for %s", cfg.BasemapScanRoot)
		}
	}
	if cfg.AutoScanOnStart && strings.TrimSpace(cfg.VectorScanRoot) != "" {
		if _, err := store.RunVectorScan(context.Background()); err != nil {
			log.Printf("startup vector scan skipped: %v", err)
		} else {
			log.Printf("startup vector scan completed for %s", cfg.VectorScanRoot)
		}
	}

	// Health inspection, push retries and the tile queue run in the background.
	store.StartBackgroundWorkers()

	server, err := basemapapi.New(store, systemInfo, basemapapi.Options{
		Admin: basemapapi.AdminAuth{
			Token:    cfg.AdminToken,
			User:     cfg.AdminUser,
			Password: cfg.AdminPassword,
		},
		CORSAllowedOrigins: cfg.CORSAllowedOrigins,
	})
	if err != nil {
		return fmt.Errorf("init api server: %w", err)
	}

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr(),
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	cleanupPID, err := writePIDFile(strings.TrimSpace(os.Getenv("RS_API_PID_FILE")))
	if err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	if cleanupPID != nil {
		defer cleanupPID()
	}

	log.Printf("api base url: %s", cfg.APIBaseURL)
	log.Printf("tile public base url: %s", cfg.TilePublicBaseURL)
	log.Printf("vector public base url: %s", cfg.VectorPublicBaseURL)
	log.Printf("sqlite path: %s", cfg.SQLitePath)
	log.Printf("scan root: %s", cfg.BasemapScanRoot)
	log.Printf("vector scan root: %s", cfg.VectorScanRoot)
	if cfg.HasAdminCredentials() {
		log.Printf("admin auth: enabled")
	} else {
		log.Printf("admin auth: disabled (loopback only)")
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("basemap pusher listening on http://%s", cfg.ListenAddr())
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("listen and serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		log.Printf("shutdown signal received, draining connections")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown incomplete: %v", err)
			_ = httpServer.Close()
		}
		if err := <-serveErr; err != nil {
			log.Printf("server stopped with error: %v", err)
		}
		log.Printf("shutdown complete")
		return nil
	}
}

func writePIDFile(path string) (func(), error) {
	if path == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	pidText := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(path, []byte(pidText+"\n"), 0o644); err != nil {
		return nil, err
	}

	cleanup := func() {
		content, err := os.ReadFile(path)
		if err != nil {
			return
		}
		if strings.TrimSpace(string(content)) == pidText {
			_ = os.Remove(path)
		}
	}
	return cleanup, nil
}
