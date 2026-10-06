package staging

import (
	"os"
	"path/filepath"
	"testing"
)

// verify-fetch leftovers (a SIGKILL during remote verification) must be
// reclaimed by the startup sweep like every other orphan; committed
// artifacts must survive it (overall review P1-K1 remainder).
func TestOrphanCleanupStartupCoversVerifyFetch(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(s.Dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("verify-fetch-job7.dump.age", "fetched")
	write("verify-fetch-job7.manifest.json", "{}")
	write("backup-job7.dump.age", "committed")
	write("backup-job7.dump.age.manifest.json", "{}")
	write("job-creds-9", "creds")
	write("backup-job8.dump.age.inprogress", "partial")

	removed, err := s.OrphanCleanupStartup()
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	gone := map[string]bool{}
	for _, name := range removed {
		gone[name] = true
	}
	for _, name := range []string{
		"verify-fetch-job7.dump.age", "verify-fetch-job7.manifest.json",
		"job-creds-9", "backup-job8.dump.age.inprogress",
	} {
		if !gone[name] {
			t.Errorf("%s was not removed", name)
		}
		if _, err := os.Stat(filepath.Join(s.Dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still on disk", name)
		}
	}
	for _, name := range []string{"backup-job7.dump.age", "backup-job7.dump.age.manifest.json"} {
		if gone[name] {
			t.Errorf("%s is a committed artifact and must survive the sweep", name)
		}
		if _, err := os.Stat(filepath.Join(s.Dir, name)); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}
}
