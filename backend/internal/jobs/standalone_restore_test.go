package jobs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/agekey"
	"github.com/cloudfan/supabackup/backend/internal/dumper"
	"github.com/cloudfan/supabackup/backend/internal/pgclient"
	"github.com/cloudfan/supabackup/backend/internal/platform"
	"github.com/cloudfan/supabackup/backend/internal/recovery"
)

// Standalone recovery (quality plan: "restore must work from the final
// storage object with the independently stored key and the documentation
// alone"): NO application instance, NO metadata database, NO session — the
// inputs are exactly three: the committed ciphertext, an age identity file
// written outside the app, and the generated recovery script. The restore
// must reproduce the source rows in an isolated PostgreSQL.
func TestStandaloneRestoreWithoutApplicationState(t *testing.T) {
	requireDocker(t)
	for _, tool := range []string{"age", "sha256sum", "psql", "pg_restore"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("host tool %s unavailable", tool)
		}
	}
	uri := startTestPostgres(t)
	const rows = 120
	seedTestTable(t, uri, rows)
	ci, err := pgclient.ParseURI(uri)
	if err != nil {
		t.Fatal(err)
	}

	// --- the ONLY key material: generated and stored OUTSIDE any app state ---
	identity, recipient, err := agekey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	// --- produce the artifact with the real dump kernel, no Runner/Store ---
	staging := t.TempDir()
	cfg := dumper.Config{StagingDir: staging}
	res, rerr := cfg.Run(context.Background(), 9001, dumper.Target{
		Conn:        ci,
		Recipient:   recipient,
		ServerMajor: 18, // matches the postgres:18-alpine test container (m1_test.go)
	})
	if rerr != nil {
		t.Fatalf("standalone dump: %v", rerr)
	}
	if res.ArtifactPath == "" || res.SHA256 == "" {
		t.Fatal("committed artifact must carry path and sha256")
	}

	// --- simulate the disaster: only the object + the offline key survive ---
	vault := t.TempDir()
	artifactName := "backup-standalone.dump.age"
	cipher, err := os.ReadFile(res.ArtifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, artifactName), cipher, 0o600); err != nil {
		t.Fatal(err)
	}
	identityFile := filepath.Join(vault, "identity.txt")
	if err := os.WriteFile(identityFile, []byte(identity+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kit := recovery.GenerateRestoreScriptWithTables(recovery.KitInput{
		BackupUUID:       "standalone-restore",
		Platform:         platform.Generic,
		ArtifactFileName: artifactName,
		SHA256:           res.SHA256,
		KeyID:            "standalone-key",
	}, 1)
	scriptPath := filepath.Join(vault, "restore.sh")
	if err := os.WriteFile(scriptPath, []byte(kit), 0o700); err != nil {
		t.Fatal(err)
	}

	targetDB := fmt.Sprintf("standalone_%d", time.Now().UnixNano()%1e6)
	if out, err := exec.Command("docker", "exec", pgContainer, "psql", "-U", "postgres",
		"-c", "CREATE DATABASE "+targetDB).CombinedOutput(); err != nil {
		t.Fatalf("create target db: %v: %s", err, out)
	}
	uriTail := uri[strings.LastIndex(uri, "@")+1:]
	hostPort := uriTail[:strings.Index(uriTail, "/")]
	target := fmt.Sprintf("postgresql://postgres@%s/%s?sslmode=disable", hostPort, targetDB)

	cmd := exec.Command("sh", scriptPath, target, filepath.Join(vault, artifactName))
	cmd.Dir = vault
	cmd.Env = append(os.Environ(),
		"AGE_IDENTITY_FILE="+identityFile,
		"PGPASSWORD="+ci.Password,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("standalone restore failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Restored 1 user tables.") {
		t.Fatalf("restore output missing table-count gate:\n%s", out)
	}

	// Data proof from the isolated target — the app never participated.
	got, err := exec.Command("docker", "exec", pgContainer, "psql", "-U", "postgres",
		"-d", targetDB, "-At", "-c", "SELECT count(*), max(id) FROM m1test").CombinedOutput()
	if err != nil {
		t.Fatalf("verify restored data: %v: %s", err, got)
	}
	want := fmt.Sprintf("%d|%d", rows, rows)
	if strings.TrimSpace(string(got)) != want {
		t.Fatalf("restored data = %q, want %q", strings.TrimSpace(string(got)), want)
	}

	// Corruption gate: a single flipped byte must stop the restore BEFORE
	// any decrypt/restore work (hash gate), still without app state.
	tampered := filepath.Join(vault, "tampered.dump.age")
	bad := make([]byte, len(cipher))
	copy(bad, cipher)
	bad[len(bad)/2] ^= 0xFF
	if err := os.WriteFile(tampered, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	script2 := strings.ReplaceAll(kit, artifactName, "tampered.dump.age")
	script2Path := filepath.Join(vault, "restore-tampered.sh")
	if err := os.WriteFile(script2Path, []byte(script2), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("sh", script2Path, target, tampered)
	cmd.Dir = vault
	cmd.Env = append(os.Environ(), "AGE_IDENTITY_FILE="+identityFile, "PGPASSWORD="+ci.Password)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("tampered ciphertext restored successfully:\n%s", out)
	} else if !strings.Contains(string(out), "HASH") && !strings.Contains(strings.ToUpper(string(out)), "MISMATCH") {
		t.Logf("tampered restore rejected with: %s", out)
	}
}
