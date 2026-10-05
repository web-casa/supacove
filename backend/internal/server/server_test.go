package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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
	"github.com/cloudfan/supabackup/backend/internal/web"
)

// webfsStat reports whether the embedded SPA has a real index.html.
func webfsStat() (fs.FileInfo, error) {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return nil, err
	}
	return fs.Stat(dist, "index.html")
}

type testEnv struct {
	t         *testing.T
	base      string
	client    *http.Client // cookie jar: acts as the browser
	authStore *auth.Store
	store     *db.Store
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		DataDir:           t.TempDir(),
		SessionTTL:        time.Hour,
		BootstrapTokenTTL: time.Minute,
		InsecureCookie:    true, // plain-HTTP test server
	}
}

func newTestEnvWithConfig(t *testing.T, cfg *config.Config) *testEnv {
	t.Helper()
	store, err := db.Open(cfg.DataDir)
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authStore := auth.NewStore(store.DB)
	key := make([]byte, 32)
	srv := New(store, authStore, cfg, key, limiter.New(), slog.New(slog.NewTextHandler(io.Discard, nil)), BuildInfo{Version: "test"})
	ts := httptestServer(t, srv.Router())
	jar, _ := cookiejar.New(nil)
	return &testEnv{t: t, base: ts.URL, authStore: authStore, store: store, client: &http.Client{Jar: jar}}
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return newTestEnvWithConfig(t, testConfig(t))
}

func httptestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

func postJSON(t *testing.T, env *testEnv, path string, body any, headers ...map[string]string) (int, map[string]any) {
	t.Helper()
	return do(t, env.client, http.MethodPost, env.base+path, body, headers...)
}

func getJSON(t *testing.T, env *testEnv, path string) (int, map[string]any) {
	t.Helper()
	return do(t, env.client, http.MethodGet, env.base+path, nil)
}

func do(t *testing.T, client *http.Client, method, rawURL string, body any, headers ...map[string]string) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			reader = strings.NewReader(b) // raw body escape hatch
		default:
			var buf bytes.Buffer
			_ = json.NewEncoder(&buf).Encode(body)
			reader = &buf
		}
	}
	req, err := http.NewRequestWithContext(context.Background(), method, rawURL, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if body != nil {
		if _, ok := body.(string); !ok {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	for _, hs := range headers {
		for k, v := range hs {
			req.Header.Set(k, v)
		}
	}
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
		if strings.HasSuffix(c.Name, "sb_csrf") {
			return c.Value
		}
	}
	return ""
}

func (env *testEnv) bootstrapAdmin(t *testing.T) {
	t.Helper()
	token, err := env.authStore.CreateBootstrapToken(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := postJSON(t, env, "/api/auth/bootstrap", map[string]string{
		"token": token, "username": "admin", "password": "long-enough-password",
	}); code != http.StatusCreated {
		t.Fatalf("bootstrap: want 201, got %d", code)
	}
	if code, _ := postJSON(t, env, "/api/auth/login", map[string]string{
		"username": "admin", "password": "long-enough-password",
	}); code != http.StatusOK {
		t.Fatalf("login: want 200, got %d", code)
	}
}

// TestBootstrapCannotBeTakenOver is the P1 DoD: without a locally issued
// one-time token, no HTTP request can create the admin account.
func TestBootstrapCannotBeTakenOver(t *testing.T) {
	env := newTestEnv(t)

	if code, body := postJSON(t, env, "/api/auth/bootstrap", map[string]string{
		"token": "", "username": "attacker", "password": "attacker-password-1",
	}); code != http.StatusBadRequest || body["code"] != "invalid_request" {
		t.Fatalf("bootstrap without token: want 400 invalid_request, got %d %v", code, body)
	}
	if code, _ := postJSON(t, env, "/api/auth/bootstrap", map[string]string{
		"token": "forged", "username": "attacker", "password": "attacker-password-1",
	}); code != http.StatusForbidden {
		t.Fatalf("bootstrap with forged token: want 403, got %d", code)
	}
	if has, _ := env.authStore.HasUser(t.Context()); has {
		t.Fatal("no user may exist after failed takeover attempts")
	}

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
		t.Fatalf("/api/healthz: code=%d body=%v", code, body)
	}
	if code, body := getJSON(t, env, "/api/ready"); code != 200 || body["status"] != "ok" {
		t.Fatalf("/api/ready: code=%d body=%v", code, body)
	}
	// Details are protected: anonymous must get 401.
	if code, _ := getJSON(t, env, "/api/health/details"); code != http.StatusUnauthorized {
		t.Fatalf("anonymous /api/health/details: want 401, got %d", code)
	}

	env.bootstrapAdmin(t)
	if code, body := getJSON(t, env, "/api/health/details"); code != 200 || body["version"] != "test" {
		t.Fatalf("authed /api/health/details: code=%d body=%v", code, body)
	}
}

func TestLoginSessionCSRFLogout(t *testing.T) {
	env := newTestEnv(t)
	env.bootstrapAdmin(t)
	csrf := env.csrfToken(t)
	if csrf == "" {
		t.Fatal("csrf cookie must be set after login")
	}

	if code, _ := getJSON(t, env, "/api/auth/me"); code != 200 {
		t.Fatalf("me after login: want 200, got %d", code)
	}

	// Logout without CSRF header → 403; wrong value → 403; correct → 204.
	if code, _ := postJSON(t, env, "/api/auth/logout", nil); code != http.StatusForbidden {
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
	env.bootstrapAdmin(t)

	saw429 := false
	for i := range 7 {
		code, _ := postJSON(t, env, "/api/auth/login", map[string]string{
			"username": "admin", "password": fmt.Sprintf("wrong-password-%d", i),
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

// TestLoginRateLimitIgnoresUntrustedForwardedHeaders (review P0-01): rotating
// spoofed proxy headers must not reset the rate limiter for a direct client.
func TestLoginRateLimitIgnoresUntrustedForwardedHeaders(t *testing.T) {
	env := newTestEnv(t)
	env.bootstrapAdmin(t)

	attempts := 0
	saw429 := false
	for i := range 15 {
		hdrs := map[string]string{
			"X-Forwarded-For": fmt.Sprintf("203.0.113.%d", i),
			"X-Real-IP":       fmt.Sprintf("198.51.100.%d", i),
			"True-Client-IP":  fmt.Sprintf("192.0.2.%d", i),
		}
		code, _ := do(t, env.client, http.MethodPost, env.base+"/api/auth/login",
			map[string]string{"username": "admin", "password": fmt.Sprintf("wrong-%d", i)}, hdrs)
		attempts++
		if code == http.StatusTooManyRequests {
			saw429 = true
			break
		}
	}
	if !saw429 {
		t.Fatalf("spoofed forward headers must not bypass rate limiting (made %d attempts)", attempts)
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
	// Same-origin request (the test server's own origin) passes the gate.
	env.bootstrapAdmin(t)
	origin := map[string]string{"Origin": env.base}
	if code, _ := postJSON(t, env, "/api/auth/logout", nil,
		origin, map[string]string{csrfHeader: env.csrfToken(t)}); code != http.StatusNoContent {
		t.Fatalf("same-origin logout: want 204, got %d", code)
	}
}

// TestSameOriginSchemeAndEffectivePort (review P1-01): scheme and effective
// port are part of the origin; omitted ports take scheme defaults.
func TestSameOriginSchemeAndEffectivePort(t *testing.T) {
	// Origins are built per-env: each case gets a fresh instance whose port
	// differs, so the exact-match case targets that instance's own origin.
	cases := []struct {
		name   string
		origin func(host string) string
		want   int // expected status for logout with this Origin
	}{
		{"exact match", func(h string) string { return "http://" + h }, http.StatusNoContent},
		{"wrong scheme", func(h string) string { return "https://" + h }, http.StatusForbidden},
		{"wrong port", func(h string) string { return "http://" + h + ":9999" }, http.StatusForbidden},
		{"wrong host", func(string) string { return "http://evil.example" }, http.StatusForbidden},
		{"null origin", func(string) string { return "null" }, http.StatusForbidden},
		{"userinfo", func(h string) string { return "http://user@" + h }, http.StatusForbidden},
		{"path", func(h string) string { return "http://" + h + "/path" }, http.StatusForbidden},
		{"query", func(h string) string { return "http://" + h + "?x=1" }, http.StatusForbidden},
		{"repeated", func(h string) string { return "http://" + h + ",http://" + h }, http.StatusForbidden},
	}
	for _, tc := range cases {
		env2 := newTestEnv(t)
		env2.bootstrapAdmin(t)
		host := strings.TrimPrefix(env2.base, "http://")
		code, _ := postJSON(t, env2, "/api/auth/logout", nil,
			map[string]string{"Origin": tc.origin(host), csrfHeader: env2.csrfToken(t)})
		if code != tc.want {
			t.Errorf("%s: want %d, got %d", tc.name, tc.want, code)
		}
	}
}

// TestAuthBodyLimitAndValidation (review P0-02/P1-09): oversized bodies are
// 413 before hashing; malformed JSON and wrong media types use the Error shape.
func TestAuthBodyLimitAndValidation(t *testing.T) {
	env := newTestEnv(t)

	huge := map[string]string{"token": strings.Repeat("x", 20<<10)}
	code, body := postJSON(t, env, "/api/auth/login", huge)
	if code != http.StatusRequestEntityTooLarge || body["code"] != "payload_too_large" {
		t.Fatalf("oversized body: want 413 payload_too_large, got %d %v", code, body)
	}

	code, body = do(t, env.client, http.MethodPost, env.base+"/api/auth/login", "{ malformed",
		map[string]string{"Content-Type": "application/json"})
	if code != http.StatusBadRequest || body["code"] != "invalid_request" {
		t.Fatalf("malformed JSON: want 400 invalid_request, got %d %v", code, body)
	}
	// Trailing garbage after one JSON value is rejected, not silently decoded.
	code, body = do(t, env.client, http.MethodPost, env.base+"/api/auth/login",
		`{"username":"admin","password":"long-enough-password"} trailing`,
		map[string]string{"Content-Type": "application/json"})
	if code != http.StatusBadRequest || body["code"] != "invalid_request" {
		t.Fatalf("trailing JSON garbage: want 400 invalid_request, got %d %v", code, body)
	}

	code, body = postJSON(t, env, "/api/auth/login", map[string]string{})
	if code != http.StatusBadRequest || body["code"] != "invalid_request" {
		t.Fatalf("missing required fields: want 400, got %d %v", code, body)
	}

	// text/plain body → 415.
	req, _ := http.NewRequest(http.MethodPost, env.base+"/api/auth/login", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain body: want 415, got %d", resp.StatusCode)
	}
}

// TestSessionPersistenceFailuresDoNotReportSuccess (review P1-02): injected
// storage failures must turn login/logout into honest errors.
func TestSessionPersistenceFailuresDoNotReportSuccess(t *testing.T) {
	env := newTestEnv(t)
	env.bootstrapAdmin(t)
	csrf := env.csrfToken(t)

	// Fail session inserts.
	if _, err := env.store.DB.Exec(
		`CREATE TRIGGER fail_session_insert BEFORE INSERT ON sessions
		 BEGIN SELECT RAISE(FAIL, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	// A second session insert fails: logging in again (fresh env semantics)
	// would 500 — here the effect shows on any new session creation.
	code, body := postJSON(t, env, "/api/auth/login", map[string]string{
		"username": "admin", "password": "long-enough-password",
	})
	if code != http.StatusInternalServerError || body["code"] != "internal" {
		t.Fatalf("login with failing session insert: want 500 internal, got %d %v", code, body)
	}
	if _, err := env.store.DB.Exec(`DROP TRIGGER fail_session_insert`); err != nil {
		t.Fatal(err)
	}

	// Fail session deletes: logout must NOT report 204.
	if _, err := env.store.DB.Exec(
		`CREATE TRIGGER fail_session_delete BEFORE DELETE ON sessions
		 BEGIN SELECT RAISE(FAIL, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	code, body = postJSON(t, env, "/api/auth/logout", nil, map[string]string{csrfHeader: csrf})
	if code != http.StatusInternalServerError || body["code"] != "internal" {
		t.Fatalf("logout with failing delete: want 500 internal, got %d %v", code, body)
	}
	if _, err := env.store.DB.Exec(`DROP TRIGGER fail_session_delete`); err != nil {
		t.Fatal(err)
	}
	// The old session must still be valid — revocation did not happen.
	if code, _ := getJSON(t, env, "/api/auth/me"); code != http.StatusOK {
		t.Fatalf("session must survive a failed logout: want 200, got %d", code)
	}
}

// TestSessionLookupFailureIsNot401 (review P1-09): storage failure reads as
// 503, never as "not logged in".
func TestSessionLookupFailureIsNot401(t *testing.T) {
	env := newTestEnv(t)
	env.bootstrapAdmin(t)

	// Close the underlying DB to force lookup errors.
	env.store.DB.Close()
	code, _ := getJSON(t, env, "/api/auth/me")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("session lookup failure: want 503, got %d", code)
	}
}

// TestLogoutWithBodyStillRequiresAuthAndCSRF (review round 3, R3-P1-02): a
// JSON body on logout must never skip the session and CSRF checks — the
// previous fast path let an anonymous forged-CSRF logout return 204 without
// revoking anything.
func TestLogoutWithBodyStillRequiresAuthAndCSRF(t *testing.T) {
	env := newTestEnv(t)
	env.bootstrapAdmin(t)
	csrf := env.csrfToken(t)

	// Anonymous logout (fresh client, no cookies) with a non-empty body:
	// must be 401, NOT 204.
	anon := &http.Client{} // deliberately no cookie jar
	if code, _ := do(t, anon, http.MethodPost, env.base+"/api/auth/logout",
		map[string]string{}, map[string]string{"Content-Type": "application/json", csrfHeader: "forged"}); code != http.StatusUnauthorized {
		t.Fatalf("anonymous logout with body: want 401, got %d", code)
	}
	// Authenticated but forged CSRF token with a non-empty body: 403.
	if code, _ := postJSON(t, env, "/api/auth/logout", map[string]string{},
		map[string]string{csrfHeader: "forged"}); code != http.StatusForbidden {
		t.Fatalf("logout with forged csrf and body: want 403, got %d", code)
	}
	// A protected unknown path with a body never succeeds: anonymous → 401
	// (default deny); authenticated with valid CSRF → JSON 404 from the API
	// router — never 2xx, never the SPA.
	if code, _ := postJSON(t, env, "/api/auth/future-protected", map[string]string{},
		map[string]string{csrfHeader: csrf}); code < 400 || code >= 500 {
		t.Fatalf("unknown auth path with body: want a 4xx, got %d", code)
	}
	// Sanity: proper logout still works end-to-end.
	if code, _ := postJSON(t, env, "/api/auth/logout", map[string]string{},
		map[string]string{csrfHeader: csrf}); code != http.StatusNoContent {
		t.Fatalf("proper logout: want 204, got %d", code)
	}
	if code, _ := getJSON(t, env, "/api/auth/me"); code != http.StatusUnauthorized {
		t.Fatalf("me after logout: want 401, got %d", code)
	}
}

// TestLoginFieldLimits (review P0-02 remainder): oversized inputs are a 400
// before the KDF, not a 401 credential check.
func TestLoginFieldLimits(t *testing.T) {
	env := newTestEnv(t)
	env.bootstrapAdmin(t)

	code, body := postJSON(t, env, "/api/auth/login", map[string]string{
		"username": strings.Repeat("u", 65), "password": strings.Repeat("p", 20),
	})
	if code != http.StatusBadRequest || body["code"] != "invalid_request" {
		t.Fatalf("oversized username: want 400 invalid_request, got %d %v", code, body)
	}
	code, _ = postJSON(t, env, "/api/auth/login", map[string]string{
		"username": "admin", "password": strings.Repeat("p", 129),
	})
	if code != http.StatusBadRequest {
		t.Fatalf("oversized password: want 400, got %d", code)
	}
}

// TestProductionCookieAttributes (review P2-01/P1-13 DoD): Secure mode uses
// __Host- prefixed cookies with Secure and no Domain.
func TestProductionCookieAttributes(t *testing.T) {
	cfg := testConfig(t)
	cfg.InsecureCookie = false
	env := newTestEnvWithConfig(t, cfg)

	token, err := env.authStore.CreateBootstrapToken(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{
		"token": token, "username": "admin", "password": "long-enough-password",
	})
	req, _ := http.NewRequest(http.MethodPost, env.base+"/api/auth/bootstrap",
		bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(payload))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap: want 201, got %d", resp.StatusCode)
	}
	var sessionCookie, csrfCookie *http.Cookie
	for _, c := range resp.Cookies() {
		switch c.Name {
		case "__Host-sb_session":
			sessionCookie = c
		case "__Host-sb_csrf":
			csrfCookie = c
		}
	}
	if sessionCookie == nil || csrfCookie == nil {
		t.Fatalf("want __Host- prefixed cookies, got %v", resp.Cookies())
	}
	for _, c := range []*http.Cookie{sessionCookie, csrfCookie} {
		if !c.Secure || c.Domain != "" || c.Path != "/" {
			t.Fatalf("__Host- cookie %s must be Secure, Path=/, no Domain: %+v", c.Name, c)
		}
	}
	if !sessionCookie.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}
}

func TestStaticSPA(t *testing.T) {
	env := newTestEnv(t)

	// When the SPA was never built into this binary, the root must answer
	// 503 with a clear hint — never an empty page.
	req, _ := http.NewRequest(http.MethodGet, env.base+"/", nil)
	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if _, err := webfsStat(); err == nil {
		if resp.StatusCode != 200 || !strings.Contains(string(raw), "supabackup") {
			t.Fatalf("GET /: status=%d built=%v", resp.StatusCode, strings.Contains(string(raw), "supabackup"))
		}
	} else {
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("unbuilt frontend: want 503, got %d", resp.StatusCode)
		}
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		if body["code"] != "frontend_not_built" {
			t.Fatalf("unbuilt frontend body: %v", body)
		}
	}

	// Unknown API path: anonymous gets 401 (default-deny, no path oracle);
	// an authenticated caller gets the JSON 404 from the API router.
	if code, body := getJSON(t, env, "/api/nope"); code != http.StatusUnauthorized || body["code"] != "unauthenticated" {
		t.Fatalf("unknown api anonymous: code=%d body=%v", code, body)
	}
	env.bootstrapAdmin(t)
	if code, body := getJSON(t, env, "/api/nope"); code != 404 || body["code"] == nil {
		t.Fatalf("unknown api authenticated: code=%d body=%v", code, body)
	}
}
