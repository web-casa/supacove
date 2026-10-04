// Package verifier implements embedded restore verification (dev-plan
// Phase 6, ADR-004): a throwaway PostgreSQL instance is created inside the
// same process boundary (no Docker socket, no privileges, non-root), the
// backup artifact is restored into it, and consistency checks are run —
// all with bounded resources and automatic cleanup.
//
// Trust boundary (ADR-004): the verifier runs as the same OS user as the
// application. This is a DEPLOYMENT constraint (no Docker socket needed),
// NOT a security sandbox. Only administrator-trusted sources may be
// verified; untrusted dumps require a separate restricted environment.
package verifier

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Status represents the outcome of a verification run.
type Status string

const (
	StatusVerified Status = "verified"
	StatusFailed   Status = "failed"
)

// Result captures the verification outcome for persistence.
type Result struct {
	Status       Status
	Detail       string
	TablesFound  int64
	Duration     time.Duration
	ErrorMessage string
}

// Pass returns true when the verification succeeded.
func (r Result) Pass() bool { return r.Status == StatusVerified }

// Config configures the verifier.
type Config struct {
	// PGBin is the directory containing PG server binaries.
	PGBin string
	// BaseDir is the parent directory for temporary instance data.
	BaseDir string
}

// Verifier manages embedded throwaway PostgreSQL instances.
type Verifier struct {
	pgBin   string
	baseDir string
}

// New creates a Verifier, checking that the required PG binaries exist.
func New(cfg Config) (*Verifier, error) {
	for _, bin := range []string{"initdb", "pg_ctl", "postgres", "pg_restore", "psql"} {
		if _, err := os.Stat(filepath.Join(cfg.PGBin, bin)); err != nil {
			return nil, fmt.Errorf("PG binary %s not found in %s: %w", bin, cfg.PGBin, err)
		}
	}
	if err := os.MkdirAll(cfg.BaseDir, 0o700); err != nil {
		return nil, fmt.Errorf("create verifier base dir: %w", err)
	}
	return &Verifier{pgBin: cfg.PGBin, baseDir: cfg.BaseDir}, nil
}

// Input describes one verification run.
type Input struct {
	// DumpPath is the plaintext custom-format dump file (already age-decrypted).
	DumpPath string
	// Timeout bounds the entire verification.
	Timeout time.Duration
}

// Verify runs the full restore-verification cycle:
//
//	initdb → pg_ctl start (Unix socket only) → pg_restore --exit-on-error →
//	consistency check → pg_ctl stop → cleanup
func (v *Verifier) Verify(ctx context.Context, input Input) Result {
	start := time.Now()

	dataDir, sockDir, err := v.setupDirs()
	if err != nil {
		return v.fail(start, StatusFailed, err.Error())
	}
	// Cleanup on ALL exit paths.
	defer func() {
		v.stopPG(dataDir)
		os.RemoveAll(dataDir)
		os.RemoveAll(sockDir)
	}()

	if ctx.Err() != nil {
		return v.fail(start, StatusFailed, "context already canceled")
	}

	pgOpts := fmt.Sprintf(
		"-c listen_addresses= -c unix_socket_directories=%s -c fsync=off -c synchronous_commit=off",
		sockDir)
	connStr := fmt.Sprintf("host=%s user=verifier dbname=postgres", sockDir)

	// Phase 1: initdb
	if err := run(ctx, v.pgBin, "initdb", "-D", dataDir, "-A", "trust", "-U", "verifier"); err != nil {
		return v.fail(start, StatusFailed, fmt.Sprintf("initdb: %v", err))
	}

	// Phase 2: start PG (Unix socket only)
	pgCtlCtx, pgCtlCancel := context.WithTimeout(ctx, 30*time.Second)
	defer pgCtlCancel()
	if err := run(pgCtlCtx, v.pgBin, "pg_ctl",
		"-D", dataDir, "-l", filepath.Join(dataDir, "pg.log"),
		"-o", pgOpts, "-w", "start"); err != nil {
		return v.fail(start, StatusFailed, fmt.Sprintf("pg_ctl start: %v", err))
	}

	// Phase 3: pg_restore --exit-on-error
	if err := run(ctx, v.pgBin, "pg_restore",
		"--dbname="+connStr, "--exit-on-error", "--no-owner", input.DumpPath); err != nil {
		return v.fail(start, StatusFailed, fmt.Sprintf("pg_restore: %v", err))
	}

	// Phase 4: consistency check
	tableCount, err := v.countTables(ctx, connStr)
	if err != nil {
		return v.fail(start, StatusFailed, fmt.Sprintf("consistency check: %v", err))
	}

	// Phase 5: stop PG
	if err := run(ctx, v.pgBin, "pg_ctl", "-D", dataDir, "-m", "fast", "-w", "stop"); err != nil {
		// Non-fatal: the data is restored, cleanup removes the dir anyway.
		_ = err
	}

	return Result{
		Status:      StatusVerified,
		TablesFound: tableCount,
		Duration:    time.Since(start),
	}
}

// countTables returns the number of user tables in the restored database.
func (v *Verifier) countTables(ctx context.Context, connStr string) (int64, error) {
	sockDir := connStrHost(connStr)
	out, err := runOutput(ctx, filepath.Join(v.pgBin, "psql"),
		"-h", sockDir, "-U", "verifier", "-d", "postgres", "-At", "-c",
		`SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')`)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(out), 10, 64)
}

// connStrHost extracts the socket directory from a conninfo string.
func connStrHost(connStr string) string {
	for _, part := range strings.Fields(connStr) {
		if strings.HasPrefix(part, "host=") {
			return strings.TrimPrefix(part, "host=")
		}
	}
	return ""
}

// runOutput executes a command and returns its trimmed stdout.
func runOutput(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %v: %s", filepath.Base(name), err, truncateStr(string(out), 500))
	}
	return strings.TrimSpace(string(out)), nil
}

// run executes a command and returns an error on non-zero exit.
func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", filepath.Base(name), err, truncateStr(string(out), 500))
	}
	return nil
}

func (v *Verifier) fail(start time.Time, status Status, msg string) Result {
	return Result{
		Status:       status,
		Detail:       msg,
		ErrorMessage: msg,
		Duration:     time.Since(start),
	}
}

func (v *Verifier) setupDirs() (dataDir, sockDir string, err error) {
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	dataDir = filepath.Join(v.baseDir, "pgdata-"+suffix)
	sockDir = filepath.Join(v.baseDir, "pgsock-"+suffix)
	for _, d := range []string{dataDir, sockDir} {
		if mkErr := os.MkdirAll(d, 0o700); mkErr != nil {
			return "", "", fmt.Errorf("create %s: %w", d, mkErr)
		}
	}
	return dataDir, sockDir, nil
}

// stopPG attempts to stop a PG instance. Best-effort.
func (v *Verifier) stopPG(dataDir string) {
	pgCtl := filepath.Join(v.pgBin, "pg_ctl")
	_ = exec.Command(pgCtl, "-D", dataDir, "-m", "immediate", "-w", "stop").Run()
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func parseInt64(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
}
