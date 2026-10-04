// Package verifier implements embedded restore verification (dev-plan
// Phase 6, ADR-004): a throwaway PostgreSQL instance is created inside the
// same process boundary (no Docker socket, no privileges, non-root), the
// committed backup artifact is hash-checked, DECRYPTED into a restricted
// temporary file, restored into the throwaway instance, and checked against
// the manifest's expected object set — all with bounded output, bounded
// time and confirmed cleanup.
//
// Trust boundary (ADR-004): the verifier runs as the same OS user as the
// application. This is a DEPLOYMENT constraint (no Docker socket needed),
// NOT a security sandbox: a verified dump can read any file the application
// can read. It only runs when the administrator explicitly enables it
// (SB_VERIFY_ENABLED) AND provides the age identity — a deliberate,
// auditable decision, never a default.
package verifier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/agekey"
)

// Status represents the outcome of a verification run.
type Status string

const (
	StatusVerified    Status = "verified"
	StatusFailed      Status = "failed"
	StatusUnsupported Status = "unsupported" // manifest requires extensions this PG build cannot provide
)

// Result captures the verification outcome for persistence.
type Result struct {
	Status        Status
	Detail        string
	TablesFound   int64
	Duration      time.Duration
	ErrorMessage  string
	ServerVersion string // version() of the embedded instance (profile evidence)
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

// CleanupResidual removes leftover working directories from a previous
// crash (phase-5 review P1-06: startup recovery of verification residue).
// Errors are collected and returned; they never panic.
func (v *Verifier) CleanupResidual() error {
	entries, err := os.ReadDir(v.baseDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "verify-") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(v.baseDir, e.Name())); err != nil {
			errs = append(errs, fmt.Errorf("remove residual %s: %w", e.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// Input describes one verification run. The ciphertext is hash-checked
// BEFORE decryption, so the verification covers exactly the committed
// bytes; the manifest facts (expected tables/extensions) come from the
// same backup run (phase-5 review P1-05/P1-08).
type Input struct {
	// CiphertextPath is the age-encrypted artifact (the committed staging file).
	CiphertextPath string
	// SHA256Hex is the expected SHA-256 of the CIPHERTEXT.
	SHA256Hex string
	// Identity is the age identity (PEM text). Empty identity is a
	// configuration error — verification never silently skips decryption.
	Identity string
	// ExpectedTables is the manifest's dump-time user-table count; -1 when
	// the manifest predates the field and the count is unknown.
	ExpectedTables int64
	// ExpectedExtensions lists extension names the manifest recorded.
	ExpectedExtensions []string
	// Timeout bounds the entire verification (wall time).
	Timeout time.Duration
}

// Verify runs the full restore-verification cycle:
//
//	ciphertext hash → age decrypt (restricted temp) → initdb →
//	pg_ctl start (Unix socket only) → extension availability check →
//	pg_restore --exit-on-error → expected-object-set check →
//	pg_ctl stop (confirmed) → cleanup
func (v *Verifier) Verify(ctx context.Context, input Input) Result {
	start := time.Now()
	fail := func(status Status, format string, args ...any) Result {
		msg := fmt.Sprintf(format, args...)
		return Result{Status: status, Detail: msg, ErrorMessage: msg, Duration: time.Since(start)}
	}

	if input.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, input.Timeout)
		defer cancel()
	}
	if ctx.Err() != nil {
		return fail(StatusFailed, "context canceled before start: %v", ctx.Err())
	}
	if input.Identity == "" {
		return fail(StatusFailed, "no age identity configured — automatic restore verification requires it (see ADR-004 enablement)")
	}
	if input.CiphertextPath == "" {
		return fail(StatusFailed, "no ciphertext path provided")
	}

	// One working directory holding everything (pgdata, socket dir,
	// plaintext) so cleanup and residual recovery have a single target.
	workDir := filepath.Join(v.baseDir, "verify-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return fail(StatusFailed, "create verify workdir: %v", err)
	}
	dataDir := filepath.Join(workDir, "pgdata")
	sockDir := filepath.Join(workDir, "pgsock")
	plainPath := filepath.Join(workDir, "plaintext.dump")
	for _, d := range []string{dataDir, sockDir} {
		if err := os.Mkdir(d, 0o700); err != nil {
			os.RemoveAll(workDir) // partial creation must not leak the first dir (P1-06)
			return fail(StatusFailed, "create %s: %v", d, err)
		}
	}
	// Plaintext FIRST: it is the most sensitive residue. Then the whole
	// workdir (which includes the plaintext anyway, but order documents
	// intent and bounds exposure if RemoveAll were interrupted).
	defer os.Remove(plainPath)
	defer os.RemoveAll(workDir)

	env := minimalEnv(workDir)

	// 1) Ciphertext integrity (covers the exact committed bytes; this is
	// error detection, not a signature).
	if err := hashFile(input.CiphertextPath, input.SHA256Hex); err != nil {
		return fail(StatusFailed, "ciphertext check: %v", err)
	}
	// 2) Disk budget (phase-5 review P1-06): the restore peak needs the
	// plaintext dump AND a PG data directory of roughly that size. Refuse —
	// before touching anything — when free space cannot cover 2× the
	// ciphertext plus a floor margin.
	if err := checkDiskBudget(v.baseDir, input.CiphertextPath); err != nil {
		return fail(StatusFailed, "disk budget: %v", err)
	}

	// 3) Decrypt into the restricted temp file (0600 by umask-enforced mode).
	if err := decryptToFile(input.Identity, input.CiphertextPath, plainPath); err != nil {
		return fail(StatusFailed, "age decrypt: %v", err)
	}

	pgOpts := fmt.Sprintf(
		"-c listen_addresses= -c unix_socket_directories=%s -c fsync=off -c synchronous_commit=off",
		sockDir)
	connStr := fmt.Sprintf("host=%s user=verifier dbname=postgres", sockDir)

	// 4) initdb
	if err := v.run(ctx, env, "initdb", "-D", dataDir, "-A", "trust", "-U", "verifier"); err != nil {
		return fail(StatusFailed, "initdb: %v", err)
	}

	// 5) start PG (Unix socket only), bounded start window.
	pgCtlCtx, pgCtlCancel := context.WithTimeout(ctx, 30*time.Second)
	err := v.run(pgCtlCtx, env, "pg_ctl",
		"-D", dataDir, "-l", filepath.Join(dataDir, "pg.log"),
		"-o", pgOpts, "-w", "start")
	pgCtlCancel()
	if err != nil {
		return fail(StatusFailed, "pg_ctl start: %v", err)
	}

	// 6) Extension availability — BEFORE restoring: a manifest requiring
	// extensions this PG build cannot provide is 'unsupported', not a
	// restore failure (P1-08 classification).
	if len(input.ExpectedExtensions) > 0 {
		missing, err := v.missingExtensions(ctx, env, sockDir, input.ExpectedExtensions)
		if err != nil {
			return fail(StatusFailed, "extension availability check: %v", err)
		}
		if len(missing) > 0 {
			return fail(StatusUnsupported,
				"embedded PG instance cannot provide required extensions: %s", strings.Join(missing, ", "))
		}
	}

	// 7) pg_restore --exit-on-error
	if err := v.run(ctx, env, "pg_restore",
		"--dbname="+connStr, "--exit-on-error", "--no-owner", plainPath); err != nil {
		return fail(StatusFailed, "pg_restore: %v (target may be partially restored; instance is discarded)", err)
	}

	// 8) Expected-object-set check: restored user tables vs manifest count;
	// restored extensions vs manifest list. Zero-error restore alone does
	// NOT prove the object set survived (P1-08).
	tables, err := v.countTables(ctx, env, sockDir)
	if err != nil {
		return fail(StatusFailed, "consistency check: %v", err)
	}
	if input.ExpectedTables >= 0 && tables != input.ExpectedTables {
		return fail(StatusFailed,
			"object-set mismatch: manifest declares %d user tables, restore produced %d",
			input.ExpectedTables, tables)
	}
	if len(input.ExpectedExtensions) > 0 {
		missing, err := v.missingInstalledExtensions(ctx, env, sockDir, input.ExpectedExtensions)
		if err != nil {
			return fail(StatusFailed, "extension presence check: %v", err)
		}
		if len(missing) > 0 {
			return fail(StatusFailed, "extensions missing after restore: %s", strings.Join(missing, ", "))
		}
	}
	serverVersion := v.serverVersion(ctx, env, sockDir)

	// 9) Confirmed stop: fast stop, verify postmaster.pid is gone, escalate
	// to immediate; a still-running instance is reported, never silent.
	stopWarn := v.stopConfirmed(ctx, env, dataDir)

	res := Result{
		Status:        StatusVerified,
		TablesFound:   tables,
		Duration:      time.Since(start),
		ServerVersion: serverVersion,
		Detail: fmt.Sprintf("restored %d user tables into embedded PostgreSQL (%s); expected-object set matched",
			tables, profileName(serverVersion)),
	}
	if stopWarn != "" {
		res.Detail += "; " + stopWarn
	}
	return res
}

// checkDiskBudget refuses to start when free space on the verifier base
// directory cannot cover the restore peak: the plaintext dump (which can be
// much larger than the compressed ciphertext) plus a PG data directory of
// comparable size. Budget: 2× the ciphertext size + 256 MiB floor.
func checkDiskBudget(baseDir, ciphertextPath string) error {
	var st syscall.Statfs_t
	if err := syscall.Statfs(baseDir, &st); err != nil {
		return err
	}
	free := st.Bavail * uint64(st.Bsize)
	info, err := os.Stat(ciphertextPath)
	if err != nil {
		return err
	}
	need := uint64(info.Size())*2 + 256<<20
	if free < need {
		return fmt.Errorf("insufficient free space for verification: %d bytes available, need ~%d (plaintext + throwaway instance)", free, need)
	}
	return nil
}

// profileName derives the persisted verify_profile label: the embedded
// profile (local throwaway instance) plus the server major it ran on.
func profileName(serverVersion string) string {
	ver := strings.TrimSpace(serverVersion)
	if ver == "" {
		return "embedded-local:unknown"
	}
	fields := strings.Fields(ver)
	if len(fields) >= 2 {
		return "embedded-local:" + fields[1]
	}
	return "embedded-local:" + ver
}

// stopConfirmed stops the throwaway instance and confirms it is gone.
func (v *Verifier) stopConfirmed(ctx context.Context, env []string, dataDir string) string {
	stopCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	err := v.run(stopCtx, env, "pg_ctl", "-D", dataDir, "-m", "fast", "-w", "stop")
	cancel()
	pidFile := filepath.Join(dataDir, "postmaster.pid")
	if err == nil && !fileExists(pidFile) {
		return ""
	}
	// Escalate once; then report honestly.
	escCtx, escCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	err = v.run(escCtx, env, "pg_ctl", "-D", dataDir, "-m", "immediate", "-w", "stop")
	escCancel()
	if fileExists(pidFile) {
		return "WARNING: embedded postgres may still be running (postmaster.pid present after stop)"
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// hashFile streams the file and compares it to the expected hex digest.
func hashFile(path, wantHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		return fmt.Errorf("sha256 mismatch (want %s, got %s)", wantHex, got)
	}
	return nil
}

// decryptToFile writes the plaintext dump at 0600 inside the restricted
// workdir (the process umask is not relied on; the mode is explicit).
func decryptToFile(identity, ciphertextPath, plainPath string) error {
	src, err := os.Open(ciphertextPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(plainPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := agekey.DecryptStream(identity, src, dst); err != nil {
		dst.Close()
		os.Remove(plainPath)
		return err
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		os.Remove(plainPath)
		return err
	}
	return dst.Close()
}

// minimalEnv builds the subprocess environment from an explicit whitelist
// (ADR-004): the application's environment — credentials, PG* libpq
// settings, proxy vars — is NEVER inherited. HOME points into the workdir
// (writable, throwaway); PATH is kept only so PG utilities can locate
// helpers (no secret material).
func minimalEnv(home string) []string {
	env := []string{
		"HOME=" + home,
		"LC_ALL=C",
		"LANG=C",
		"TZ=UTC",
	}
	if p := os.Getenv("PATH"); p != "" {
		env = append(env, "PATH="+p)
	}
	return env
}

const userTablesSQL = `SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema','pg_toast')`

// countTables returns the number of user tables in the restored database.
func (v *Verifier) countTables(ctx context.Context, env []string, sockDir string) (int64, error) {
	out, err := v.runOutput(ctx, env, "psql",
		"-h", sockDir, "-U", "verifier", "-d", "postgres", "-X", "-At", "-v", "ON_ERROR_STOP=1",
		"-c", userTablesSQL)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("table count not numeric: %q", out)
	}
	return n, nil
}

// missingExtensions returns manifest extensions absent from
// pg_available_extensions (pre-restore feasibility).
func (v *Verifier) missingExtensions(ctx context.Context, env []string, sockDir string, expected []string) ([]string, error) {
	out, err := v.runOutput(ctx, env, "psql",
		"-h", sockDir, "-U", "verifier", "-d", "postgres", "-X", "-At", "-v", "ON_ERROR_STOP=1",
		"-c", `SELECT name FROM pg_available_extensions`)
	if err != nil {
		return nil, err
	}
	available := make(map[string]bool)
	for _, name := range strings.Split(out, "\n") {
		available[strings.TrimSpace(name)] = true
	}
	var missing []string
	for _, e := range expected {
		if !available[e] {
			missing = append(missing, e)
		}
	}
	return missing, nil
}

// missingInstalledExtensions returns manifest extensions absent from
// pg_extension after the restore (post-restore object-set proof).
func (v *Verifier) missingInstalledExtensions(ctx context.Context, env []string, sockDir string, expected []string) ([]string, error) {
	out, err := v.runOutput(ctx, env, "psql",
		"-h", sockDir, "-U", "verifier", "-d", "postgres", "-X", "-At", "-v", "ON_ERROR_STOP=1",
		"-c", `SELECT extname FROM pg_extension`)
	if err != nil {
		return nil, err
	}
	installed := make(map[string]bool)
	for _, name := range strings.Split(out, "\n") {
		installed[strings.TrimSpace(name)] = true
	}
	var missing []string
	for _, e := range expected {
		if !installed[e] {
			missing = append(missing, e)
		}
	}
	return missing, nil
}

func (v *Verifier) serverVersion(ctx context.Context, env []string, sockDir string) string {
	out, err := v.runOutput(ctx, env, "psql",
		"-h", sockDir, "-U", "verifier", "-d", "postgres", "-X", "-At", "-c",
		`SELECT version()`)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// boundedBuffer collects at most max bytes and records truncation, so a
// chatty subprocess can never balloon memory before an error is formatted
// (phase-5 review P1-06).
type boundedBuffer struct {
	buf       []byte
	max       int
	truncated bool
}

func newBoundedBuffer(max int) *boundedBuffer { return &boundedBuffer{max: max} }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(b.buf) < b.max {
		room := b.max - len(b.buf)
		if len(p) <= room {
			b.buf = append(b.buf, p...)
		} else {
			b.buf = append(b.buf, p[:room]...)
			b.truncated = true
		}
	} else {
		b.truncated = true
	}
	return len(p), nil // always report full consumption
}

func (b *boundedBuffer) String() string {
	s := string(b.buf)
	if b.truncated {
		s += "…[truncated]"
	}
	return s
}

// runOutput executes a command with the whitelisted environment and returns
// its trimmed, bounded combined stdout/stderr.
func (v *Verifier) runOutput(ctx context.Context, env []string, name string, args ...string) (string, error) {
	full := filepath.Join(v.pgBin, name)
	cmd := exec.CommandContext(ctx, full, args...)
	cmd.Env = env
	bb := newBoundedBuffer(64 * 1024)
	cmd.Stdout = bb
	cmd.Stderr = bb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %v: %s", name, err, bb.String())
	}
	return strings.TrimSpace(bb.String()), nil
}

// run executes a command and returns an error on non-zero exit.
func (v *Verifier) run(ctx context.Context, env []string, name string, args ...string) error {
	_, err := v.runOutput(ctx, env, name, args...)
	return err
}
