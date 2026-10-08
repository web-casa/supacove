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
	// LocalKeep is how many local staged artifacts per database are kept
	// (protocol D local half; newest always protected).
	LocalKeep int
	// StagingQuotaBytes is the hard per-run cap on ciphertext written to the
	// staging area (0 = only the filesystem free-space floor applies).
	StagingQuotaBytes int64
	// PublicOrigin is the exact external origin (scheme://host[:port]) users
	// browse. When set, Origin headers must equal it strictly; when empty the
	// request's own scheme+host is used (direct, non-proxied deployments).
	PublicOrigin string
	// TrustedProxies is a comma-separated CIDR list allowed to set
	// X-Forwarded-For. Empty means no proxy is trusted and client IPs are
	// taken from the socket address only (review P0-01).
	TrustedProxies string
	// VerifyEnabled turns on automatic restore verification after every
	// successful backup (ADR-004). It is OFF by default and must be enabled
	// deliberately by the administrator: verification restores the dump in a
	// same-UID throwaway PostgreSQL instance (NOT a sandbox) and requires
	// the age identity to be available to this instance.
	VerifyEnabled bool
	// VerifyIdentityFile is the path to the offline age identity file used
	// to decrypt artifacts for verification. Required when VerifyEnabled.
	// Providing the identity to the instance is an explicit trust decision.
	VerifyIdentityFile string
	// VerifyPGBin overrides the PostgreSQL server binary directory used by
	// the embedded verifier. Empty auto-detects /usr/lib/postgresql/<maj>/bin.
	VerifyPGBin string
	// JobTimeout is the wall-clock budget for one backup job — dump, upload
	// and read-back (restore verification carries its own). Global
	// concurrency is 1, so a single hung job would stall every database's
	// backups; the budget turns a hang into a classified failure. 0
	// disables the budget.
	JobTimeout time.Duration
	// FailedArtifactTTLHours is how long a failed/canceled/interrupted
	// job's committed staging artifact is kept before reclamation (nothing
	// ever re-reads it; the window is protocol D's grace for manual
	// recovery). 0 keeps artifacts forever.
	FailedArtifactTTLHours int
	// Version metadata, wired at build time.
	Version, Commit, BuildDate string
}

// Load reads configuration from the environment with defaults.
func Load() (*Config, error) {
	localKeep := 5
	if v := os.Getenv("SB_LOCAL_KEEP"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("invalid SB_LOCAL_KEEP %q: must be a positive integer", v)
		}
		localKeep = n
	}
	quota := int64(0)
	if v := os.Getenv("SB_STAGING_QUOTA_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid SB_STAGING_QUOTA_BYTES %q: must be a non-negative integer", v)
		}
		quota = n
	}
	jobTimeout := 6 * time.Hour
	if v := os.Getenv("SB_JOB_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return nil, fmt.Errorf("invalid SB_JOB_TIMEOUT %q: must be a duration like 6h or 30m, >= 0", v)
		}
		jobTimeout = d
	}
	failedTTLHours := 72
	if v := os.Getenv("SB_FAILED_ARTIFACT_TTL_HOURS"); v != "" {
		n, err := strconv.Atoi(v)
		// Upper bound guards Duration overflow: an absurd value would wrap
		// time.Duration(n)*time.Hour negative or into minutes, silently
		// turning "keep a long time" into aggressive reclamation
		// (fresh-review P3-02). 100 years is far beyond any real intent.
		if err != nil || n < 0 || n > 24*365*100 {
			return nil, fmt.Errorf("invalid SB_FAILED_ARTIFACT_TTL_HOURS %q: must be an integer in [0, %d]", v, 24*365*100)
		}
		failedTTLHours = n
	}
	c := &Config{
		DataDir:                envOr("SB_DATA_DIR", "./data"),
		LocalKeep:              localKeep,
		StagingQuotaBytes:      quota,
		JobTimeout:             jobTimeout,
		FailedArtifactTTLHours: failedTTLHours,
		Addr:                   envOr("SB_ADDR", ":8080"),
		SecretFile:             os.Getenv("SB_SECRET_FILE"),
		InsecureCookie:         envBool("SB_INSECURE_COOKIE"),
		PublicOrigin:           os.Getenv("SB_PUBLIC_ORIGIN"),
		TrustedProxies:         os.Getenv("SB_TRUSTED_PROXIES"),
		VerifyEnabled:          envBool("SB_VERIFY_ENABLED"),
		VerifyIdentityFile:     os.Getenv("SB_VERIFY_IDENTITY_FILE"),
		VerifyPGBin:            os.Getenv("SB_VERIFY_PGBIN"),
		BootstrapTokenTTL:      15 * time.Minute,
		SessionTTL:             7 * 24 * time.Hour,
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
	// ADR-004 enablement gate: verification without the identity would be a
	// silent no-op, so an inconsistent enablement is a configuration error,
	// not a warning.
	if c.VerifyEnabled {
		if c.VerifyIdentityFile == "" {
			return nil, errors.New("SB_VERIFY_ENABLED=1 requires SB_VERIFY_IDENTITY_FILE (the offline age identity); verification cannot decrypt artifacts without it")
		}
		st, err := os.Lstat(c.VerifyIdentityFile)
		if err != nil {
			return nil, fmt.Errorf("SB_VERIFY_IDENTITY_FILE %s: %w", c.VerifyIdentityFile, err)
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("SB_VERIFY_IDENTITY_FILE %s is a symlink; refusing", c.VerifyIdentityFile)
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("SB_VERIFY_IDENTITY_FILE %s is not a regular file", c.VerifyIdentityFile)
		}
		if st.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("SB_VERIFY_IDENTITY_FILE %s has permissive mode %04o; want 0600", c.VerifyIdentityFile, st.Mode().Perm())
		}
	}
	return c, nil
}

// TrustedProxyCIDRs parses SB_TRUSTED_PROXIES into CIDR ranges.
func (c *Config) TrustedProxyCIDRs() ([]*net.IPNet, error) {
	if c.TrustedProxies == "" {
		return nil, nil
	}
	var out []*net.IPNet
	for part := range strings.SplitSeq(c.TrustedProxies, ",") {
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
		// A read back from an UNVERIFIED directory must not be reported as
		// durably persisted: confirm the directory is syncable or fail
		// (review round 4, P1-04 remainder).
		if err := syncDir(filepath.Dir(path)); err != nil {
			return nil, err
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
	defer func() { _ = os.Remove(tmpName) }() // no-op once linked and removed below
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
	_ = os.Remove(tmpName) // cleanup on a path whose outcome cannot change the result (errcheck)
	if err := syncDir(dir); err != nil {
		return nil, err
	}
	return key, nil
}

// syncDir fsyncs a directory so a linked/renamed entry survives a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open secret dir for fsync: %w", err)
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return fmt.Errorf("fsync secret dir: %w", err)
	}
	if err := d.Close(); err != nil {
		return err
	}
	return nil
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
