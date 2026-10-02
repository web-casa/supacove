// Command supabackup is the single-binary entrypoint for the self-hosted
// backup control plane. Subcommands:
//
//	serve                     run the web server (default)
//	bootstrap                 print a one-time admin bootstrap token
//	reset-password <username> reset a local password and revoke its sessions
//	version                   print build information
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"crypto/sha256"
	"github.com/cloudfan/supabackup/backend/internal/auth"
	"github.com/cloudfan/supabackup/backend/internal/config"
	"github.com/cloudfan/supabackup/backend/internal/db"
	"github.com/cloudfan/supabackup/backend/internal/limiter"
	"github.com/cloudfan/supabackup/backend/internal/server"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = runServe()
	case "bootstrap":
		err = runBootstrap()
	case "reset-password":
		if len(os.Args) < 3 {
			err = errors.New("usage: supabackup reset-password <username>")
			break
		}
		err = runResetPassword(os.Args[2])
	case "healthcheck":
		err = runHealthcheck()
	case "version":
		fmt.Printf("supabackup %s (commit %s, built %s)\n", version, commit, buildDate)
	case "help", "-h", "--help":
		fmt.Print("usage: supabackup [serve|bootstrap|reset-password <username>|healthcheck|version]\n")
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	switch os.Getenv("SB_LOG_LEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

func mustLoadConfig(log *slog.Logger) *config.Config {
	cfg, err := config.Load()
	if err != nil {
		fatal(log, err)
	}
	return cfg
}

func openStore(cfg *config.Config, log *slog.Logger) *db.Store {
	store, err := db.Open(cfg.DataDir)
	if err != nil {
		if errors.Is(err, db.ErrLocked) {
			log.Error("another supabackup instance holds the advisory lock on this data directory",
				"data_dir", cfg.DataDir)
			os.Exit(1)
		}
		fatal(log, err)
	}
	return store
}

// openStoreCLIWithSchema opens the store for a CLI utility command. If the
// exclusive lock is free (server not running), it migrates the schema under
// the lock; otherwise it attaches lockless and only verifies the schema is
// already migrated — CLI commands never migrate beside a running server
// (review P1-06).
func openStoreCLIWithSchema(cfg *config.Config, log *slog.Logger) (*db.Store, error) {
	store, err := db.Open(cfg.DataDir)
	if err == nil {
		// We hold the lock: safe to migrate.
		if err := store.Migrate(context.Background()); err != nil {
			store.Close()
			return nil, err
		}
		return store, nil
	}
	if !errors.Is(err, db.ErrLocked) {
		return nil, err
	}
	store, err = db.OpenForCLI(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	if err := store.EnsureFreshSchemaForCLI(context.Background()); err != nil {
		store.Close()
		return nil, fmt.Errorf("cannot run beside an initializing server: %w", err)
	}
	return store, nil
}

func fatal(log *slog.Logger, err error) {
	log.Error("fatal", "err", err)
	os.Exit(1)
}

func runServe() error {
	log := newLogger()
	cfg := mustLoadConfig(log)

	// Lock first, then create the secret: two concurrent first starts would
	// otherwise race to write different keys (review P1-04).
	store := openStore(cfg, log)
	defer store.Close()

	// Protocol B: the application master secret encrypts stored credentials
	// (used from Phase 2). Losing it never affects backup-file recoverability.
	key, err := config.LoadOrCreateSecret(cfg.SecretFile)
	if err != nil {
		return fmt.Errorf("load master secret: %w", err)
	}
	fingerprint := sha256.Sum256(key)
	log.Info("master secret loaded", "fingerprint", fmt.Sprintf("%x", fingerprint[:6]))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	authStore := auth.NewStore(store.DB)
	srv := server.New(store, authStore, cfg, key, limiter.New(), log, server.BuildInfo{
		Version: version, Commit: commit, BuildDate: buildDate,
	})

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// Hourly purge of expired sessions and bootstrap tokens.
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := authStore.PurgeExpired(context.Background()); err != nil {
					log.Error("purge expired", "err", err)
				}
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "data_dir", cfg.DataDir)
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Error("graceful shutdown", "err", err)
		}
		return nil
	}
}

func runBootstrap() error {
	log := newLogger()
	cfg := mustLoadConfig(log)
	store, err := openStoreCLIWithSchema(cfg, log)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()
	authStore := auth.NewStore(store.DB)
	if has, err := authStore.HasUser(ctx); err != nil {
		return err
	} else if has {
		return errors.New("already initialized: use `supabackup reset-password <username>` instead")
	}
	token, err := authStore.CreateBootstrapToken(cfg.BootstrapTokenTTL)
	if err != nil {
		return err
	}
	fmt.Printf("One-time bootstrap token (valid %s):\n\n  %s\n\nUse it in the web UI to create the admin account. It will not be shown again.\n",
		cfg.BootstrapTokenTTL, token)
	return nil
}

func runResetPassword(username string) error {
	log := newLogger()
	cfg := mustLoadConfig(log)
	store, err := openStoreCLIWithSchema(cfg, log)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()
	newPass, err := auth.NewStore(store.DB).ResetPassword(ctx, username)
	if err != nil {
		return err
	}
	fmt.Printf("New password for %s (all existing sessions were revoked):\n\n  %s\n\nStore it in your password manager; it will not be shown again.\n",
		username, newPass)
	return nil
}

// runHealthcheck is used by the container HEALTHCHECK: probe this instance's
// liveness endpoint with a short timeout. Exit 0 = alive.
func runHealthcheck() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return fmt.Errorf("invalid SB_ADDR %q: %w", cfg.Addr, err)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	url := fmt.Sprintf("http://%s:%s/api/healthz", host, port)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("liveness probe %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("liveness probe %s: status %d", url, resp.StatusCode)
	}
	return nil
}
