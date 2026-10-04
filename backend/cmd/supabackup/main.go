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
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"crypto/sha256"
	"github.com/cloudfan/supabackup/backend/internal/agekey"
	"github.com/cloudfan/supabackup/backend/internal/auth"
	"github.com/cloudfan/supabackup/backend/internal/config"
	"github.com/cloudfan/supabackup/backend/internal/db"
	"github.com/cloudfan/supabackup/backend/internal/jobs"
	"github.com/cloudfan/supabackup/backend/internal/limiter"
	"github.com/cloudfan/supabackup/backend/internal/scheduler"
	"github.com/cloudfan/supabackup/backend/internal/server"
	"github.com/cloudfan/supabackup/backend/internal/staging"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	// Strip inherited PG* variables FIRST: both pgx and libpq honor them, and
	// a hostile/foreign environment (PGOPTIONS, PGSERVICE, PGSSLROOTCERT…)
	// must never alter backup semantics (round-1 review P1-08).
	for _, kv := range os.Environ() {
		for _, name := range strings.SplitN(kv, "=", 2) {
			if strings.HasPrefix(name, "PG") {
				_ = os.Unsetenv(name)
			}
		}
	}

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
	case "age":
		if len(os.Args) < 3 {
			err = errors.New("usage: supabackup age [init|show|verify --identity-file <path>]")
			break
		}
		err = runAge(os.Args[2], os.Args[3:])
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
		if err := store.CheckSchemaCompatibility(context.Background()); err != nil {
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
	// Refuse to serve on top of a database written by a NEWER binary.
	if err := store.CheckSchemaCompatibility(ctx); err != nil {
		return err
	}

	authStore := auth.NewStore(store.DB)
	srv := server.New(store, authStore, cfg, key, limiter.New(), log, server.BuildInfo{
		Version: version, Commit: commit, BuildDate: buildDate,
	})

	// Backup kernel (Phase 2): staging orphans → interrupted recovery → worker.
	stg := staging.New(cfg.DataDir)
	if err := stg.Ensure(); err != nil {
		return fmt.Errorf("prepare staging: %w", err)
	}
	if removed, err := stg.OrphanCleanupStartup(); err != nil {
		// A credential dir that cannot be removed is a security finding.
		log.Error("staging orphan cleanup incomplete", "removed", removed, "err", err)
	} else if len(removed) > 0 {
		log.Info("staging orphans removed", "count", len(removed))
	}
	runner := jobs.NewRunner(store, key, stg.Dir,
		func(ctx context.Context) (string, error) { return srv.RecipientFor(ctx) }, log)
	runner.SetQuota(cfg.StagingQuotaBytes)
	runner.SetLocalKeep(cfg.LocalKeep)
	if hb := os.Getenv("SB_HEARTBEAT_URL"); hb != "" {
		runner.SetHeartbeatURL(hb)
		log.Info("heartbeat configured", "url_prefix", hb[:min(len(hb), 20)])
	}
	srv.SetRunner(runner, stg.Dir)
	if n, err := runner.RecoverInterrupted(ctx); err != nil {
		return fmt.Errorf("recover interrupted jobs: %w", err)
	} else if n > 0 {
		log.Warn("jobs interrupted by previous shutdown", "count", n)
	}
	// Protocol C restart convergence: complete interrupted remote commits
	// BEFORE the worker claims new jobs.
	runner.ResumeRemotePhase(ctx)
	runner.Start(ctx)
	defer runner.Stop()

	// Phase 4: cron scheduler for automatic backups.
	sched := scheduler.New(store.DB, runner, log, 30*time.Second)
	if whs, werr := scheduler.LoadWebhooks(ctx, store.DB); werr == nil && len(whs) > 0 {
		sched.SetWebhooks(whs)
		log.Info("webhooks loaded", "count", len(whs))
	}
	sched.Start(ctx)
	defer sched.Stop()

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		// ReadTimeout bounds the whole body read for control-plane JSON;
		// streaming backup endpoints (later phases) get per-request deadlines.
		ReadTimeout:    30 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
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

// runAge implements the protocol-B key lifecycle:
//   - init: generate a keypair, store ONLY the recipient, print the private
//     key exactly once with explicit loss warnings;
//   - show: print the stored recipient and its fingerprint;
//   - verify: round-trip a canary through encrypt(recipient)/decrypt(identity)
//     to prove the offline identity matches before the first real backup.
func runAge(action string, args []string) error {
	log := newLogger()
	cfg := mustLoadConfig(log)

	switch action {
	case "init":
		store, err := db.OpenForCLI(cfg.DataDir)
		if err != nil {
			return err
		}
		defer store.Close()
		ctx := context.Background()
		if err := store.EnsureFreshSchemaForCLI(ctx); err != nil {
			return err
		}
		var existing string
		_ = store.DB.QueryRow(`SELECT value FROM settings WHERE key = 'age_recipient'`).Scan(&existing)
		if existing != "" {
			return fmt.Errorf("age recipient already configured (key id %s); rotating requires a deliberate decision", agekey.Fingerprint(existing))
		}
		identity, recipient, err := agekey.Generate()
		if err != nil {
			return err
		}
		// stdout carries EXACTLY a standard age identity file (comment lines
		// + key line) so `supabackup age init > identity.txt` can be fed back
		// to `age verify` and the official age tool; all guidance goes to
		// stderr (round-1 review P2-06).
		if _, err := fmt.Fprintf(os.Stdout, "# generated by supabackup — save OFFLINE; losing it makes backups unreadable\n%s\n", identity); err != nil {
			return fmt.Errorf("could not write the identity to stdout; NOTHING has been configured, retry: %w", err)
		}
		if _, err := fmt.Fprintf(os.Stderr, `age keypair generated.

The file you just captured (stdout) is the ONLY way to decrypt every
backup created from now on. Save it offline NOW. It is NOT stored by
supabackup and will never be shown again. Verify it with:

  supabackup age verify --identity-file <file>

Recipient (public, stored in supabackup): %s
Key ID: %s
`, recipient, agekey.Fingerprint(recipient)); err != nil {
			// The identity was delivered; stderr guidance failing is not fatal,
			// but the user must know configuration succeeded regardless.
			fmt.Fprintln(os.Stderr, "(stderr write failed)")
		}
		if _, err := store.DB.Exec(
			`INSERT INTO settings (key, value) VALUES ('age_recipient', ?), ('age_key_id', ?)`,
			recipient, agekey.Fingerprint(recipient)); err != nil {
			return err
		}
		return nil

	case "show":
		store, err := db.OpenForCLI(cfg.DataDir)
		if err != nil {
			return err
		}
		defer store.Close()
		var recipient, keyID string
		err = store.DB.QueryRow(`SELECT value FROM settings WHERE key = 'age_recipient'`).Scan(&recipient)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("age is not configured yet; run `supabackup age init`")
		}
		if err != nil {
			return err
		}
		_ = store.DB.QueryRow(`SELECT value FROM settings WHERE key = 'age_key_id'`).Scan(&keyID)
		fmt.Printf("recipient: %s\nkey id:    %s\n", recipient, keyID)
		return nil

	case "verify":
		var identityFile string
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "--identity-file" {
				identityFile = args[i+1]
			}
		}
		if identityFile == "" {
			return errors.New("usage: supabackup age verify --identity-file <path>")
		}
		raw, err := os.ReadFile(identityFile)
		if err != nil {
			return err
		}
		store, err := db.OpenForCLI(cfg.DataDir)
		if err != nil {
			return err
		}
		defer store.Close()
		var recipient string
		if err := store.DB.QueryRow(`SELECT value FROM settings WHERE key = 'age_recipient'`).Scan(&recipient); err != nil {
			return errors.New("age is not configured yet; run `supabackup age init`")
		}
		canary := fmt.Sprintf("supabackup canary %d", time.Now().UnixNano())
		var enc strings.Builder
		if err := agekey.EncryptStream(recipient, strings.NewReader(canary), &enc); err != nil {
			return fmt.Errorf("encrypt canary: %w", err)
		}
		var dec strings.Builder
		if err := agekey.DecryptStream(string(raw), strings.NewReader(enc.String()), &dec); err != nil {
			return fmt.Errorf("DECRYPT FAILED — this identity does not match the stored recipient: %w", err)
		}
		if dec.String() != canary {
			return errors.New("round-trip mismatch after decryption")
		}
		id, _ := agekey.ParseIdentity(string(raw))
		fmt.Printf("OK: identity matches recipient %s (key id %s)\n", recipient, agekey.Fingerprint(recipient))
		_ = id
		return nil

	default:
		return fmt.Errorf("unknown age action %q", action)
	}
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
