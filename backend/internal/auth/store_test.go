package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/db"
)

func newTestAuthStore(t *testing.T) *Store {
	t.Helper()
	store, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewStore(store.DB)
}

// insertExpiredSession creates an already-expired session row via SQL —
// sessions are otherwise only created internally with valid TTLs.
func (s *Store) insertExpiredSession(t *testing.T, userID int64) string {
	t.Helper()
	raw, err := randomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(
		`INSERT INTO sessions (id_hash, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		hashToken(raw), userID, time.Now().Add(-time.Hour).Unix(), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBootstrapLifecycle(t *testing.T) {
	s := newTestAuthStore(t)
	ctx := context.Background()

	if has, err := s.HasUser(ctx); err != nil || has {
		t.Fatalf("fresh store must have no users (has=%v err=%v)", has, err)
	}

	token, err := s.CreateBootstrapToken(time.Minute)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	u, session, err := s.Bootstrap(ctx, token, "admin", "long-enough-password")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if u.Username != "admin" {
		t.Fatalf("unexpected user %+v", u)
	}
	if session == "" {
		t.Fatal("bootstrap must create the first session atomically")
	}
	if got, _ := s.UserForSession(ctx, session); got == nil {
		t.Fatal("bootstrap session must be immediately valid")
	}

	// Even a fresh, unused token must not allow a second admin.
	token2, err := s.CreateBootstrapToken(time.Minute)
	if err != nil {
		t.Fatalf("create token2: %v", err)
	}
	if _, _, err := s.Bootstrap(ctx, token2, "second", "long-enough-password"); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second bootstrap: want ErrAlreadyInitialized, got %v", err)
	}
}

// TestBootstrapConcurrentTokensSingleAdmin: N concurrent valid tokens must
// yield exactly one admin; every other attempt fails with a business error.
func TestBootstrapConcurrentTokensSingleAdmin(t *testing.T) {
	s := newTestAuthStore(t)
	ctx := context.Background()

	const n = 6
	tokens := make([]string, n)
	for i := range tokens {
		tk, err := s.CreateBootstrapToken(time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		tokens[i] = tk
	}
	var wg sync.WaitGroup
	results := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, results[i] = s.Bootstrap(ctx, tokens[i], "admin", "long-enough-password")
		}(i)
	}
	wg.Wait()
	winners := 0
	for i, err := range results {
		if err == nil {
			winners++
			continue
		}
		// Losers get a business error; the global KDF budget may also
		// deliberately reject concurrent attempts (ErrKDFBusy) — that is the
		// P0-02 protection working, never a second admin.
		if !errors.Is(err, ErrAlreadyInitialized) && !errors.Is(err, ErrInvalidToken) && !errors.Is(err, ErrKDFBusy) {
			t.Fatalf("attempt %d: unexpected error %v", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("want exactly 1 winner, got %d", winners)
	}
	if has, _ := s.HasUser(ctx); !has {
		t.Fatal("admin must exist")
	}
}

func TestBootstrapRejectsBadTokens(t *testing.T) {
	s := newTestAuthStore(t)
	ctx := context.Background()

	if _, _, err := s.Bootstrap(ctx, "", "admin", "long-enough-password"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("empty token: want ErrInvalidToken, got %v", err)
	}
	if _, _, err := s.Bootstrap(ctx, "forged-token", "admin", "long-enough-password"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("forged token: want ErrInvalidToken, got %v", err)
	}
	if _, _, err := s.Bootstrap(ctx, strings.Repeat("x", maxTokenLen+1), "admin", "long-enough-password"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("oversized token: want ErrInvalidToken, got %v", err)
	}

	token, err := s.CreateBootstrapToken(-time.Minute) // already expired
	if err != nil {
		t.Fatalf("create expired token: %v", err)
	}
	if _, _, err := s.Bootstrap(ctx, token, "admin", "long-enough-password"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token: want ErrInvalidToken, got %v", err)
	}
	if has, _ := s.HasUser(ctx); has {
		t.Fatal("failed bootstrap must not create a user")
	}
}

func TestIssueSessionAndResetPassword(t *testing.T) {
	s := newTestAuthStore(t)
	ctx := context.Background()

	token, _ := s.CreateBootstrapToken(time.Minute)
	if _, _, err := s.Bootstrap(ctx, token, "admin", "original-password-1"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	if _, _, err := s.IssueSession(ctx, "nobody", "original-password-1", time.Hour); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("unknown user must be ErrBadCredentials, got %v", err)
	}
	if _, _, err := s.IssueSession(ctx, "admin", "wrong-password-99", time.Hour); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong password must be ErrBadCredentials, got %v", err)
	}
	u, raw, err := s.IssueSession(ctx, "admin", "original-password-1", time.Hour)
	if err != nil {
		t.Fatalf("valid login: %v", err)
	}
	if got, _ := s.UserForSession(ctx, raw); got == nil || got.ID != u.ID {
		t.Fatal("issued session must resolve to the user")
	}

	newPass, err := s.ResetPassword(ctx, "admin")
	if err != nil {
		t.Fatalf("reset password: %v", err)
	}
	if _, _, err := s.IssueSession(ctx, "admin", "original-password-1", time.Hour); !errors.Is(err, ErrBadCredentials) {
		t.Fatal("old password must stop working after reset")
	}
	if got, _ := s.UserForSession(ctx, raw); got != nil {
		t.Fatal("reset password must revoke existing sessions")
	}
	if _, _, err := s.IssueSession(ctx, "admin", newPass, time.Hour); err != nil {
		t.Fatalf("login with the new password must work: %v", err)
	}
	if _, err := s.ResetPassword(ctx, "ghost"); err == nil {
		t.Fatal("reset of unknown user must fail")
	}
}

// TestResetPasswordInvalidatesInFlightGeneration pins the mechanism behind
// review P1-03: a session issued against a stale auth_generation cannot insert.
func TestResetPasswordInvalidatesInFlightGeneration(t *testing.T) {
	s := newTestAuthStore(t)
	ctx := context.Background()
	token, _ := s.CreateBootstrapToken(time.Minute)
	if _, _, err := s.Bootstrap(ctx, token, "admin", "original-password-1"); err != nil {
		t.Fatal(err)
	}

	// Simulate the in-flight window: read generation (as IssueSession does
	// before the KDF), reset the password, then attempt the conditional
	// insert with the stale generation — it must match zero rows.
	var generation, userID int64
	if err := s.db.QueryRow(
		`SELECT id, auth_generation FROM users WHERE username = 'admin'`).Scan(&userID, &generation); err != nil {
		t.Fatal(err)
	}
	newPass, err := s.ResetPassword(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id_hash, user_id, expires_at, created_at)
		 SELECT 'stale-hash', id, strftime('%s','now')+3600, strftime('%s','now')
		 FROM users WHERE id = ? AND auth_generation = ?`, userID, generation)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Fatal("stale generation must not be able to insert a session")
	}
	if _, _, err := s.IssueSession(ctx, "admin", newPass, time.Hour); err != nil {
		t.Fatalf("login with the new password must work: %v", err)
	}
}

func TestPurgeExpiredAndExpiryWithoutPurge(t *testing.T) {
	s := newTestAuthStore(t)
	ctx := context.Background()

	token, _ := s.CreateBootstrapToken(time.Minute)
	_, _, err := s.Bootstrap(ctx, token, "admin", "long-enough-password")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	var userID int64
	if err := s.db.QueryRow(`SELECT id FROM users WHERE username='admin'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	raw := s.insertExpiredSession(t, userID)
	if u, _ := s.UserForSession(ctx, raw); u != nil {
		t.Fatal("expired session must be rejected without purge")
	}
	if err := s.PurgeExpired(ctx); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if u, _ := s.UserForSession(ctx, raw); u != nil {
		t.Fatal("expired session must be gone after purge")
	}
}

func TestSessionOnlyStoresHash(t *testing.T) {
	s := newTestAuthStore(t)
	ctx := context.Background()
	token, _ := s.CreateBootstrapToken(time.Minute)
	if _, _, err := s.Bootstrap(ctx, token, "admin", "long-enough-password"); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("want exactly one stored session hash, got %d", rows)
	}
}

func TestPasswordHashRoundtrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=2,p=1$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("correct password must verify")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Fatal("wrong password must not verify")
	}
	h2, _ := HashPassword("correct horse battery staple")
	if h2 == hash {
		t.Fatal("two hashes of the same password must differ (random salt)")
	}
}

// TestVerifyPasswordStrictProfile (review P2-02): only the exact supported
// PHC profile passes; tampered versions/parameters fail closed without KDF.
func TestVerifyPasswordStrictProfile(t *testing.T) {
	hash, _ := HashPassword("some password")
	parts := strings.Split(hash, "$")

	tamper := func(idx int, old, new string) string {
		p := append([]string(nil), parts...)
		if !strings.Contains(p[idx], old) {
			t.Fatalf("fixture error: part %d %q lacks %q", idx, p[idx], old)
		}
		p[idx] = strings.Replace(p[idx], old, new, 1)
		return strings.Join(p, "$")
	}
	if VerifyPassword(tamper(2, "v=19", "v=999"), "some password") {
		t.Fatal("unsupported version must fail closed")
	}
	if VerifyPassword(tamper(3, "t=2", "t=0"), "some password") {
		t.Fatal("t=0 must be rejected (would panic in the KDF)")
	}
	if VerifyPassword(tamper(3, "m=65536", "m=99999999"), "some password") {
		t.Fatal("excessive memory parameter must be rejected")
	}
	if VerifyPassword(tamper(3, "p=1", "p=99"), "some password") {
		t.Fatal("excessive parallelism must be rejected")
	}
	for _, bad := range []string{
		"",
		"$bcrypt$abc",
		"argon2id$v=19$m=65536,t=2,p=1$aaaa$bbbb",
		"$argon2id$v=19$m=65536,t=2,p=1$not-base64!!$also-bad",
	} {
		if VerifyPassword(bad, "anything") {
			t.Fatalf("malformed hash must not verify: %q", bad)
		}
	}
}

func TestCredentialValidation(t *testing.T) {
	if err := ValidateUsername("ab"); err == nil {
		t.Error("too-short username must fail")
	}
	if err := ValidateUsername("has space"); err == nil {
		t.Error("username with space must fail")
	}
	if err := ValidateUsername("admin-01"); err != nil {
		t.Errorf("valid username rejected: %v", err)
	}
	if err := ValidatePassword("short"); err == nil {
		t.Error("short password must fail")
	}
	if err := ValidatePassword("long-enough-password"); err != nil {
		t.Errorf("valid password rejected: %v", err)
	}
	if err := ValidatePassword(strings.Repeat("x", passwordMax+1)); err == nil {
		t.Error("oversized password must fail")
	}
}
