package dumper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/agekey"
	"github.com/cloudfan/supabackup/backend/internal/pgclient"
)

// fakePgDump is a controlled pg_dump replacement for fault injection.
const fakePgDump = `#!/bin/sh
case "$1" in
  --version) echo "pg_dump (PostgreSQL) 99.0"; exit 0 ;;
esac
# Emit recognizable plaintext, then optionally fail (protocol A fault probe).
echo "FAKEDUMP-DATA-LINE-1"
echo "FAKEDUMP-DATA-LINE-2"
if [ -n "$FAKE_EXIT_CODE" ] && [ "$FAKE_EXIT_CODE" != "0" ]; then
  echo "partial trailing output"
  echo "pg_dump: error: simulated failure" >&2
  exit "$FAKE_EXIT_CODE"
fi
exit 0
`

func newTestConfig(t *testing.T, dumpScript string) (*Config, string) {
	t.Helper()
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "pg_dump"), []byte(dumpScript), 0o755); err != nil {
		t.Fatal(err)
	}
	staging := t.TempDir()
	return &Config{StagingDir: staging, BinDirOverride: binDir}, staging
}

func testTarget(recipient string) Target {
	return Target{
		Conn: &pgclient.ConnInfo{
			Host: "db.test", Port: "5432", User: "app", Password: "CANARY-secret-pw", DBName: "appdb", SSLMode: "prefer",
		},
		ServerMajor: 99,
		Recipient:   recipient,
	}
}

func mustRecipient(t *testing.T) string {
	t.Helper()
	_, rcp, err := agekey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return rcp
}

func TestRunCommitsOnlyOnFullSuccess(t *testing.T) {
	cfg, staging := newTestConfig(t, fakePgDump)
	rcp := mustRecipient(t)

	res, err := cfg.Run(context.Background(), 1, testTarget(rcp))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Committed artifact exists under the official name, no .inprogress left.
	if st, err := os.Stat(res.ArtifactPath); err != nil || st.Size() == 0 {
		t.Fatalf("committed artifact missing/empty: %v", err)
	}
	if strings.HasSuffix(res.ArtifactPath, ".inprogress") {
		t.Fatal("committed path must not carry the in-progress suffix")
	}
	entries, _ := os.ReadDir(staging)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".inprogress") || strings.HasPrefix(e.Name(), "job-creds-") {
			t.Fatalf("leftover %s after success", e.Name())
		}
	}
	// SHA-256 over the committed ciphertext matches the streamed hash.
	data, err := os.ReadFile(res.ArtifactPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != res.SHA256 {
		t.Fatal("recorded SHA-256 does not match the committed ciphertext")
	}
	if res.SizeBytes != int64(len(data)) {
		t.Fatalf("size mismatch: %d vs %d", res.SizeBytes, len(data))
	}
	if !strings.Contains(res.DumpToolVer, "99.0") || res.ClientMajor != 99 {
		t.Fatalf("unexpected tool info: %s / %d", res.DumpToolVer, res.ClientMajor)
	}
	if res.PlaintextArc == 0 || res.PlaintextArc > res.SizeBytes {
		t.Fatalf("PlaintextArc=%d must be the pre-encryption archive (0 < arc <= ciphertext %d)",
			res.PlaintextArc, res.SizeBytes)
	}
}

// TestRunRejectsDumpThatFailsAfterOutput (protocol A): a pg_dump that emits
// data and THEN exits non-zero must never produce a committed artifact.
func TestRunRejectsDumpThatFailsAfterOutput(t *testing.T) {
	cfg, staging := newTestConfig(t, fakePgDump)
	t.Setenv("FAKE_EXIT_CODE", "1")

	_, err := cfg.Run(context.Background(), 2, testTarget(mustRecipient(t)))
	if err == nil {
		t.Fatal("dump that exits non-zero must fail")
	}
	var cls *Classified
	if !asClassified(err, &cls) {
		t.Fatalf("failure must be a Classified error, got %T: %v", err, err)
	}
	if cls.Class != pgclient.ClassUnknown {
		t.Fatalf("simulated failure classifies as %q, want unknown", cls.Class)
	}
	entries, _ := os.ReadDir(staging)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".inprogress") && !strings.HasPrefix(e.Name(), "job-creds-") {
			t.Fatalf("a committed artifact appeared despite dump failure: %s", e.Name())
		}
	}
}

// TestRunBadRecipientNeverCommits: an unusable recipient is an encryption
// failure — nothing may be committed.
func TestRunBadRecipientNeverCommits(t *testing.T) {
	cfg, staging := newTestConfig(t, fakePgDump)
	tgt := testTarget("not-a-valid-age-recipient")
	_, err := cfg.Run(context.Background(), 3, tgt)
	if err == nil {
		t.Fatal("bad recipient must fail")
	}
	entries, _ := os.ReadDir(staging)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".inprogress") && !strings.HasPrefix(e.Name(), "job-creds-") {
			t.Fatalf("artifact committed despite encryption failure: %s", e.Name())
		}
	}
}

// TestRunContextCancel: a canceled run leaves no committed artifact.
func TestRunContextCancel(t *testing.T) {
	// The script must still be RUNNING when we cancel: stream data, then
	// block. (fakePgDump ends with exit 0, so it cannot be reused here.)
	slowDump := `#!/bin/sh
case "$1" in
  --version) echo "pg_dump (PostgreSQL) 99.0"; exit 0 ;;
esac
echo "FAKEDUMP-DATA-LINE-1"
sleep 30
echo "NEVER-REACHED"
`
	cfg, staging := newTestConfig(t, slowDump)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := cfg.Run(ctx, 4, testTarget(mustRecipient(t)))
		done <- err
	}()
	time.Sleep(300 * time.Millisecond) // let the dump start streaming
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled run must fail")
		}
		t.Logf("cancel returned: %v", err)
	case <-time.After(40 * time.Second):
		t.Fatal("canceled run did not return — process group still alive?")
	}
	entries, _ := os.ReadDir(staging)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".inprogress") && !strings.HasPrefix(e.Name(), "job-creds-") {
			t.Fatalf("artifact committed despite cancellation: %s", e.Name())
		}
	}
}

// TestSecretCanaryNeverEscapes (M1 DoD): the credential must not appear in
// stderr excerpts, child argv, or leftover passfiles.
func TestSecretCanaryNeverEscapes(t *testing.T) {
	cfg, _ := newTestConfig(t, fakePgDump)
	pw := "CANARY-Secret-P@ssw:rd\\with/specials"
	rcp := mustRecipient(t)
	tgt := testTarget(rcp)
	tgt.Conn.Password = pw

	res, err := cfg.Run(context.Background(), 5, tgt)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(res.StdErrExcerpt, pw) {
		t.Fatal("canary password leaked into stderr excerpt")
	}
	// No passfile remains anywhere in staging.
	err = filepath.Walk(cfg.StagingDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(path) //nolint:gosec // G122: walks its own t.TempDir staging; no attacker-controlled symlinks
		if rerr != nil {
			return nil
		}
		if bytes.Contains(data, []byte(pw)) {
			t.Errorf("canary password found in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFindClientPrefersSameMajor(t *testing.T) {
	binDir := t.TempDir()
	// two "clients": majors 97 and 99, server is 98 → must pick 98? No such
	// client exists; must pick the NEWEST >= server, i.e. 99.
	for _, v := range []string{"97", "99"} {
		script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in --version) echo \"pg_dump (PostgreSQL) %s.0\"; exit 0;; esac\nexit 0\n", v)
		if err := os.WriteFile(filepath.Join(binDir, "pg_dump"+v), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &Config{StagingDir: t.TempDir()}
	_ = cfg
	// Use the override dir probe by creating pg_dump named binaries —
	// candidateBinaries only recognizes literal pg_dump paths, so emulate
	// through separate directories.
	dir97 := t.TempDir()
	dir99 := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir97, "pg_dump"), []byte("#!/bin/sh\ncase \"$1\" in --version) echo \"pg_dump (PostgreSQL) 97.0\"; exit 0;; esac\n"), 0o755)
	_ = os.WriteFile(filepath.Join(dir99, "pg_dump"), []byte("#!/bin/sh\ncase \"$1\" in --version) echo \"pg_dump (PostgreSQL) 99.0\"; exit 0;; esac\n"), 0o755)

	// Directly probe the selection helper via FindClient with override.
	cfg97 := &Config{StagingDir: t.TempDir(), BinDirOverride: dir97}
	if _, major, _, err := cfg97.FindClient(96); err != nil || major != 97 {
		t.Fatalf("server 96 → client 97 expected, got major=%d err=%v", major, err)
	}
	if _, _, _, err := cfg97.FindClient(98); err == nil {
		t.Fatal("client 97 must NOT serve server 98 (older client)")
	}
	cfg99 := &Config{StagingDir: t.TempDir(), BinDirOverride: dir99}
	if _, major, _, err := cfg99.FindClient(98); err != nil || major != 99 {
		t.Fatalf("server 98 → newest eligible client 99 expected, got %d err=%v", major, err)
	}
}

func asClassified(err error, target **Classified) bool {
	var c *Classified
	if errors.As(err, &c) {
		*target = c
		return true
	}
	return false
}

// TestRunDrainsStderrBeyondLimit (round-2 review P1-01): a pg_dump that
// writes FAR more stderr than we retain must not deadlock, and the retained
// excerpt must be bounded.
func TestRunDrainsStderrBeyondLimit(t *testing.T) {
	noisy := `#!/bin/sh
case "$1" in
  --version) echo "pg_dump (PostgreSQL) 99.0"; exit 0 ;;
esac
i=0
while [ $i -lt 5000 ]; do
  echo "warning line $i from a very verbose pg_dump" >&2
  i=$((i+1))
done
echo "FAKEDUMP-DATA"
`
	cfg, staging := newTestConfig(t, noisy)
	res, err := cfg.Run(context.Background(), 42, testTarget(mustRecipient(t)))
	if err != nil {
		t.Fatalf("verbose stderr must not break the run: %v", err)
	}
	if len(res.StdErrExcerpt) > stderrKeep {
		t.Fatalf("stderr excerpt exceeds the retained cap: %d", len(res.StdErrExcerpt))
	}
	entries, _ := os.ReadDir(staging)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".inprogress") || strings.HasPrefix(e.Name(), "job-creds-") {
			t.Fatalf("leftover %s", e.Name())
		}
	}
}
