// Package config loads runtime configuration from the environment.
//
// All defaults are safe for local development; the container image sets
// explicit values (see deploy/Dockerfile).
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the resolved runtime configuration.
type Config struct {
	// DataDir holds supabackup.db, the advisory lock file and pre-migration backups.
	DataDir string
	// Addr is the HTTP listen address.
	Addr string
	// SecretFile is the path of the 32-byte application master secret
	// (AES-GCM key material for credentials stored in SQLite; see dev-plan §0.5 protocol B).
	SecretFile string
	// InsecureCookie allows session cookies without the Secure attribute.
	// Only for plain-HTTP local development; never enable in production.
	InsecureCookie bool
	// BootstrapTokenTTL is how long a CLI-generated bootstrap token stays valid.
	BootstrapTokenTTL time.Duration
	// SessionTTL is the fixed session lifetime.
	SessionTTL time.Duration
	// Version metadata, wired at build time.
	Version, Commit, BuildDate string
}

// Load reads configuration from the environment with defaults.
func Load() (*Config, error) {
	c := &Config{
		DataDir:           envOr("SB_DATA_DIR", "./data"),
		Addr:              envOr("SB_ADDR", ":8080"),
		SecretFile:        os.Getenv("SB_SECRET_FILE"),
		InsecureCookie:    envBool("SB_INSECURE_COOKIE"),
		BootstrapTokenTTL: 15 * time.Minute,
		SessionTTL:        7 * 24 * time.Hour,
	}
	c.DataDir = filepath.Clean(c.DataDir)
	if c.SecretFile == "" {
		c.SecretFile = filepath.Join(c.DataDir, "secret.key")
	}
	c.SecretFile = filepath.Clean(c.SecretFile)
	if c.Addr == "" || strings.HasPrefix(c.Addr, "-") {
		return nil, fmt.Errorf("invalid SB_ADDR %q", c.Addr)
	}
	return c, nil
}

// LoadOrCreateSecret returns the 32-byte master secret, creating a random one
// on first run with 0600 permissions. Losing this file makes stored encrypted
// credentials unreadable (recorded credentials must be re-entered); it never
// affects the recoverability of backup files (see protocol B).
func LoadOrCreateSecret(path string) ([]byte, error) {
	if st, err := os.Stat(path); err == nil {
		if st.IsDir() {
			return nil, fmt.Errorf("secret path %s is a directory", path)
		}
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		key, err = decodeKey(key)
		if err != nil {
			return nil, fmt.Errorf("secret file %s: %w", path, err)
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// Hex-encoded for human inspectability; permissions are the security boundary.
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func decodeKey(raw []byte) ([]byte, error) {
	s := strings.TrimSpace(string(raw))
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		return nil, errors.New("must be 64 hex chars encoding a 32-byte key")
	}
	return b, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	b, _ := strconv.ParseBool(os.Getenv(key))
	return b
}
