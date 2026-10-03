// Package dumper executes pg_dump per protocol A (dev-plan §0.5): the dump
// streams through age encryption into a staging temp file, and the artifact
// is committed (atomic rename + directory fsync) only after EVERY condition
// in the commit chain holds — process exit 0, full pipe drain, age writer
// Close, file Sync/Close. Any failure leaves NO committed artifact behind.
//
// Supervision model (round-2 review P1-01): the three IO participants —
// process, stdout consumer (age), stderr drainer — are supervised together.
// Every path that leaves the pipeline reads BOTH pipes to EOF, so pg_dump
// can never block on a full pipe; the first fatal error cancels the process
// group and all consumers converge with a bounded wait.
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

// stderrKeep is how much sanitized stderr is RETAINED; the pipe is always
// drained to EOF regardless (round-2 review P1-01).
const stderrKeep = 16 << 10

// waitAfterKill bounds the reaping wait after the process group is killed.
const waitAfterKill = 10 * time.Second

// Config configures the executor. DumpBinOverride exists exclusively for
// tests (fault-injecting fake pg_dump scripts). QuotaBytes limits how much
// ciphertext one job may write (0 = only the filesystem free-space floor).
type Config struct {
	StagingDir     string
	BinDirOverride string // test hook
	QuotaBytes     int64  // staging hard budget for one job's ciphertext
	FreeFloor      int64  // minimum free filesystem bytes before starting
}

// Target describes one dump run.
type Target struct {
	Conn        *pgclient.ConnInfo
	ServerMajor int
	Recipient   string // age recipient (protocol B); required
}

// Result describes a committed artifact.
type Result struct {
	ArtifactPath  string
	SHA256        string // SHA-256 of the full CIPHERTEXT (protocol C.1)
	SizeBytes     int64
	DumpToolVer   string
	ClientMajor   int
	EncryptedTo   string // recipient fingerprint
	PlaintextArc  int64  // bytes of compressed archive streamed by pg_dump
	StdErrExcerpt string // sanitized, bounded stderr
}

// Typed errors.
var (
	ErrNoClient    = errors.New("no suitable pg_dump client")
	ErrDumpFailed  = errors.New("pg_dump failed")
	ErrEncrypt     = errors.New("age encryption failed")
	ErrStagingFull = errors.New("staging budget exceeded")
)

// Classified wraps a failure with one of the seven job error classes.
type Classified struct {
	Class pgclient.ErrClass
	Err   error
}

func (e *Classified) Error() string { return e.Err.Error() }
func (e *Classified) Unwrap() error { return e.Err }

var pgDumpVersionRe = regexp.MustCompile(`pg_dump \(PostgreSQL\) (\d+)`)

// FindClient returns the pg_dump whose major version best matches the
// server: same major preferred, otherwise the smallest client major that is
// still >= the server major (older clients can never dump newer servers).
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
			continue
		}
		if bestPath == "" || m < bestMajor {
			bestPath, bestMajor, bestVersion = cand, m, v
		}
	}
	if bestPath == "" {
		return "", 0, "", &Classified{
			Class: pgclient.ClassClientVer,
			Err:   fmt.Errorf("%w: no pg_dump with major >= %d available", ErrNoClient, serverMajor),
		}
	}
	return bestPath, bestMajor, bestVersion, nil
}

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
			Err: fmt.Errorf("%w: pg_dump not found in image or PATH", ErrNoClient)}
	}
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
		return 0, "", fmt.Errorf("unrecognized version output")
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, "", err
	}
	return n, v, nil
}

// sanitize masks credential-bearing patterns in stderr before storage or
// logging. The VALUE is removed, not just the key labeled (round-2 review
// P1-02: `password=[REDACTED]<value>` still leaks the value).
var (
	// A quoted conninfo password may itself contain backslash-escaped
	// quotes — the value regex must consume `\'` sequences, otherwise the
	// tail of the secret survives redaction (round-6 review P1-02).
	quotedPwRe = regexp.MustCompile(`(?i)(password=')(?:[^'\\]|\\.)*(')`)
	plainPwRe  = regexp.MustCompile(`(?i)(password=)(?:[^\s'";,\\]|\\.)*`)
	uriRe      = regexp.MustCompile(`(?i)(postgres(?:ql)?://)[^\s'";,]*`)
)

// sanitize masks credential-bearing patterns in stderr: the VALUE is removed
// for both bare and quoted forms (round-2 review P1-02 remainder).
func sanitize(s string) string {
	s = quotedPwRe.ReplaceAllString(s, "$1[REDACTED]$2")
	s = plainPwRe.ReplaceAllString(s, "$1[REDACTED]")
	s = uriRe.ReplaceAllString(s, "$1[REDACTED]")
	return s
}

// RedactKnownSecrets removes every occurrence of the actual secret values
// from arbitrary error text. Each character of the secret may appear in the
// text with up to two levels of backslash escaping (raw, \' and \\' — the
// compositions conninfo quoting and JSON encoding actually produce), so the
// match pattern allows optional backslashes before every character. The
// whole span is removed in ONE regex pass; form-enumeration loops had
// termination edge cases (round-8 review R8-P1-01).
func RedactKnownSecrets(secrets []string, s string) string {
	for _, sec := range secrets {
		if sec == "" {
			continue
		}
		var pattern strings.Builder
		pattern.WriteString(`\\{0,2}`)
		for _, r := range sec {
			pattern.WriteString(`(?:\\{0,2}`)
			pattern.WriteString(regexp.QuoteMeta(string(r)))
			pattern.WriteString(`)`)
		}
		re, err := regexp.Compile(pattern.String())
		if err != nil {
			continue
		}
		s = re.ReplaceAllLiteralString(s, "[REDACTED]")
	}
	return s
}

// spaceCheck verifies free headroom and returns the CURRENT staging usage
// so the writer's budget can be the REMAINING allowance, not the full quota
// (round-5 review P1-13: 700 KiB existing + 717 KiB new passed a 1 MiB
// total budget when the writer received the full quota).
func (c *Config) spaceCheck() (used int64, err error) {
	if err := os.MkdirAll(c.StagingDir, 0o700); err != nil {
		return 0, err
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(c.StagingDir, &st); err != nil {
		return 0, err
	}
	avail := int64(st.Bavail) * int64(st.Bsize)
	floor := c.FreeFloor
	if floor <= 0 {
		floor = 64 << 20
	}
	if avail < floor {
		return 0, fmt.Errorf("%w: filesystem has %d bytes free, need %d", ErrStagingFull, avail, floor)
	}
	_ = filepath.WalkDir(c.StagingDir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			used += info.Size()
		}
		return nil
	})
	if c.QuotaBytes > 0 && used >= c.QuotaBytes {
		return used, fmt.Errorf("%w: staging holds %d of %d budgeted bytes", ErrStagingFull, used, c.QuotaBytes)
	}
	return used, nil
}

// Run executes one dump+encrypt+commit chain per protocol A.
func (c *Config) Run(ctx context.Context, jobID int64, t Target) (res *Result, err error) {
	if t.Recipient == "" {
		return nil, &Classified{Class: pgclient.ClassUnknown,
			Err: errors.New("age recipient not configured")}
	}
	bin, clientMajor, toolVersion, err := c.FindClient(t.ServerMajor)
	if err != nil {
		return nil, err
	}
	used, err := c.spaceCheck()
	if err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Err: err}
	}
	writerBudget := c.QuotaBytes // 0 = unlimited
	if c.QuotaBytes > 0 {
		writerBudget = c.QuotaBytes - used
		if writerBudget <= 0 {
			return nil, &Classified{Class: pgclient.ClassDisk,
				Err: fmt.Errorf("%w: staging usage %d already at budget %d", ErrStagingFull, used, c.QuotaBytes)}
		}
	}

	// Budget watchdog: if the staging usage blows past the quota mid-dump,
	// the context is canceled and the process group killed (round-2 P1-13).
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	if c.QuotaBytes > 0 {
		stopWatch := make(chan struct{})
		defer close(stopWatch)
		go func() {
			tick := time.NewTicker(500 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-runCtx.Done():
					return
				case <-stopWatch:
					return
				case <-tick.C:
					var used int64
					_ = filepath.WalkDir(c.StagingDir, func(_ string, d os.DirEntry, err error) error {
						if err != nil || d.IsDir() {
							return nil
						}
						if info, ierr := d.Info(); ierr == nil {
							used += info.Size()
						}
						return nil
					})
					if used > c.QuotaBytes {
						cancelRun()
						return
					}
				}
			}
		}()
	}

	// PGPASSFILE: dedicated 0700 dir, 0600 file, libpq-escaped, removed on
	// every exit path; control characters were rejected at URI parse time.
	passDir, err := os.MkdirTemp(c.StagingDir, "job-creds-")
	if err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Err: err}
	}
	committedCreds := false
	defer func() {
		if !committedCreds {
			if rerr := os.RemoveAll(passDir); rerr != nil {
				// A credential leftover is a security finding: surface it on
				// the result error path (round-2 review P2-03).
				err = errors.Join(err, fmt.Errorf("PGPASSFILE REMOVAL FAILED for %s: %w", passDir, rerr))
			}
		}
	}()
	if err := os.Chmod(passDir, 0o700); err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Err: err}
	}
	passFile := filepath.Join(passDir, "pgpass")
	line := fmt.Sprintf("%s:%s:%s:%s:%s",
		libpqField(t.Conn.Host), libpqField(t.Conn.Port), libpqField(t.Conn.DBName),
		libpqField(t.Conn.User), libpqField(t.Conn.Password))
	if err := os.WriteFile(passFile, []byte(line+"\n"), 0o600); err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Err: err}
	}

	// Prepare the temp artifact BEFORE creating process pipes: a failing
	// OpenFile must not leak exec's internal pipe FDs (round-2 review P2-04).
	tmpPath := filepath.Join(c.StagingDir, fmt.Sprintf("backup-job%d.dump.age.inprogress", jobID))
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Err: err}
	}
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmpPath)
		}
	}()

	// Child environment: strip every inherited PG* variable (protocol A; also
	// keeps pgx-side semantics controlled — the process itself is cleaned in
	// main.go before anything PG-related parses config).
	childEnv := filterEnv(os.Environ(), "PG")
	childEnv = append(childEnv, "PGPASSFILE="+passFile)

	args := []string{"--format=custom", "-d", t.Conn.DSN()}
	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Env = childEnv
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// ctx cancellation runs cmd.Cancel inside exec's watcher (process
	// guaranteed started) and WaitDelay bounds a surviving grandchild's hold
	// on the pipes.
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	dumpOut, err := cmd.StdoutPipe()
	if err != nil {
		return nil, &Classified{Class: pgclient.ClassUnknown, Err: err}
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		dumpOut.Close()
		return nil, &Classified{Class: pgclient.ClassUnknown, Err: err}
	}

	hasher := sha256.New()
	counter := &countingWriter{w: tmp, quota: writerBudget}
	encTarget := io.MultiWriter(counter, hasher)

	if err := cmd.Start(); err != nil {
		return nil, &Classified{Class: pgclient.ClassClientVer, Err: err}
	}

	// Supervised consumers. Every path below waits for BOTH to reach EOF —
	// pg_dump is never left blocked on a full pipe (round-2 P1-01).
	var wg sync.WaitGroup
	var encErr, stderrErr error
	stderrData := make([]byte, 0, stderrKeep)
	stderrMu := sync.Mutex{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		encErr = agekey.EncryptStream(t.Recipient, dumpOut, encTarget)
		if encErr != nil {
			// Consumer failure: make sure no process-group member keeps the
			// pipe open past this point.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return
		}
		// Hard quota enforcement at the writer (round-2 review P1-13): the
		// periodic scanner is too slow for a fast stream, so the counter
		// itself cancels the run the moment the budget is breached.
		if c.QuotaBytes > 0 && counter.n > c.QuotaBytes {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			cancelRun()
		}
		// On SUCCESS: no kill. pg_dump closed its stdout and is finishing
		// its shutdown; killing here would turn a clean dump into
		// "signal: killed" (found by the M1 gate).
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Drain to EOF ALWAYS; keep only the first stderrKeep bytes.
		buf := make([]byte, 32<<10)
		total := 0
		for {
			n, rerr := stderrPipe.Read(buf)
			stderrMu.Lock()
			if total < stderrKeep {
				room := stderrKeep - total
				if n > room {
					n = room
				}
				stderrData = append(stderrData, buf[:n]...)
				total += n
			}
			stderrMu.Unlock()
			if rerr != nil {
				if rerr != io.EOF {
					stderrErr = rerr
				}
				return
			}
		}
	}()

	wg.Wait()
	// Wait with a bounded grace period: pg_dump normally exits right after
	// closing its stdout, but a wedged process must not pin the worker.
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-waitDone:
	case <-time.After(waitAfterKill):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case waitErr = <-waitDone:
		case <-time.After(5 * time.Second):
			waitErr = errors.New("pg_dump process group did not terminate after SIGKILL")
		}
	}

	if ctx.Err() != nil {
		// Outer cancellation: either the user or instance shutdown.
		return nil, &Classified{Class: pgclient.ClassUnknown, Err: ctx.Err()}
	}
	if runCtx.Err() != nil {
		return nil, &Classified{Class: pgclient.ClassDisk,
			Err: fmt.Errorf("%w (staging budget exceeded mid-dump)", ErrStagingFull)}
	}
	stderrMu.Lock()
	excerpt := sanitize(string(stderrData))
	stderrMu.Unlock()
	if stderrErr != nil {
		return nil, &Classified{Class: pgclient.ClassUnknown,
			Err: fmt.Errorf("read pg_dump stderr: %w", stderrErr)}
	}
	if encErr != nil {
		// Consumer failure: ensure the producer is gone, then fail without a
		// committed artifact.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
		if errors.Is(encErr, errQuotaExceeded) {
			return nil, &Classified{Class: pgclient.ClassDisk,
				Err: fmt.Errorf("%w: ciphertext exceeded %d bytes mid-stream", ErrStagingFull, c.QuotaBytes)}
		}
		return nil, &Classified{Class: pgclient.ClassUnknown,
			Err: fmt.Errorf("%w: %v", ErrEncrypt, encErr)}
	}
	if waitErr != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return nil, &Classified{Class: classifyDumpFailure(excerpt),
			Err: fmt.Errorf("%w: %v", ErrDumpFailed, waitErr)}
	}

	if err := tmp.Sync(); err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Err: err}
	}
	if err := tmp.Close(); err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Err: err}
	}

	// Atomic commit + directory fsync so the rename survives power loss
	// (round-2 review P1-11).
	finalPath := strings.TrimSuffix(tmpPath, ".inprogress")
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Err: err}
	}
	if err := syncDir(c.StagingDir); err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Err: err}
	}
	committed = true

	st, err := os.Stat(finalPath)
	if err != nil {
		return nil, &Classified{Class: pgclient.ClassDisk, Err: err}
	}
	return &Result{
		ArtifactPath:  finalPath,
		SHA256:        hex.EncodeToString(hasher.Sum(nil)),
		SizeBytes:     st.Size(),
		DumpToolVer:   toolVersion,
		ClientMajor:   clientMajor,
		EncryptedTo:   agekey.Fingerprint(t.Recipient),
		PlaintextArc:  counter.n,
		StdErrExcerpt: excerpt,
	}, nil
}

// KillGroup force-terminates a dump process group (cancel path).
func KillGroup(p *os.Process) {
	if p != nil {
		_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
	}
}

// quotaBreached is set when the writer observes the budget exceeded; the
// consumer goroutine turns it into an immediate cancel+kill (round-2 review
// P1-13 remainder: the 500 ms scanner let 8 MiB through a 1 MiB budget).
var errQuotaExceeded = errors.New("staging quota exceeded mid-stream")

type countingWriter struct {
	w        io.Writer
	n        int64
	quota    int64
	breached bool
}

func (c *countingWriter) Write(p []byte) (int, error) {
	// Pre-write check: a single oversized Write must not land on disk before
	// the budget is enforced (round-4 review P1-13 remainder).
	if c.quota > 0 && c.n+int64(len(p)) > c.quota {
		c.breached = true
		return 0, errQuotaExceeded
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	if c.quota > 0 && c.n > c.quota {
		c.breached = true
		return n, errQuotaExceeded
	}
	return n, err
}

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
	case strings.Contains(m, "no space left on device"):
		return pgclient.ClassDisk
	default:
		return pgclient.ClassUnknown
	}
}

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

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}
