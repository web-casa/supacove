package auth

import (
	"context"
	"errors"
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
	u, err := s.Bootstrap(ctx, token, "admin", "long-enough-password")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if u.Username != "admin" {
		t.Fatalf("unexpected user %+v", u)
	}

	// Even a fresh, unused token must not allow a second admin.
	token2, err := s.CreateBootstrapToken(time.Minute)
	if err != nil {
		t.Fatalf("create token2: %v", err)
	}
	if _, err := s.Bootstrap(ctx, token2, "second", "long-enough-password"); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second bootstrap: want ErrAlreadyInitialized, got %v", err)
	}
}

func TestBootstrapRejectsBadTokens(t *testing.T) {
	s := newTestAuthStore(t)
	ctx := context.Background()

	if _, err := s.Bootstrap(ctx, "", "admin", "long-enough-password"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("empty token: want ErrInvalidToken, got %v", err)
	}
	if _, err := s.Bootstrap(ctx, "forged-token", "admin", "long-enough-password"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("forged token: want ErrInvalidToken, got %v", err)
	}

	token, err := s.CreateBootstrapToken(-time.Minute) // already expired
	if err != nil {
		t.Fatalf("create expired token: %v", err)
	}
	if _, err := s.Bootstrap(ctx, token, "admin", "long-enough-password"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token: want ErrInvalidToken, got %v", err)
	}
}

func TestLoginAndResetPassword(t *testing.T) {
	s := newTestAuthStore(t)
	ctx := context.Background()

	token, _ := s.CreateBootstrapToken(time.Minute)
	if _, err := s.Bootstrap(ctx, token, "admin", "original-password-1"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	if _, err := s.Login(ctx, "nobody", "original-password-1"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("unknown user must be ErrBadCredentials, got %v", err)
	}
	if _, err := s.Login(ctx, "admin", "wrong-password-99"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong password must be ErrBadCredentials, got %v", err)
	}
	if _, err := s.Login(ctx, "admin", "original-password-1"); err != nil {
		t.Fatalf("valid login: %v", err)
	}

	raw, err := s.CreateSession(ctx, 1, time.Hour)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if u, err := s.UserForSession(ctx, raw); err != nil || u == nil {
		t.Fatalf("session lookup: user=%v err=%v", u, err)
	}

	newPass, err := s.ResetPassword(ctx, "admin")
	if err != nil {
		t.Fatalf("reset password: %v", err)
	}
	if _, err := s.Login(ctx, "admin", "original-password-1"); !errors.Is(err, ErrBadCredentials) {
		t.Fatal("old password must stop working after reset")
	}
	if _, err := s.Login(ctx, "admin", newPass); err != nil {
		t.Fatalf("new password login: %v", err)
	}
	if u, _ := s.UserForSession(ctx, raw); u != nil {
		t.Fatal("reset password must revoke existing sessions")
	}
}

func TestPurgeExpired(t *testing.T) {
	s := newTestAuthStore(t)
	ctx := context.Background()

	token, _ := s.CreateBootstrapToken(time.Minute)
	if _, err := s.Bootstrap(ctx, token, "admin", "long-enough-password"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	raw, err := s.CreateSession(ctx, 1, -time.Second) // already expired
	if err != nil {
		t.Fatalf("create expired session: %v", err)
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
	u, err := s.Bootstrap(ctx, token, "admin", "long-enough-password")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	raw, err := s.CreateSession(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// The raw id must not appear anywhere in the sessions table.
	var count int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE id_hash = ?`, raw).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("raw session id must not be stored; only its hash")
	}
}
