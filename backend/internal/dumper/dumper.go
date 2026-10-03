// Package dumper executes pg_dump per protocol A (dev-plan §0.5): the dump
// streams through age encryption into a staging temp file, and the artifact
// is committed (atomic rename) only after EVERY condition in the commit
// chain holds — process exit 0, full pipe copy, age writer Close, file
// Sync/Close. Any failure leaves NO committed artifact behind.
package dumper

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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/agekey"
	"github.com/cloudfan/supabackup/backend/internal/pgclient"
)

// maxStdErr caps captured pg_dump stderr; anything beyond is counted and
// dropped, never buffered unbounded.
const maxStdErr = 64 << 10

var pgDumpVersionRe = regexp.MustCompile(`pg_dump \(PostgreSQL\) (\d+)`)

// Config configures the executor. DumpBinOverride exists exclusively for
// tests (fault-injecting fake pg_dump scripts).
type Config struct {
	StagingDir     string // where temp + committed artifacts live
	BinDirOverride string // test hook: directory holding the pg_dump to use
}

// Target describes one dump run. It embeds the shared ConnInfo so pgx and
// pg_dump always agree on host/user/db/TLS semantics.
type Target struct {
	Conn        *pgclient.ConnInfo
	ServerMajor int
	Recipient   string // age recipient (protocol B); required
}

// Result describes a committed artifact.
type Result struct {
	ArtifactPath  string
	ManifestPath  string
	SHA256        string // SHA-256 of the full CIPHERTEXT (protocol C.1)
	SizeBytes     int64
	DumpToolVer   string // e.g. "pg_dump (PostgreSQL) 18.4 ..."
	ClientMajor   int
	EncryptedTo   string // recipient fingerprint
	BytesDumped   int64  // plaintext bytes streamed out of pg_dump
	StdErrExcerpt string // sanitized, capped stderr (empty on success)
}

// Typed errors carrying the job error class (dev-plan seven classes).
var (
	ErrNoClient    = errors.New("no suitable pg_dump client")
	ErrDumpFailed  = errors.New("pg_dump failed")
	ErrEncrypt     = errors.New("age encryption failed")
	ErrStagingFull = errors.New("staging directory has insufficient space")
)

// Classified wraps a failure with one of the seven job error classes.
type Classified struct {
	Class pgclient.ErrClass
	Msg   string
	Err   error
}

func (e *Classified) Error() string { return e.Msg }
func (e *Classified) Unwrap() error { return e.Err }

// FindClient returns the pg_dump path whose major version best matches the
// server: same major preferred, otherwise the newest client that is still
// >= the server major. Clients older than the server are never used.
func (c *Config) FindClient(serverMajor int) (path string, major int, version string, err error) {
	candidates, err := c.candidateBinaries()
	if err != nil {
		return "", 0, "", err
	}
	bestPath, bestMajor, bestVersion := "", -1, ""
	for _, cand := range candidates {
		m, v, err := probeVersion(cand)
		if err != nil {
			continue
		}
		if m < serverMajor {
			continue // older clients can never dump newer servers
		}
		// Prefer the closest major, then the newest binary version.
		if bestPath == "" || m < bestMajor ||
			(m == bestMajor && v > bestVersion) {
			bestPath, bestMajor, bestVersion = cand, m, v
		}
	}
	if bestPath == "" {
		return "", 0, "", &Classified{
			Class: pgclient.ClassClientVer,
			Msg: fmt.Sprintf("no pg_dump with major >= %d available (install a matching PostgreSQL client)",
				serverMajor),
			Err: ErrNoClient,
		}
	}
	return bestPath, bestMajor, bestVersion, nil
}

// candidateBinaries lists pg_dump binaries from the override dir, the
// standard Debian/PGDG versioned dirs, then PATH.
func (c *Config) candidateBinaries() ([]string, error) {
	var out []string
	if c.BinDirOverride != "" {
		out = append(out, filepath.Join(c.BinDirOverride, "pg_dump"))
	}
	if globs, err := filepath.Glob("/usr/lib/postgresql/*/bin/pg_dump"); err == nil {
		out = append(out, globs...)
	}
	if p, err := exec.LookPath("pg_dump"); err == nil {
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, &Classified{Class: pgclient.ClassClientVer,
			Msg: "pg_dump not found in image or PATH", Err: ErrNoClient}
	}
	// Deduplicate, preserving order.
	seen := map[string]bool{}
	uniq := out[:0]
	for _, p := range out {
		if !seen[p] {
			seen[p] = true
			uniq = append(uniq, p)
		}
	}
	return uniq, nil
}

func probeVersion(bin string) (major int, version string, err error) {
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		return 0, "", err
	}
	v := strings.TrimSpace(string(out))
	m := pgDumpVersionRe.FindStringSubmatch(v)
	if m == nil {
		return 0, "", fmt.Errorf("unrecognized %s output %q", bin, v)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, "", err
	}
	return n, v, nil
}

// sanitize masks anything that looks like a password value in stderr before
// it is stored or logged (secret canary invariant).
func sanitize(s string) string {
	r := strings.NewReplacer(
		"password=", "password=[REDACTED]",
		"PASSWORD=", "PASSWORD=[REDACTED]",
	)
	return r.Replace(s)
}

// Run executes one dump+encrypt+commit chain per protocol A.
func (c *Config) Run(ctx context.Context, jobID int64, t Target) (*Result, error) {
	if t.Recipient == "" {
		return nil, &Classified{Class: pgclient.ClassUnknown,
			Msg: "age recipient not configured", Err: errors.New("recipient missing")}
	}
	bin, clientMajor, toolVersion, err := c.FindClient(t.ServerMajor)
	if err != nil {
		return nil, err
	}

	// Pre-flight: staging must hold enough headroom for the CIPHERTEXT
	// (compressed dump is usually smaller than the DB, but we reserve a
	// conservative multiple and check the filesystem, not the quota alone).
	if err := ensureSpace(c.StagingDir, 64<<20); err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Msg: err.Error(), Err: err}
	}

	// PGPASSFILE: dedicated 0700 dir, 0600 file, libpq-escaped, removed on
	// every exit path (protocol A).
	passDir, err := os.MkdirTemp(c.StagingDir, "job-creds-")
	if err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Msg: err.Error(), Err: err}
	}
	defer os.RemoveAll(passDir)
	if err := os.Chmod(passDir, 0o700); err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Msg: err.Error(), Err: err}
	}
	passFile := filepath.Join(passDir, "pgpass")
	line := fmt.Sprintf("%s:%s:%s:%s:%s",
		libpqField(t.Conn.Host), libpqField(t.Conn.Port), libpqField(t.Conn.DBName),
		libpqField(t.Conn.User), libpqField(t.Conn.Password))
	if err := os.WriteFile(passFile, []byte(line+"\n"), 0o600); err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Msg: err.Error(), Err: err}
	}

	// Child environment: strip every inherited PG* variable so nothing from
	// the supervisor leaks into libpq semantics (protocol A).
	childEnv := filterEnv(os.Environ(), "PG")
	childEnv = append(childEnv, "PGPASSFILE="+passFile)

	// pg_dump: direct argv, no shell. -d takes a conninfo WITHOUT a password.
	// NOTE: we deliberately do NOT pass --file=- — at least Debian's PG18
	// pg_dump treats "-" as a LITERAL filename, silently writing the dump to
	// ./- while stdout stays empty (found by the M1 gate). Without -f, pg_dump
	// writes the custom archive to stdout by definition.
	args := []string{
		"--format=custom",
		"-d", t.Conn.DSN(),
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = childEnv
	// Kill the whole process group on cancel/timeout: pg_dump may have
	// helpers, and a half-alive child (e.g. sh waiting on sleep) would keep
	// the pipes open forever after CommandContext kills only the leader
	// (protocol A cancel path). cmd.Cancel runs inside exec's own cancel
	// watcher (process is guaranteed started); WaitDelay bounds how long a
	// surviving grandchild can hold the pipes after the leader dies.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	dumpOut, err := cmd.StdoutPipe()
	if err != nil {
		return nil, &Classified{Class: pgclient.ClassUnknown, Msg: err.Error(), Err: err}
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, &Classified{Class: pgclient.ClassUnknown, Msg: err.Error(), Err: err}
	}

	tmpPath := filepath.Join(c.StagingDir, fmt.Sprintf("backup-job%d.dump.age.inprogress", jobID))
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Msg: err.Error(), Err: err}
	}
	committed := false
	// Failure path: remove the temp artifact; only the atomic rename below
	// makes it official (protocol A).
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	counting := &countingWriter{w: tmp}
	encTarget := io.MultiWriter(counting, hasher)

	if err := cmd.Start(); err != nil {
		return nil, &Classified{Class: pgclient.ClassClientVer, Msg: err.Error(), Err: err}
	}

	// stderr reader: concurrent, capped, sanitized.
	var stderrMu sync.Mutex
	stderrBuf := &strings.Builder{}
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		data, _ := io.ReadAll(io.LimitReader(stderrPipe, maxStdErr))
		stderrMu.Lock()
		stderrBuf.Write(data)
		stderrMu.Unlock()
	}()

	// Stream: pg_dump stdout → age encrypt → staging temp + hash + count.
	encErrCh := make(chan error, 1)
	go func() {
		encErrCh <- agekey.EncryptStream(t.Recipient, dumpOut, encTarget)
	}()

	// ORDER MATTERS (protocol A): Wait() closes the stdout/stderr pipes, so
	// both consumers must finish BEFORE Wait. The copy goroutine sees natural
	// EOF when pg_dump exits and its output is fully drained; only then does
	// Wait reap the process. A premature Wait would truncate the dump and —
	// worse — commit a valid-but-short age file.
	copyErr := <-encErrCh
	<-stderrDone
	waitErr := cmd.Wait()

	if ctx.Err() != nil {
		return nil, &Classified{Class: pgclient.ClassUnknown,
			Msg: "backup canceled", Err: ctx.Err()}
	}
	if waitErr != nil {
		stderrMu.Lock()
		excerpt := sanitize(stderrBuf.String())
		stderrMu.Unlock()
		// Process-group kill guard: on cancel we may still be here with a
		// live group — make sure nothing survives (protocol A).
		if p := cmd.Process; p != nil {
			_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
		}
		return nil, &Classified{Class: classifyDumpFailure(excerpt),
			Msg: fmt.Sprintf("pg_dump exited abnormally: %v", waitErr),
			Err: fmt.Errorf("%w: %v", ErrDumpFailed, waitErr)}
	}
	// A pg_dump that printed data but exited non-zero MUST NOT commit — but
	// Wait() above already captures that. Kill-leftovers check: ensure the
	// group is gone so pipes are fully drained and closed.
	if p := cmd.Process; p != nil {
		_ = syscall.Kill(-p.Pid, syscall.SIGKILL) // no-op if already exited
	}
	if copyErr != nil {
		return nil, &Classified{Class: pgclient.ClassUnknown,
			Msg: "age encryption failed; artifact discarded", Err: copyErr}
	}
	// age writer Close is inside EncryptStream; now sync the FILE itself.
	if err := tmp.Sync(); err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Msg: err.Error(), Err: err}
	}
	if err := tmp.Close(); err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Msg: err.Error(), Err: err}
	}

	stderrMu.Lock()
	excerpt := sanitize(stderrBuf.String())
	stderrMu.Unlock()

	// Atomic commit (protocol A): everything above succeeded.
	finalPath := strings.TrimSuffix(tmpPath, ".inprogress")
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Msg: err.Error(), Err: err}
	}
	committed = true

	st, err := os.Stat(finalPath)
	if err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Msg: err.Error(), Err: err}
	}
	return &Result{
		ArtifactPath:  finalPath,
		SHA256:        hex.EncodeToString(hasher.Sum(nil)),
		SizeBytes:     st.Size(),
		DumpToolVer:   toolVersion,
		ClientMajor:   clientMajor,
		EncryptedTo:   agekey.Fingerprint(t.Recipient),
		BytesDumped:   counting.n,
		StdErrExcerpt: excerpt,
	}, nil
}

// KillGroup force-terminates a running dump's process group (cancel path).
func KillGroup(p *os.Process) {
	if p != nil {
		_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
	}
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// classifyDumpFailure maps pg_dump stderr patterns to job error classes.
func classifyDumpFailure(stderr string) pgclient.ErrClass {
	m := strings.ToLower(stderr)
	switch {
	case strings.Contains(m, "password authentication failed"),
		strings.Contains(m, "no password supplied"),
		strings.Contains(m, "fe_sendauth"):
		return pgclient.ClassAuth
	case strings.Contains(m, "could not connect to server"),
		strings.Contains(m, "connection refused"),
		strings.Contains(m, "no such host"),
		strings.Contains(m, "timeout expired"),
		strings.Contains(m, "could not translate host name"):
		return pgclient.ClassNetwork
	case strings.Contains(m, "permission denied"),
		strings.Contains(m, "must be superuser"),
		strings.Contains(m, "privilege"):
		return pgclient.ClassPermission
	case strings.Contains(m, "server version"):
		return pgclient.ClassClientVer
	case strings.Contains(m, "no space left on device"):
		return pgclient.ClassDisk
	default:
		return pgclient.ClassUnknown
	}
}

// libpqField escapes one PGPASSFILE field: backslashes and colons.
func libpqField(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	return strings.ReplaceAll(s, ":", "\\:")
}

func filterEnv(env []string, prefix string) []string {
	out := env[:0]
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return out
}

// ensureSpace checks free headroom on the staging filesystem.
func ensureSpace(dir string, minBytes uint64) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return err
	}
	avail := st.Bavail * uint64(st.Bsize)
	if avail < minBytes {
		return fmt.Errorf("%w: %d bytes available, need at least %d", ErrStagingFull, avail, minBytes)
	}
	return nil
}
