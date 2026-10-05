package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLoadOrCreateSecretAtomicCreation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.key")

	// Concurrent first starts must converge on exactly one key (review P1-04).
	const n = 16
	keys := make([][]byte, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k, err := LoadOrCreateSecret(path)
			if err != nil {
				t.Errorf("worker %d: %v", i, err)
				return
			}
			keys[i] = k
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if string(keys[i]) != string(keys[0]) {
			t.Fatalf("worker %d got a different key than worker 0", i)
		}
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("secret file must be 0600, got %04o", perm)
	}
}

func TestLoadOrCreateSecretRejectsWeakPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.key")
	if err := os.WriteFile(path, []byte(repeat("ab", 32)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateSecret(path); err == nil {
		t.Fatal("a group/other readable secret must be rejected")
	}
}

func TestLoadOrCreateSecretRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere.key")
	if err := os.WriteFile(target, []byte(repeat("ab", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "secret.key")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateSecret(link); err == nil {
		t.Fatal("symlinked secret path must be rejected")
	}
}

func TestLoadOrCreateSecretRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.key")
	k1, err := LoadOrCreateSecret(path)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoadOrCreateSecret(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(k1) != string(k2) || len(k1) != 32 {
		t.Fatal("existing secret must be returned unchanged as 32 bytes")
	}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for range n {
		out = append(out, s...)
	}
	return string(out)
}

// TestVerifyEnablementGate: SB_VERIFY_ENABLED without the identity file is a
// configuration ERROR (never a silent no-op); a symlinked or permissive
// identity file is refused (ADR-004 enablement gate).
func TestVerifyEnablementGate(t *testing.T) {
	setEnv := func(t *testing.T, k, v string) {
		old, had := os.LookupEnv(k)
		if v == "" {
			_ = os.Unsetenv(k)
		} else {
			_ = os.Setenv(k, v)
		}
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(k, old)
			} else {
				_ = os.Unsetenv(k)
			}
		})
	}

	// Enabled without identity → error.
	setEnv(t, "SB_VERIFY_ENABLED", "1")
	setEnv(t, "SB_VERIFY_IDENTITY_FILE", "")
	if _, err := Load(); err == nil || !containsStr(err.Error(), "SB_VERIFY_IDENTITY_FILE") {
		t.Fatalf("enabled without identity must fail naming the variable, got %v", err)
	}

	// Identity missing on disk → error.
	setEnv(t, "SB_VERIFY_IDENTITY_FILE", "/nonexistent/identity.txt")
	if _, err := Load(); err == nil {
		t.Fatal("missing identity file must fail")
	}

	// Permissive identity mode → error.
	dir := t.TempDir()
	loose := filepath.Join(dir, "identity.txt")
	if err := os.WriteFile(loose, []byte("AGE-SECRET-KEY-1"), 0o644); err != nil {
		t.Fatal(err)
	}
	setEnv(t, "SB_VERIFY_IDENTITY_FILE", loose)
	if _, err := Load(); err == nil || !containsStr(err.Error(), "0600") {
		t.Fatalf("permissive identity mode must fail, got %v", err)
	}

	// Symlinked identity → error.
	tight := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(tight, []byte("AGE-SECRET-KEY-1"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(tight, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	setEnv(t, "SB_VERIFY_IDENTITY_FILE", link)
	if _, err := Load(); err == nil {
		t.Fatal("symlinked identity must fail")
	}

	// Proper file → OK, and disabled by default.
	setEnv(t, "SB_VERIFY_IDENTITY_FILE", tight)
	c, err := Load()
	if err != nil {
		t.Fatalf("valid verification config rejected: %v", err)
	}
	if !c.VerifyEnabled {
		t.Fatal("VerifyEnabled lost")
	}
	setEnv(t, "SB_VERIFY_ENABLED", "")
	c, err = Load()
	if err != nil {
		t.Fatalf("default load failed: %v", err)
	}
	if c.VerifyEnabled {
		t.Fatalf("verification must default off: %+v", c)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
