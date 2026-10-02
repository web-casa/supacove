// Package auth implements the P1 access layer: single-admin bootstrap via
// one-time CLI-issued tokens, Argon2id password hashing, server-side sessions,
// and CSRF tokens bound to sessions. The public first visitor can never become
// the administrator: bootstrap requires a token printed by the local CLI
// (dev-plan P1-06).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// User is an account row.
type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	CreatedAt int64  `json:"createdAt"`
}

var (
	// ErrAlreadyInitialized means an admin user already exists.
	ErrAlreadyInitialized = errors.New("already initialized: admin user exists")
	// ErrInvalidToken means the bootstrap token is unknown, used, or expired.
	ErrInvalidToken = errors.New("invalid or expired bootstrap token")
	// ErrBadCredentials means wrong username or password (also used when a
	// session could not be issued because the password changed mid-flight —
	// deliberately indistinguishable from a wrong password).
	ErrBadCredentials = errors.New("invalid username or password")
	// ErrKDFBusy means the global Argon2id concurrency budget is exhausted.
	ErrKDFBusy = errors.New("password hashing busy, try again")
)

// Store persists users, bootstrap tokens and sessions.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CreateBootstrapToken mints a one-time token, stores only its hash, and
// returns the raw token for one-time display by the local CLI.
func (s *Store) CreateBootstrapToken(ttl time.Duration) (string, error) {
	raw, err := randomToken(32)
	if err != nil {
		return "", err
	}
	now := time.Now()
	_, err = s.db.ExecContext(context.Background(),
		`INSERT INTO bootstrap_tokens (token_hash, expires_at, created_at) VALUES (?, ?, ?)`,
		hashToken(raw), now.Add(ttl).Unix(), now.Unix())
	if err != nil {
		return "", err
	}
	return raw, nil
}

// HasUser reports whether any account exists.
func (s *Store) HasUser(ctx context.Context) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// Bootstrap validates a one-time token and creates the initial admin plus
// first session atomically. Expensive work ordering (review P0-02): cheap
// validation → cheap token existence check → Argon2id OUTSIDE the transaction →
// transactional re-check + token consumption + user insert + session insert.
// A second bootstrap fails with ErrAlreadyInitialized even with a second valid
// token. The returned session value (empty string on error) must be turned
// into cookies by the caller.
func (s *Store) Bootstrap(ctx context.Context, token, username, password string) (*User, string, error) {
	if err := ValidateUsername(username); err != nil {
		return nil, "", err
	}
	if err := ValidatePassword(password); err != nil {
		return nil, "", err
	}
	if len(token) == 0 || len(token) > maxTokenLen {
		return nil, "", ErrInvalidToken
	}

	// Cheap rejections before any KDF work (still re-checked in the tx below).
	if has, err := s.HasUser(ctx); err != nil {
		return nil, "", err
	} else if has {
		return nil, "", ErrAlreadyInitialized
	}
	now := time.Now().Unix()
	tokenHash := hashToken(token)
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM bootstrap_tokens WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		tokenHash, now).Scan(&n); err != nil {
		return nil, "", err
	}
	if n == 0 {
		return nil, "", ErrInvalidToken
	}

	// KDF outside any transaction; bounded by the global semaphore.
	release, err := acquireKDF(ctx)
	if err != nil {
		return nil, "", err
	}
	hash, err := HashPassword(password)
	release()
	if err != nil {
		return nil, "", err
	}

	sessionRaw, err := randomToken(32)
	if err != nil {
		return nil, "", err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()

	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return nil, "", err
	}
	if n > 0 {
		return nil, "", ErrAlreadyInitialized
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE bootstrap_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		now, tokenHash, now)
	if err != nil {
		return nil, "", err
	}
	if used, _ := res.RowsAffected(); used != 1 {
		return nil, "", ErrInvalidToken
	}
	var id int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?) RETURNING id`,
		username, hash, now).Scan(&id); err != nil {
		return nil, "", err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions (id_hash, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		hashToken(sessionRaw), id, now+int64(defaultSessionTTL/time.Second), now); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}
	return &User{ID: id, Username: username, CreatedAt: now}, sessionRaw, nil
}

// defaultSessionTTL is used inside Bootstrap where the HTTP layer's TTL is not
// available; the handler treats it as authoritative for cookie MaxAge too.
const defaultSessionTTL = 7 * 24 * time.Hour

// SessionTTL reports the fixed session lifetime used by the store.
func SessionTTL() time.Duration { return defaultSessionTTL }

// IssueSession verifies credentials and, on success, inserts a session whose
// validity is conditioned on the auth_generation observed before the KDF
// (review P1-03): if a password reset commits in between, the conditional
// insert matches zero rows and the caller gets ErrBadCredentials — no session
// survives a completed reset.
func (s *Store) IssueSession(ctx context.Context, username, password string, ttl time.Duration) (*User, string, error) {
	if username == "" || password == "" {
		return nil, "", ErrBadCredentials
	}
	var u User
	var hash string
	var generation int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, auth_generation, created_at FROM users WHERE username = ?`,
		username).Scan(&u.ID, &u.Username, &hash, &generation, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		// Burn comparable time to reduce a username oracle.
		release, kerr := acquireKDF(ctx)
		if kerr == nil {
			_, _ = HashPassword(password)
			release()
		}
		return nil, "", ErrBadCredentials
	}
	if err != nil {
		return nil, "", err
	}

	release, err := acquireKDF(ctx)
	if err != nil {
		return nil, "", err
	}
	ok := VerifyPassword(hash, password)
	release()
	if !ok {
		return nil, "", ErrBadCredentials
	}

	sessionRaw, err := randomToken(32)
	if err != nil {
		return nil, "", err
	}
	now := time.Now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id_hash, user_id, expires_at, created_at)
		 SELECT ?, id, ?, ? FROM users WHERE id = ? AND auth_generation = ?`,
		hashToken(sessionRaw), now.Add(ttl).Unix(), now.Unix(), u.ID, generation)
	if err != nil {
		return nil, "", err
	}
	if inserted, _ := res.RowsAffected(); inserted != 1 {
		// The password changed between the hash read and this insert: treat
		// exactly like bad credentials.
		return nil, "", ErrBadCredentials
	}
	return &u, sessionRaw, nil
}

// ResetPassword replaces a user's password, bumps auth_generation, and deletes
// all their sessions in one transaction. Intended for the local CLI.
func (s *Store) ResetPassword(ctx context.Context, username string) (string, error) {
	newPass, err := randomToken(18) // ~144 bits, URL-safe printable
	if err != nil {
		return "", err
	}
	release, err := acquireKDF(ctx)
	if err != nil {
		return "", err
	}
	hash, err := HashPassword(newPass)
	release()
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var userID int64
	err = tx.QueryRowContext(ctx,
		`UPDATE users SET password_hash = ?, auth_generation = auth_generation + 1
		 WHERE username = ? RETURNING id`, hash, username).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("no such user %q", username)
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return newPass, nil
}

// UserForSession resolves a raw session cookie value: (user, nil) valid,
// (nil, nil) invalid/expired session, (nil, err) storage failure — callers
// must not report 401 for a storage failure (review P1-09).
func (s *Store) UserForSession(ctx context.Context, raw string) (*User, error) {
	if raw == "" {
		return nil, nil
	}
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.username, u.created_at FROM sessions s
		 JOIN users u ON u.id = s.user_id
		 WHERE s.id_hash = ? AND s.expires_at > ?`,
		hashToken(raw), time.Now().Unix()).Scan(&u.ID, &u.Username, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// DeleteSession removes one session by raw id (logout).
func (s *Store) DeleteSession(ctx context.Context, raw string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, hashToken(raw))
	return err
}

// PurgeExpired removes expired sessions and bootstrap tokens; called on a
// timer by the server.
func (s *Store) PurgeExpired(ctx context.Context) error {
	now := time.Now().Unix()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM bootstrap_tokens WHERE expires_at <= ?`, now)
	return err
}

const (
	usernameMin = 3
	usernameMax = 64
	passwordMin = 12
	passwordMax = 128
	maxTokenLen = 128
)

func ValidateUsername(u string) error {
	if len(u) < usernameMin || len(u) > usernameMax {
		return fmt.Errorf("username must be %d-%d characters", usernameMin, usernameMax)
	}
	for _, r := range u {
		ok := r == '-' || r == '_' || r == '.' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return errors.New("username may contain letters, digits, dot, dash, underscore only")
		}
	}
	return nil
}

func ValidatePassword(p string) error {
	if len(p) < passwordMin {
		return fmt.Errorf("password must be at least %d characters", passwordMin)
	}
	if len(p) > passwordMax {
		return fmt.Errorf("password must be at most %d characters", passwordMax)
	}
	return nil
}

// SecureEqual is a constant-time string comparison for secrets.
func SecureEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
