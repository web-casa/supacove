// Package auth implements the P1 access layer: single-admin bootstrap via
// one-time CLI-issued tokens, Argon2id password hashing, server-side sessions,
// and double-submit CSRF. The public first visitor can never become the
// administrator: bootstrap requires a token printed by the local CLI
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
	// ErrBadCredentials means wrong username or password.
	ErrBadCredentials = errors.New("invalid username or password")
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

// Bootstrap validates a one-time token and creates the initial admin atomically.
// A second bootstrap attempt fails with ErrAlreadyInitialized even with a
// second valid token.
func (s *Store) Bootstrap(ctx context.Context, token, username, password string) (*User, error) {
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, ErrAlreadyInitialized
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE bootstrap_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		now, hashToken(token), now)
	if err != nil {
		return nil, err
	}
	if used, _ := res.RowsAffected(); used != 1 {
		return nil, ErrInvalidToken
	}
	var id int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?) RETURNING id`,
		username, hash, now).Scan(&id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &User{ID: id, Username: username, CreatedAt: now}, nil
}

// ResetPassword replaces a user's password with a generated one and revokes
// all their sessions. Intended for the local CLI (`supabackup reset-password`).
func (s *Store) ResetPassword(ctx context.Context, username string) (string, error) {
	newPass, err := randomToken(18) // ~144 bits, URL-safe printable
	if err != nil {
		return "", err
	}
	hash, err := HashPassword(newPass)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE username = ?`, hash, username)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", fmt.Errorf("no such user %q", username)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM sessions WHERE user_id = (SELECT id FROM users WHERE username = ?)`, username); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return newPass, nil
}

// Login verifies credentials. Failures must be indistinguishable for unknown
// user vs wrong password.
func (s *Store) Login(ctx context.Context, username, password string) (*User, error) {
	var u User
	var hash string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, created_at FROM users WHERE username = ?`,
		username).Scan(&u.ID, &u.Username, &hash, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		// Burn comparable time to reduce a username oracle.
		_, _ = HashPassword(password)
		return nil, ErrBadCredentials
	}
	if err != nil {
		return nil, err
	}
	if !VerifyPassword(hash, password) {
		return nil, ErrBadCredentials
	}
	return &u, nil
}

// CreateSession stores a session and returns the raw cookie value. Only the
// SHA-256 of the session id is persisted.
func (s *Store) CreateSession(ctx context.Context, userID int64, ttl time.Duration) (string, error) {
	raw, err := randomToken(32)
	if err != nil {
		return "", err
	}
	now := time.Now()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id_hash, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		hashToken(raw), userID, now.Add(ttl).Unix(), now.Unix()); err != nil {
		return "", err
	}
	return raw, nil
}

// UserForSession resolves a raw session cookie value to its user, or nil.
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
	return nil
}

// SecureEqual is a constant-time string comparison for secrets.
func SecureEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
