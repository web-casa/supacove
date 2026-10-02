package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/auth"
	"github.com/cloudfan/supabackup/backend/internal/config"
	"github.com/cloudfan/supabackup/backend/internal/db"
	"github.com/cloudfan/supabackup/backend/internal/limiter"
)

type testEnv struct {
	base      string
	client    *http.Client // cookie jar: acts as the browser
	authStore *auth.Store
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	cfg := &config.Config{
		DataDir:           t.TempDir(),
		SessionTTL:        time.Hour,
		BootstrapTokenTTL: time.Minute,
		InsecureCookie:    true, // plain-HTTP test server
	}
	store, err := db.Open(cfg.DataDir)
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authStore := auth.NewStore(store.DB)
	srv := New(store, authStore, cfg, limiter.New(), slog.New(slog.NewTextHandler(io.Discard, nil)), BuildInfo{Version: "test"})
	ts := httptestServer(t, srv.Router())
	jar, _ := cookiejar.New(nil)
	return &testEnv{base: ts.URL, authStore: authStore, client: &http.Client{Jar: jar}}
}

func httptestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

func postJSON(t *testing.T, env *testEnv, path string, body any, headers ...map[string]string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequest(http.MethodPost, env.base+path, &buf)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, hs := range headers {
		for k, v := range hs {
			req.Header.Set(k, v)
		}
	}
	return doJSON(t, env.client, req)
}

func getJSON(t *testing.T, env *testEnv, path string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.base+path, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return doJSON(t, env.client, req)
}

func doJSON(t *testing.T, client *http.Client, req *http.Request) (int, map[string]any) {
	t.Helper()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (env *testEnv) csrfToken(t *testing.T) string {
	t.Helper()
	u, _ := url.Parse(env.base)
	for _, c := range env.client.Jar.Cookies(u) {
		if c.Name == csrfCookie {
			return c.Value
		}
	}
	return ""
}

// TestBootstrapCannotBeTakenOver is the P1 DoD: without a locally issued
// one-time token, no HTTP request can create the admin account.
func TestBootstrapCannotBeTakenOver(t *testing.T) {
	env := newTestEnv(t)

	// No token at all.
	if code, _ := postJSON(t, env, "/api/auth/bootstrap", map[string]string{
		"token": "", "username": "attacker", "password": "attacker-password-1",
	}); code != http.StatusForbidden {
		t.Fatalf("bootstrap without token: want 403, got %d", code)
	}
	// Forged token.
	if code, _ := postJSON(t, env, "/api/auth/bootstrap", map[string]string{
		"token": "forged", "username": "attacker", "password": "attacker-password-1",
	}); code != http.StatusForbidden {
		t.Fatalf("bootstrap with forged token: want 403, got %d", code)
	}
	if has, _ := env.authStore.HasUser(t.Context()); has {
		t.Fatal("no user may exist after failed takeover attempts")
	}

	// Legitimate local-CLI token works exactly once.
	token, err := env.authStore.CreateBootstrapToken(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := postJSON(t, env, "/api/auth/bootstrap", map[string]string{
		"token": token, "username": "admin", "password": "long-enough-password",
	})
	if code != http.StatusCreated {
		t.Fatalf("legit bootstrap: want 201, got %d", code)
	}

	// A second fresh token must not create a second admin.
	token2, _ := env.authStore.CreateBootstrapToken(time.Minute)
	if code, _ := postJSON(t, env, "/api/auth/bootstrap", map[string]string{
		"token": token2, "username": "admin2", "password": "long-enough-password",
	}); code != http.StatusConflict {
		t.Fatalf("second bootstrap: want 409, got %d", code)
	}
}

func TestHealthEndpoints(t *testing.T) {
	env := newTestEnv(t)

	if code, body := getJSON(t, env, "/api/healthz"); code != 200 || body["status"] != "ok" {
		t.Fatalf("/healthz: code=%d body=%v", code, body)
	}
	if code, body := getJSON(t, env, "/api/ready"); code != 200 || body["status"] != "ok" {
		t.Fatalf("/api/ready: code=%d body=%v", code, body)
	}
	// Details are protected: anonymous must get 401.
	if code, _ := getJSON(t, env, "/api/health/details"); code != http.StatusUnauthorized {
		t.Fatalf("anonymous /api/health/details: want 401, got %d", code)
	}

	token, _ := env.authStore.CreateBootstrapToken(time.Minute)
	postJSON(t, env, "/api/auth/bootstrap", map[string]string{
		"token": token, "username": "admin", "password": "long-enough-password",
	})
	if code, body := getJSON(t, env, "/api/health/details"); code != 200 || body["version"] != "test" {
		t.Fatalf("authed /api/health/details: code=%d body=%v", code, body)
	}
}

func TestLoginSessionCSRFLogout(t *testing.T) {
	env := newTestEnv(t)
	token, _ := env.authStore.CreateBootstrapToken(time.Minute)
	postJSON(t, env, "/api/auth/bootstrap", map[string]string{
		"token": token, "username": "admin", "password": "long-enough-password",
	})

	// Wrong password.
	if code, _ := postJSON(t, env, "/api/auth/login", map[string]string{
		"username": "admin", "password": "wrong-password-99",
	}); code != http.StatusUnauthorized {
		t.Fatalf("wrong password: want 401, got %d", code)
	}

	// Correct password sets session + CSRF cookies.
	if code, _ := postJSON(t, env, "/api/auth/login", map[string]string{
		"username": "admin", "password": "long-enough-password",
	}); code != 200 {
		t.Fatalf("login: want 200, got %d", code)
	}
	csrf := env.csrfToken(t)
	if csrf == "" {
		t.Fatal("csrf cookie must be set after login")
	}

	if code, _ := getJSON(t, env, "/api/auth/me"); code != 200 {
		t.Fatalf("me after login: want 200, got %d", code)
	}

	// Logout without CSRF header → 403; with the cookie value echoed → 204.
	if code, _ := postJSON(t, env, "/api/auth/logout", nil, nil); code != http.StatusForbidden {
		t.Fatalf("logout without csrf: want 403, got %d", code)
	}
	if code, _ := postJSON(t, env, "/api/auth/logout", nil, map[string]string{csrfHeader: "wrong"}); code != http.StatusForbidden {
		t.Fatalf("logout with wrong csrf: want 403, got %d", code)
	}
	if code, _ := postJSON(t, env, "/api/auth/logout", nil, map[string]string{csrfHeader: csrf}); code != http.StatusNoContent {
		t.Fatalf("logout with csrf: want 204, got %d", code)
	}
	if code, _ := getJSON(t, env, "/api/auth/me"); code != http.StatusUnauthorized {
		t.Fatalf("me after logout: want 401, got %d", code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	env := newTestEnv(t)
	token, _ := env.authStore.CreateBootstrapToken(time.Minute)
	postJSON(t, env, "/api/auth/bootstrap", map[string]string{
		"token": token, "username": "admin", "password": "long-enough-password",
	})

	saw429 := false
	for i := 0; i < 7; i++ {
		code, _ := postJSON(t, env, "/api/auth/login", map[string]string{
			"username": "admin", "password": fmt.Sprintf("wrong-%d", i),
		})
		if code == http.StatusTooManyRequests {
			saw429 = true
			break
		}
		if code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: want 401, got %d", i, code)
		}
	}
	if !saw429 {
		t.Fatal("consecutive failures must trigger 429 lockout")
	}
	// Even the correct password is blocked while locked out.
	if code, _ := postJSON(t, env, "/api/auth/login", map[string]string{
		"username": "admin", "password": "long-enough-password",
	}); code != http.StatusTooManyRequests {
		t.Fatalf("locked-out login: want 429, got %d", code)
	}
}

func TestCrossOriginStateChangeRejected(t *testing.T) {
	env := newTestEnv(t)
	headers := map[string]string{"Origin": "https://evil.example"}

	if code, _ := postJSON(t, env, "/api/auth/login", map[string]string{
		"username": "admin", "password": "long-enough-password",
	}, headers); code != http.StatusForbidden {
		t.Fatalf("cross-origin login: want 403, got %d", code)
	}
	// Same-origin request (test server's own origin) is fine; browser clients
	// send Origin on POST, so emulate it.
	origin := map[string]string{"Origin": env.base}
	if code, _ := postJSON(t, env, "/api/auth/login", map[string]string{
		"username": "admin", "password": "long-enough-password",
	}, origin); code != http.StatusUnauthorized {
		// User does not exist in this env: 401 proves the origin gate passed (a cross-origin
	// request would have been rejected with 403 before reaching auth).
		t.Fatalf("same-origin login passed the origin gate but got unexpected %d", code)
	}
}

func TestStaticSPA(t *testing.T) {
	env := newTestEnv(t)

	req, _ := http.NewRequest(http.MethodGet, env.base+"/", nil)
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), "supabackup") {
		t.Fatalf("GET /: status=%d contains=%v", resp.StatusCode, strings.Contains(string(raw), "supabackup"))
	}

	// SPA fallback for a client-side route.
	req2, _ := http.NewRequest(http.MethodGet, env.base+"/some/client/route", nil)
	resp2, err := env.client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("SPA fallback: want 200, got %d", resp2.StatusCode)
	}

	// Unknown API path stays JSON 404, not the SPA.
	if code, body := getJSON(t, env, "/api/nope"); code != 404 || body["code"] == nil {
		t.Fatalf("unknown api: code=%d body=%v", code, body)
	}
}
