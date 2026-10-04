package verifier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/agekey"
)

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func TestBoundedBuffer(t *testing.T) {
	bb := newBoundedBuffer(16)
	if n, err := bb.Write([]byte("0123456789ABCDEF")); err != nil || n != 16 {
		t.Fatalf("write: %d %v", n, err)
	}
	if bb.truncated {
		t.Fatal("premature truncation flag")
	}
	// Extra writes are consumed (never block the pipe) but flagged.
	if n, err := bb.Write([]byte("more")); err != nil || n != 4 {
		t.Fatalf("overflow write: %d %v", n, err)
	}
	if !bb.truncated {
		t.Fatal("truncation not flagged")
	}
	s := bb.String()
	if len(s) != 16+14+len("") { // 16 bytes + "…[truncated]" suffix (14 bytes)
		t.Fatalf("bounded string wrong length: %d (%q)", len(s), s)
	}
	if !strings.HasSuffix(s, "…[truncated]") {
		t.Fatalf("missing truncation marker: %q", s)
	}
}

func TestMinimalEnvWhitelist(t *testing.T) {
	t.Setenv("PGPASSWORD", "canary-secret")
	t.Setenv("PGOPTIONS", "-c canary=1")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "canary-aws")
	t.Setenv("LD_PRELOAD", "/tmp/evil.so")
	env := minimalEnv("/tmp/home")
	joined := strings.Join(env, "\n")
	for _, banned := range []string{"PGPASSWORD", "PGOPTIONS", "AWS_SECRET_ACCESS_KEY", "LD_PRELOAD"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("environment whitelisted banned variable %s: %v", banned, env)
		}
	}
	for _, want := range []string{"LC_ALL=C", "LANG=C", "TZ=UTC", "HOME=/tmp/home"} {
		found := false
		for _, kv := range env {
			if kv == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("env missing %q: %v", want, env)
		}
	}
}

func TestDecryptRoundTripAndHashGate(t *testing.T) {
	identity, recipient, err := agekey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ciphertext := filepath.Join(dir, "x.dump.age")
	plaintext := filepath.Join(dir, "plain.dump")
	src := filepath.Join(dir, "src.dump")
	if err := os.WriteFile(src, []byte("FAKE CUSTOM FORMAT DUMP BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Encrypt via the agekey package (same library the verifier uses).
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(ciphertext, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := agekey.EncryptStream(recipient, in, out); err != nil {
		t.Fatal(err)
	}
	in.Close()
	out.Close()

	// The verifier hashes the CIPHERTEXT (the committed bytes), so compute
	// the digest from the encrypted file, not the plaintext.
	sum, err := fileSHA256(ciphertext)
	if err != nil {
		t.Fatal(err)
	}

	// Hash gate accepts the right digest and decrypts to a 0600 file.
	if err := hashFile(context.Background(), ciphertext, sum); err != nil {
		t.Fatalf("hashFile accepted-want failed: %v", err)
	}
	if err := hashFile(context.Background(), ciphertext, strings.Repeat("0", 64)); err == nil {
		t.Fatal("hashFile accepted a wrong digest")
	}
	if err := decryptToFile(context.Background(), identity, ciphertext, plaintext); err != nil {
		t.Fatalf("decryptToFile: %v", err)
	}
	st, err := os.Stat(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("plaintext mode %04o, want 0600", st.Mode().Perm())
	}
	got, err := os.ReadFile(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "FAKE CUSTOM FORMAT DUMP BYTES" {
		t.Fatalf("round trip mismatch: %q", got)
	}
	// A wrong identity must fail and remove the partial plaintext.
	os.Remove(plaintext)
	if err := decryptToFile(context.Background(), "AGE-SECRET-KEY-1AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ciphertext, plaintext); err == nil {
		t.Fatal("decrypt with a wrong identity succeeded")
	}
	if _, err := os.Stat(plaintext); !os.IsNotExist(err) {
		t.Fatal("failed decryption left a plaintext file behind")
	}
}

func TestCleanupResidual(t *testing.T) {
	base := t.TempDir()
	// New() only requires the PG binaries to EXIST; dummy files suffice.
	pgBin := t.TempDir()
	for _, bin := range []string{"initdb", "pg_ctl", "postgres", "pg_restore", "psql"} {
		if err := os.WriteFile(filepath.Join(pgBin, bin), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	v, err := New(Config{PGBin: pgBin, BaseDir: base})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"verify-111", "verify-222"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "verify-111", "plaintext.dump"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "keepme"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := v.CleanupResidual(); err != nil {
		t.Fatalf("CleanupResidual: %v", err)
	}
	for _, d := range []string{"verify-111", "verify-222"} {
		if _, serr := os.Stat(filepath.Join(base, d)); !os.IsNotExist(serr) {
			t.Fatalf("residual %s survived cleanup", d)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "keepme")); err != nil {
		t.Fatal("unrelated directory was removed")
	}
}

func TestProfileName(t *testing.T) {
	cases := map[string]string{
		"PostgreSQL 18.4 (Debian 18.4-1.pgdg120+1) on aarch64": "embedded-local:18.4",
		"":           "embedded-local:unknown",
		"PostgreSQL": "embedded-local:PostgreSQL",
	}
	for in, want := range cases {
		if got := ProfileName(in); got != want {
			t.Errorf("profileName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestVerifyRefusesEmptyIdentity pins the P1-05 contract: verification
// never silently treats ciphertext as plaintext or skips decryption.
func TestVerifyRefusesEmptyIdentity(t *testing.T) {
	v := &Verifier{pgBin: t.TempDir(), baseDir: t.TempDir()}
	res := v.Verify(context.Background(), Input{
		CiphertextPath: "/nonexistent",
		SHA256Hex:      "x",
		Timeout:        time.Second,
	})
	if res.Pass() || res.Status == StatusVerified {
		t.Fatalf("empty identity must never verify: %+v", res)
	}
	if !strings.Contains(res.Detail, "identity") {
		t.Fatalf("detail must name the identity problem: %q", res.Detail)
	}
}

func TestVerifyRefusesBadHashBeforeDecrypt(t *testing.T) {
	dir := t.TempDir()
	identity, _, err := agekey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.age")
	if err := os.WriteFile(bad, []byte("not really age"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := &Verifier{pgBin: dir, baseDir: dir}
	res := v.Verify(context.Background(), Input{
		CiphertextPath: bad,
		SHA256Hex:      strings.Repeat("a", 64), // wrong on purpose
		Identity:       identity,
		Timeout:        time.Second,
	})
	if res.Status == StatusVerified {
		t.Fatal("wrong hash must not verify")
	}
	if !strings.Contains(res.Detail, "ciphertext check") {
		t.Fatalf("expected the hash gate to stop it: %q", res.Detail)
	}
}

// stubPG installs fake PG server binaries that emulate the lifecycle
// WITHOUT real processes: initdb lays down a postmaster.pid (the marker
// stopConfirmed checks), pg_ctl logs and either removes or keeps it, and
// pg_restore/psql are configurable. These tests pin the round-2 R2-P1-01
// contract: the instance is stopped on EVERY exit path after a successful
// start, and a failed stop PRESERVES the workdir as evidence.
type pgStub struct {
	binDir      string
	baseDir     string
	stopLog     string
	keepPIDMark string // when this file exists, pg_ctl stop FAILS (pid kept)
}

func newPGStub(t *testing.T, restoreExit string) *pgStub {
	s := &pgStub{
		binDir:      t.TempDir(),
		baseDir:     t.TempDir(),
		stopLog:     filepath.Join(t.TempDir(), "pgctl.log"),
		keepPIDMark: filepath.Join(t.TempDir(), ".keep-pid"),
	}
	stub := func(name, body string) {
		if err := os.WriteFile(filepath.Join(s.binDir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	log := fmt.Sprintf(`printf 'SUB:%%s\n' "$*" >> '%s'`, s.stopLog)
	stub("initdb", `#!/bin/sh
dir=""
prev=""
for a in "$@"; do [ "$prev" = "-D" ] && dir="$a"; prev="$a"; done
mkdir -p "$dir"
touch "$dir/postmaster.pid"
exit 0
`)
	// pg_ctl may be called with options before the subcommand, so scan the
	// args for start/stop instead of testing $1.
	stub("pg_ctl", "#!/bin/sh\n"+log+"\n"+
		"is_start=0; is_stop=0\n"+
		"for a in \"$@\"; do [ \"$a\" = start ] && is_start=1; [ \"$a\" = stop ] && is_stop=1; done\n"+
		"dir=\"\"; prev=\"\"\n"+
		"for a in \"$@\"; do [ \"$prev\" = -D ] && dir=\"$a\"; prev=\"$a\"; done\n"+
		"if [ \"$is_stop\" = 1 ]; then\n"+
		"  if [ -f \""+s.keepPIDMark+"\" ]; then exit 1; fi\n"+
		"  rm -f \"$dir/postmaster.pid\"\n"+
		"  exit 0\n"+
		"fi\n"+
		"exit 0\n")
	stub("postgres", "#!/bin/sh\nexit 0\n")
	stub("pg_restore", "#!/bin/sh\nexit "+restoreExit+"\n")
	stub("psql", "#!/bin/sh\necho 3\nexit 0\n")
	return s
}

func (s *pgStub) verifier(t *testing.T) *Verifier {
	t.Helper()
	v, err := New(Config{PGBin: s.binDir, BaseDir: s.baseDir})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (s *pgStub) stopCalls() int {
	b, err := os.ReadFile(s.stopLog)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, " stop") {
			n++
		}
	}
	return n
}

// verifyInput builds a well-formed Input over a real age-encrypted artifact
// so decryption succeeds and the run reaches the PG phases.
func verifyInput(t *testing.T, v *Verifier) Input {
	t.Helper()
	identity, recipient, err := agekey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.dump")
	if err := os.WriteFile(src, []byte("DUMP BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	ct := filepath.Join(dir, "x.dump.age")
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(ct, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := agekey.EncryptStream(recipient, in, out); err != nil {
		t.Fatal(err)
	}
	in.Close()
	out.Close()
	sum, err := fileSHA256(ct)
	if err != nil {
		t.Fatal(err)
	}
	return Input{
		CiphertextPath: ct,
		SHA256Hex:      sum,
		Identity:       identity,
		Timeout:        10 * time.Second,
	}
}

// TestStopOnRestoreFailurePath: a pg_restore failure AFTER a successful
// start must still stop the throwaway instance (round-2 R2-P1-01).
func TestStopOnRestoreFailurePath(t *testing.T) {
	s := newPGStub(t, "1") // pg_restore fails
	v := s.verifier(t)
	res := v.Verify(context.Background(), verifyInput(t, v))
	if res.Status != StatusFailed {
		t.Fatalf("status = %q (%s), want failed", res.Status, res.Detail)
	}
	if s.stopCalls() == 0 {
		t.Fatal("pg_ctl stop was never invoked on the failure path (R2-P1-01 regression)")
	}
	left, _ := os.ReadDir(s.baseDir)
	if len(left) != 0 {
		t.Fatalf("workdir left behind despite confirmed stop: %d entries", len(left))
	}
}

// TestStopOnUnsupportedPath: the unsupported classification must also stop
// the instance.
func TestStopOnUnsupportedPath(t *testing.T) {
	s := newPGStub(t, "0")
	if err := os.WriteFile(filepath.Join(s.binDir, "psql"), []byte("#!/bin/sh\necho plpgsql\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	v := s.verifier(t)
	in := verifyInput(t, v)
	in.ExpectedExtensions = []string{"pgcrypto"}
	res := v.Verify(context.Background(), in)
	if res.Status != StatusUnsupported {
		t.Fatalf("status = %q (%s), want unsupported", res.Status, res.Detail)
	}
	if s.stopCalls() == 0 {
		t.Fatal("pg_ctl stop was never invoked on the unsupported path")
	}
}

// TestFailedStopPreservesEvidence: when the instance cannot be confirmed
// stopped, the result is downgraded and the workdir is PRESERVED (never
// deleted under a possibly-running postgres).
func TestFailedStopPreservesEvidence(t *testing.T) {
	s := newPGStub(t, "0")
	v := s.verifier(t)
	// The marker file makes the stub's stop refuse (pid stays).
	if err := os.WriteFile(s.keepPIDMark, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := v.Verify(context.Background(), verifyInput(t, v))
	if res.Status == StatusVerified {
		t.Fatal("an unconfirmable stop must not yield a clean verified")
	}
	if !strings.Contains(res.Detail, "PRESERVED") {
		t.Fatalf("detail must record the preserved workdir: %q", res.Detail)
	}
	left, _ := os.ReadDir(s.baseDir)
	if len(left) == 0 {
		t.Fatal("workdir was deleted although the stop could not be confirmed")
	}
	// Cleanup for the test environment: allow stops and sweep.
	os.Remove(s.keepPIDMark)
	if err := v.CleanupResidual(); err != nil {
		t.Fatalf("CleanupResidual after unmarking: %v", err)
	}
	left, _ = os.ReadDir(s.baseDir)
	if len(left) != 0 {
		t.Fatalf("CleanupResidual did not reclaim the preserved workdir: %d entries", len(left))
	}
}

// TestCleanupResidualStopsLivePID: startup recovery stops a residual
// instance before deleting its directory (round-2 P1-06 remainder).
func TestCleanupResidualStopsLivePID(t *testing.T) {
	s := newPGStub(t, "0")
	v := s.verifier(t)
	residual := filepath.Join(s.baseDir, "verify-999")
	if err := os.MkdirAll(filepath.Join(residual, "pgdata"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(residual, "pgdata", "postmaster.pid"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.CleanupResidual(); err != nil {
		t.Fatalf("CleanupResidual: %v", err)
	}
	if s.stopCalls() == 0 {
		t.Fatal("residual live pid was not stopped before deletion")
	}
	if _, err := os.Stat(residual); !os.IsNotExist(err) {
		t.Fatal("residual dir survived cleanup")
	}
}
