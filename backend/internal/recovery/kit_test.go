package recovery

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	platformpkg "github.com/cloudfan/supabackup/backend/internal/platform"
)

func testKit() KitInput {
	return KitInput{
		BackupUUID:       "job-42",
		Platform:         platformpkg.Generic,
		ArtifactFileName: "backup-job-42.dump.age",
		SHA256:           "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		KeyID:            "3f8a9c1d2b4e",
	}
}

// TestScriptShape pins the structural safety properties the phase-5 review
// found broken: no line continuations (P1-01), exclusive temp dir with
// umask 077, signal-safe cleanup, hash and emptiness gates, and no swallowed
// psql failures.
func TestScriptShape(t *testing.T) {
	script := GenerateRestoreScript(testKit())

	if !strings.HasPrefix(script, "#!/bin/sh") {
		t.Fatal("script must start with #!/bin/sh")
	}
	if strings.Contains(script, "\\\n") {
		t.Fatal("script contains a line continuation — P1-01 regression risk; use single-line commands")
	}
	for _, want := range []string{
		"umask 077",
		"mktemp -d",
		"trap cleanup EXIT",
		"trap 'exit 130' INT",
		"trap 'exit 143' TERM",
		"trap 'exit 129' HUP",
		"SHA-256 mismatch",
		"NOT empty",
		"--exit-on-error",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q", want)
		}
	}
	if strings.Contains(script, `|| echo "0"`) {
		t.Fatal("script swallows psql failures with || echo")
	}
	if out, err := exec.Command("bash", "-n", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("script is not valid shell syntax: %v: %s", err, out)
	}
}

func TestScriptMetadataSanitized(t *testing.T) {
	script := GenerateRestoreScript(KitInput{
		BackupUUID:       "job-1\n. /tmp/evil",
		Platform:         platformpkg.Generic,
		ArtifactFileName: "x$(rm -rf ~).dump.age",
		SHA256:           "abc`id`def",
		KeyID:            strings.Repeat("K", 300),
	})
	// The script's own cleanup legitimately contains `rm -rf "$WORK_DIR"`;
	// what must never appear is the HOSTILE metadata.
	for _, danger := range []string{"rm -rf ~", "$(rm", "`id`", ". /tmp"} {
		if strings.Contains(script, danger) {
			t.Fatalf("unsanitized metadata reached the script: %q", danger)
		}
	}
	if out, err := exec.Command("bash", "-n", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("script with hostile metadata is not valid shell: %v: %s", err, out)
	}
}

func TestScriptExpectedTablesEmbedded(t *testing.T) {
	with := GenerateRestoreScriptWithTables(testKit(), 7)
	if !strings.Contains(with, `"7"`) || !strings.Contains(with, "Expected user tables at dump time: 7") {
		t.Fatal("expected-table cross-check not embedded")
	}
	without := GenerateRestoreScriptWithTables(testKit(), -1)
	if strings.Contains(without, "Expected user tables") {
		t.Fatal("unknown table count must not fabricate an expectation")
	}
}

// harness runs the generated script against stub age/pg_restore/psql/
// sha256sum tools. Stubs append their argv to a log file so tests can assert
// the EXACT command lines the restore tools received (P1-01/P1-02 argv
// checks). The psql stub answers the FIRST invocation (target-emptiness
// check) with psqlFirst and every later one (post-restore count) with
// psqlNext, via an invocation counter.
type harness struct {
	dir     string // working dir where the script runs
	logPath string // argv log
}

type hopt struct {
	pgRestoreExit string // exit code for the pg_restore stub (default "0")
	psqlExit      string // exit code for every psql call (default "0")
	psqlNextExit  string // exit code for psql calls AFTER the first (default = psqlExit)
	psqlFirst     string // table count for the emptiness check (default "0")
	psqlNext      string // table count for later psql calls (default = psqlFirst)
	sha           string // hash the sha256sum stub prints (default: the real one)
	noShaTool     bool   // omit the sha256sum stub entirely
	tableCount    int64  // expected tables embedded in the kit (with tableCountSet)
	tableCountSet bool   // distinguishes "expect 0" from "no expectation"
}

func (o hopt) or(def, v string) string {
	if v == "" {
		return def
	}
	return v
}

// tableCountOrUnknown defaults the harness to "no expectation" (-1): only
// tests that set tableCountSet embed a cross-check. A set tableCount of 0
// is a genuine zero-table expectation (round-2 R2-P1-02).
func (o hopt) tableCountOrUnknown() int64 {
	if !o.tableCountSet {
		return -1
	}
	return o.tableCount
}

func newHarness(t *testing.T, o hopt) (*harness, string) {
	t.Helper()
	h := &harness{dir: t.TempDir(), logPath: filepath.Join(t.TempDir(), "argv.log")}
	binDir := t.TempDir()
	sha := o.sha
	if sha == "" {
		sha = testKit().SHA256
	}
	psqlNext := o.psqlNext
	if psqlNext == "" {
		psqlNext = o.or("0", o.psqlFirst)
	}
	cnt := filepath.Join(h.dir, ".psql-calls")
	stub := func(name, body string) {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	logLine := fmt.Sprintf(`printf 'TOOL:%%s ARGV:[%%s]\n' "${0##*/}" "$*" >> '%s'`, h.logPath)
	stub("age", `#!/bin/sh
`+logLine+`
out=""
prev=""
for a in "$@"; do
  [ "$prev" = "-o" ] && out="$a"
  prev="$a"
done
printf 'FAKE-PG-DUMP-CONTENT' > "$out"
exit 0
`)
	stub("pg_restore", "#!/bin/sh\n"+logLine+"\nexit "+o.or("0", o.pgRestoreExit)+"\n")
	stub("psql", `#!/bin/sh
`+logLine+`
n=$(cat '`+cnt+`' 2>/dev/null || echo 0)
echo $((n+1)) > '`+cnt+`'
if [ "$n" = "0" ]; then echo '`+o.or("0", o.psqlFirst)+`'; else echo '`+psqlNext+`'; fi
if [ "$n" = "0" ]; then exit `+o.or("0", o.psqlExit)+`; fi
exit `+o.or(o.or("0", o.psqlExit), o.psqlNextExit)+`
`)
	if !o.noShaTool {
		stub("sha256sum", "#!/bin/sh\n"+logLine+"\necho '"+sha+"'\n")
	}
	// Absolute-path wrappers for every coreutils tool the script shells out
	// to. With these, tests can restrict PATH to the stub dir ALONE, which
	// makes the missing-hash-tool case genuinely reachable (round-3: the
	// old test kept the real /usr/bin/sha256sum reachable and only ever hit
	// the hash-mismatch branch).
	for _, name := range []string{"mkdir", "rm", "chmod", "cut", "cat"} {
		wrapper := "#!/bin/sh\nexec /usr/bin/" + name + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(wrapper), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// mktemp -d TEMPLATE: one deterministic dir per harness (tests are
	// sequential within a harness and the script cleans up after itself).
	stub("mktemp", `#!/bin/sh
# usage: mktemp -d TEMPLATE
tpl="$2"
d="${tpl%??????}.harness"
mkdir "$d" 2>/dev/null || true
echo "$d"
`)
	script := GenerateRestoreScriptWithTables(testKit(), o.tableCountOrUnknown())
	scriptPath := filepath.Join(h.dir, "restore.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// The staged ciphertext the script is pointed at.
	if err := os.WriteFile(filepath.Join(h.dir, "staged.dump.age"), []byte("AGE-CIPHERTEXT"), 0o600); err != nil {
		t.Fatal(err)
	}
	return h, binDir
}

// run executes restore.sh with the given env and args inside the harness
// working directory, where a "collateral.txt" file exists that must survive.
func (h *harness) run(t *testing.T, binDir string, env []string, args ...string) error {
	t.Helper()
	collateral := filepath.Join(h.dir, "collateral.txt")
	if err := os.WriteFile(collateral, []byte("do not delete"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{filepath.Join(h.dir, "restore.sh")}, args...)...)
	cmd.Dir = h.dir
	cmd.Env = append(os.Environ(), "PATH="+binDir)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("restore.sh output:\n%s", out)
	}
	if _, serr := os.Stat(collateral); serr != nil {
		t.Fatalf("pre-existing file in the working directory was deleted by the script (P1-02)")
	}
	return err
}

func (h *harness) argvLog(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(h.logPath)
	if err != nil {
		t.Fatalf("argv log unreadable: %v", err)
	}
	return string(b)
}

func (h *harness) tempDirLeftovers(t *testing.T) []string {
	t.Helper()
	entries, _ := os.ReadDir("/tmp")
	var left []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "supabackup-restore.") {
			left = append(left, e.Name())
		}
	}
	return left
}

func mustFail(t *testing.T, err error, why string) {
	t.Helper()
	if err == nil {
		t.Fatalf("script succeeded, want failure (%s)", why)
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 0 {
		t.Fatalf("failure (%s) exited 0", why)
	}
}

const (
	testTarget = "postgresql://backup@db.example.com:5432/target"
	testEnc    = "staged.dump.age"
	testEnv    = "AGE_IDENTITY_FILE=/run/age/identity.txt"
)

// TestHappyPath: correct hash, empty target → pg_restore called with the
// plaintext path, NO password in any argv, temp dir cleaned up.
func TestHappyPath(t *testing.T) {
	h, binDir := newHarness(t, hopt{psqlNext: "4"})
	err := h.run(t, binDir,
		[]string{testEnv, "PGPASSWORD=sup3rsecret"}, testTarget, testEnc)
	if err != nil {
		t.Fatalf("happy path failed: %v", err)
	}
	log := h.argvLog(t)
	if !strings.Contains(log, "--exit-on-error") || !strings.Contains(log, "--no-owner") {
		t.Fatalf("pg_restore argv missing safety flags: %s", log)
	}
	if strings.Contains(log, "sup3rsecret") {
		t.Fatalf("password leaked into tool argv: %s", log)
	}
	if !strings.Contains(log, "TOOL:pg_restore ARGV:[--dbname="+testTarget+" --exit-on-error --no-owner") {
		t.Fatalf("pg_restore argv malformed (P1-01 continuation breakage?): %s", log)
	}
	if left := h.tempDirLeftovers(t); len(left) > 0 {
		t.Fatalf("temp dirs left behind: %v", left)
	}
}

// TestWrongHashRefusesDecrypt: hash mismatch must fail BEFORE pg_restore.
func TestWrongHashRefusesDecrypt(t *testing.T) {
	h, binDir := newHarness(t, hopt{sha: "deadbeef"})
	mustFail(t, h.run(t, binDir, []string{testEnv}, testTarget, testEnc), "hash mismatch")
	if strings.Contains(h.argvLog(t), "TOOL:pg_restore") {
		t.Fatal("pg_restore ran despite hash mismatch")
	}
}

// TestMissingEncryptedFile: a missing ciphertext fails cleanly.
func TestMissingEncryptedFile(t *testing.T) {
	h, binDir := newHarness(t, hopt{})
	mustFail(t, h.run(t, binDir, []string{testEnv}, testTarget, "/backups/GONE.dump.age"),
		"missing ciphertext")
}

// TestNonEmptyTargetRefused: psql reporting tables > 0 must abort before
// decryption (P1-03); the explicit override proceeds.
func TestNonEmptyTargetRefused(t *testing.T) {
	h, binDir := newHarness(t, hopt{psqlFirst: "12"})
	mustFail(t, h.run(t, binDir, []string{testEnv}, testTarget, testEnc), "non-empty target")
	if strings.Contains(h.argvLog(t), "TOOL:pg_restore") {
		t.Fatal("restore ran on a non-empty target")
	}
	if err := h.run(t, binDir,
		[]string{testEnv, "SUPABACKUP_ALLOW_NONEMPTY=1"}, testTarget, testEnc); err != nil {
		t.Fatalf("override run failed: %v", err)
	}
}

// TestInlinePasswordRefused: a conninfo carrying a password is refused
// before any tool runs.
func TestInlinePasswordRefused(t *testing.T) {
	h, binDir := newHarness(t, hopt{})
	mustFail(t, h.run(t, binDir, []string{testEnv},
		"postgresql://backup:sup3rsecret@db/db", testEnc), "inline password URI")
	mustFail(t, h.run(t, binDir, []string{testEnv},
		"host=db password=sup3rsecret", testEnc), "keyword password")
	// libpq-legal spaced keyword form (round-2 P1-02 remainder).
	mustFail(t, h.run(t, binDir, []string{testEnv},
		"host=db user=backup password = 'sup3rsecret' dbname=t", testEnc), "spaced keyword password")
	// Percent-encoded password parameter (round-3: libpq decodes ?%70assword=
	// into a password field; the script must reject any percent escape).
	mustFail(t, h.run(t, binDir, []string{testEnv},
		"postgresql://db/db?%70assword=sup3rsecret", testEnc), "percent-encoded password")
	// Unknown query parameters are refused outright (allowlist).
	mustFail(t, h.run(t, binDir, []string{testEnv},
		"postgresql://db/db?options=-c%20x", testEnc), "non-allowlisted query param")
	// The allowlisted TLS/timeout parameters keep working.
	if err := h.run(t, binDir, []string{testEnv},
		"postgresql://backup@db/db?sslmode=require&connect_timeout=10", testEnc); err != nil {
		t.Fatalf("allowlisted query params must be accepted: %v", err)
	}
	// No tool may have been invoked at all (the log file is created lazily
	// by the first stub call — its absence proves nothing ran).
	if b, err := os.ReadFile(h.logPath); err == nil && strings.Contains(string(b), "sup3rsecret") {
		t.Fatalf("refused password reached a tool: %s", b)
	}
}

// TestPgRestoreFailurePartialWrite: a pg_restore failure must exit non-zero.
func TestPgRestoreFailurePartialWrite(t *testing.T) {
	h, binDir := newHarness(t, hopt{pgRestoreExit: "1"})
	mustFail(t, h.run(t, binDir, []string{testEnv}, testTarget, testEnc), "pg_restore failure")
}

// TestPostRestoreCountMismatch: manifest declares 7 tables, the restored
// database has 3 → failure even though pg_restore exited 0 (P1-03/P1-08).
func TestPostRestoreCountMismatch(t *testing.T) {
	h, binDir := newHarness(t, hopt{psqlNext: "3", tableCount: 7, tableCountSet: true})
	mustFail(t, h.run(t, binDir, []string{testEnv}, testTarget, testEnc), "table-count mismatch")
}

// TestPostRestoreCountMatch: same scenario, matching count → success.
func TestPostRestoreCountMatch(t *testing.T) {
	h, binDir := newHarness(t, hopt{psqlNext: "7", tableCount: 7, tableCountSet: true})
	if err := h.run(t, binDir, []string{testEnv}, testTarget, testEnc); err != nil {
		t.Fatalf("matching count must succeed: %v", err)
	}
}

// TestPsqlFailureNotSwallowed: a failing emptiness check aborts the script
// (P1-03: the old script swallowed psql failures).
func TestPsqlFailureNotSwallowed(t *testing.T) {
	h, binDir := newHarness(t, hopt{psqlExit: "1"})
	mustFail(t, h.run(t, binDir, []string{testEnv}, testTarget, testEnc), "psql failure")
}

// TestMissingShaTool: no sha256sum/shasum → clean refusal (error detection
// is mandatory, not optional).
func TestMissingShaTool(t *testing.T) {
	h, binDir := newHarness(t, hopt{noShaTool: true})
	mustFail(t, h.run(t, binDir, []string{testEnv}, testTarget, testEnc), "missing sha tool")
}

// TestTempDirCleanedOnError: even a restore failure leaves no plaintext
// behind (the trap must fire on every path).
func TestTempDirCleanedOnError(t *testing.T) {
	h, binDir := newHarness(t, hopt{pgRestoreExit: "2"})
	mustFail(t, h.run(t, binDir, []string{testEnv}, testTarget, testEnc), "restore failure cleanup")
	if left := h.tempDirLeftovers(t); len(left) > 0 {
		t.Fatalf("temp dirs (with the plaintext dump!) left behind: %v", left)
	}
}

// TestPostRestorePsqlFailure: the FINAL count query failing (after a
// successful restore) must fail the script — not just the pre-flight check
// (round-2 P2-03: the old test only exercised the first psql call).
func TestPostRestorePsqlFailure(t *testing.T) {
	h, binDir := newHarness(t, hopt{psqlNextExit: "1"})
	mustFail(t, h.run(t, binDir, []string{testEnv}, testTarget, testEnc),
		"post-restore psql failure")
	if !strings.Contains(h.argvLog(t), "TOOL:pg_restore") {
		t.Fatal("expected the restore to have run before the count check failed")
	}
}

// TestGenuineZeroTableExpectation: a manifest declaring 0 tables with a
// restored 0-table database must SUCCEED (round-2 R2-P1-02: known-zero must
// not be silently treated as unknown, and unknown must not fail on 0).
func TestGenuineZeroTableExpectation(t *testing.T) {
	h, binDir := newHarness(t, hopt{tableCount: 0, tableCountSet: true, psqlNext: "0"})
	if err := h.run(t, binDir, []string{testEnv}, testTarget, testEnc); err != nil {
		t.Fatalf("zero-table expectation with zero restored tables must succeed: %v", err)
	}
	// Declared 0 but restored 3 → mismatch failure.
	h2, binDir2 := newHarness(t, hopt{tableCount: 0, tableCountSet: true, psqlNext: "3"})
	mustFail(t, h2.run(t, binDir2, []string{testEnv}, testTarget, testEnc),
		"zero-table mismatch")
}
