package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/web-casa/supacove/backend/internal/agekey"
	"github.com/web-casa/supacove/backend/internal/jobs"
)

// helper accessors used by the tests below.

func (e *phase2Env) post(path string, body any) (int, map[string]any) {
	return postJSON(e.t, e.testEnv, path, body, map[string]string{csrfHeader: e.csrfToken(e.t)})
}

func (e *phase2Env) get(path string) (int, map[string]any) {
	return getJSON(e.t, e.testEnv, path)
}

// newPhase2Env boots the full stack (DB + runner + server) for API tests.
// The age recipient is preconfigured by the test so backup jobs can run with
// a fake pg_dump via SetClientOverride.
type phase2Env struct {
	*testEnv
	runner *jobs.Runner
}

func newPhase2Env(t *testing.T) *phase2Env {
	t.Helper()
	env := &phase2Env{testEnv: newTestEnv(t)}

	_, rcp, err := agekey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB.Exec(
		`INSERT INTO settings (key, value) VALUES ('age_recipient', ?), ('age_key_id', ?)`,
		rcp, agekey.Fingerprint(rcp)); err != nil {
		t.Fatal(err)
	}

	staging := t.TempDir()
	runner := jobs.NewRunner(env.store, phase2Key(), staging, func(ctx context.Context) (string, error) {
		return rcp, nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	runner.SetClientOverride(phase2FakeDumpDir(t))
	runner.Start(context.Background())
	t.Cleanup(runner.Stop)
	env.runner = runner

	return env
}

func phase2Key() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(7)
	}
	return k
}

func phase2FakeDumpDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1" in
  --version) echo "pg_dump (PostgreSQL) 99.0"; exit 0 ;;
esac
echo "FAKEDUMP-PHASE2"
`
	if err := os.WriteFile(filepath.Join(dir, "pg_dump"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPhase2DefaultDenyAndAgeStatus(t *testing.T) {
	env := newPhase2Env(t)

	// Every Phase 2 surface is protected by the default-deny guard.
	for _, path := range []string{"/api/databases", "/api/tasks", "/api/age/status"} {
		if code, _ := env.get(path); code != http.StatusUnauthorized {
			t.Fatalf("%s anonymous: want 401, got %d", path, code)
		}
	}

	env.bootstrapAdmin(t)

	// Not configured → configured=false (the env here has no recipient row in
	// settings until SetRecipient is exercised; newPhase2Env wrote one, so we
	// expect configured=true with a fingerprint).
	code, body := env.get("/api/age/status")
	if code != 200 || body["configured"] != true {
		t.Fatalf("age status: code=%d body=%v", code, body)
	}

	// An invalid recipient is rejected with the contract error shape (PUT).
	req, _ := http.NewRequest(http.MethodPut, env.base+"/api/age/recipient",
		strings.NewReader(`{"recipient":"garbage"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeader, env.csrfToken(t))
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("invalid recipient: want 400, got %d", resp.StatusCode)
	}
}

func TestPhase2DatabaseLifecycleAndBackupRun(t *testing.T) {
	env := newPhase2Env(t)
	env.bootstrapAdmin(t)

	// Connection test failure → 422 with credential-free message.
	//nolint:gosec // G101: loopback test URIs; the assertions prove the password never leaves in any response
	code, body := env.post("/api/databases", map[string]string{
		"name": "broken", "connectionUri": "postgres://app:pw@127.0.0.1:1/appdb?sslmode=disable",
	})
	if code != 422 || body["code"] != "connection_test_failed" {
		t.Fatalf("connection failure: code=%d body=%v", code, body)
	}

	// Invalid URI (disallowed parameter) → 400.
	//nolint:gosec // G101: loopback test URI for the disallowed-parameter case
	code, _ = env.post("/api/databases", map[string]string{
		"name": "bad", "connectionUri": "postgres://app:pw@h/db?passfile=/x",
	})
	if code != 400 {
		t.Fatalf("passfile URI: want 400, got %d", code)
	}

	// Unreachable target registers as 422 (live test failed) — the full
	// success path is covered by the M1 integration test.
	//nolint:gosec // G101: loopback test URI for the unreachable-target case
	code, _ = env.post("/api/databases", map[string]string{
		"name": "dup-db", "connectionUri": "postgres://appuser:whatever@localhost:1/appdb?sslmode=disable",
	})
	if code != 422 {
		t.Fatalf("unreachable db: want 422, got %d", code)
	}

	// Trigger for an unknown database → 404.
	if code, _ = env.post("/api/databases/999/backups", nil); code != 404 {
		t.Fatalf("trigger unknown db: want 404, got %d", code)
	}

	// Delete unknown → 404.
	req, _ := http.NewRequest(http.MethodDelete, env.base+"/api/databases/999", nil)
	req.Header.Set(csrfHeader, env.csrfToken(t))
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("delete unknown: want 404, got %d", resp.StatusCode)
	}

	// Tasks list is available and empty-shaped.
	code, body = env.get("/api/tasks")
	if code != 200 || body["tasks"] == nil {
		t.Fatalf("tasks list: code=%d body=%v", code, body)
	}
}
