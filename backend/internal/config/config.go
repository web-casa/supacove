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
	"net"
	"net/url"
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
	// PublicOrigin is the exact external origin (scheme://host[:port]) users
	// browse. When set, Origin headers must equal it strictly; when empty the
	// request's own scheme+host is used (direct, non-proxied deployments).
	PublicOrigin string
	// TrustedProxies is a comma-separated CIDR list allowed to set
	// X-Forwarded-For. Empty means no proxy is trusted and client IPs are
	// taken from the socket address only (review P0-01).
	TrustedProxies string
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
		PublicOrigin:      os.Getenv("SB_PUBLIC_ORIGIN"),
		TrustedProxies:    os.Getenv("SB_TRUSTED_PROXIES"),
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
	if c.PublicOrigin != "" {
		u, err := url.Parse(c.PublicOrigin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return nil, fmt.Errorf("invalid SB_PUBLIC_ORIGIN %q: must be scheme://host[:port] only", c.PublicOrigin)
		}
	}
	if _, err := c.TrustedProxyCIDRs(); err != nil {
		return nil, err
	}
	return c, nil
}

// TrustedProxyCIDRs parses SB_TRUSTED_PROXIES into CIDR ranges.
func (c *Config) TrustedProxyCIDRs() ([]*net.IPNet, error) {
	if c.TrustedProxies == "" {
		return nil, nil
	}
	var out []*net.IPNet
	for _, part := range strings.Split(c.TrustedProxies, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		_, cidr, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("invalid SB_TRUSTED_PROXIES entry %q: %w", part, err)
		}
		out = append(out, cidr)
	}
	return out, nil
}

// LoadOrCreateSecret returns the 32-byte master secret, creating a random one
// on first run. Creation is atomic (O_EXCL + fsync) so concurrent first
// starts converge on exactly one key; an existing file must be a regular file
// with 0600 permissions. Losing this file makes stored encrypted credentials
// unreadable (recorded credentials must be re-entered); it never affects the
// recoverability of backup files (protocol B).
func LoadOrCreateSecret(path string) ([]byte, error) {
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("secret path %s is a symlink; refusing", path)
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("secret path %s is not a regular file", path)
		}
		if st.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("secret file %s has permissive mode %04o; want 0600", path, st.Mode().Perm())
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
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// Hex-encoded for human inspectability; permissions are the security
	// boundary. The key is written COMPLETELY to a temp file, fsynced, then
	// published with link(2) — atomic and never overwriting — so a concurrent
	// reader can never observe a half-written secret and a crash cannot leave
	// an invalid final file (review round 2, P1-04 remainder).
	tmp, err := os.CreateTemp(dir, ".secret-*.tmp")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once linked and removed below
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write([]byte(hex.EncodeToString(key))); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Link(tmpName, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			// Someone else won the race; use their key.
			return LoadOrCreateSecret(path)
		}
		return nil, err
	}
	os.Remove(tmpName)
	// Sync the directory so the link survives a crash; a failure here means
	// the secret may not persist, which must not be reported as success
	// (review round 3, P1-04 remainder).
	d, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("open secret dir for fsync: %w", err)
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return nil, fmt.Errorf("fsync secret dir: %w", err)
	}
	if err := d.Close(); err != nil {
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
