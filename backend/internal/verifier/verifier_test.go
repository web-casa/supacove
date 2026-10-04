package verifier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	if err := hashFile(ciphertext, sum); err != nil {
		t.Fatalf("hashFile accepted-want failed: %v", err)
	}
	if err := hashFile(ciphertext, strings.Repeat("0", 64)); err == nil {
		t.Fatal("hashFile accepted a wrong digest")
	}
	if err := decryptToFile(identity, ciphertext, plaintext); err != nil {
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
	if err := decryptToFile("AGE-SECRET-KEY-1AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ciphertext, plaintext); err == nil {
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
		if got := profileName(in); got != want {
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
