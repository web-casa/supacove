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
	for i := 0; i < n; i++ {
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
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
