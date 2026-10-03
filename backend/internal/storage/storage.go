// Package storage implements the BYOS object-storage layer (dev-plan
// Phase 3, protocol C): S3-compatible backends (AWS S3, Cloudflare R2,
// Backblaze B2 — all via their S3 APIs) with explicit multipart handling,
// read-back verification support and paginated listing.
//
// Design notes:
//   - Multipart upload is implemented directly on the S3 API so that abort
//     on failure is explicit (protocol C: "multipart 遗留 abort").
//   - Verification is a full streamed read-back (protocol C.1): multipart
//     ETags are not content hashes, so a HEAD/ETag check proves nothing.
//   - The interface is deliberately small so tests can fake it.
package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// Platforms with configuration presets.
const (
	PlatformS3 = "s3"
	PlatformR2 = "r2"
	PlatformB2 = "b2"
)

var Platforms = map[string]bool{PlatformS3: true, PlatformR2: true, PlatformB2: true}

// Config is the validated, credential-carrying configuration for one
// destination. SecretKey must never be logged.
type Config struct {
	Platform  string
	Endpoint  string // required for r2/b2; empty = AWS S3
	Region    string
	Bucket    string
	Prefix    string // normalized with a trailing slash; may be empty
	AccessKey string
	SecretKey string
}

var (
	bucketRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	prefixRe   = regexp.MustCompile(`^[A-Za-z0-9/_.\-]*$`)
	endpointRe = regexp.MustCompile(`^https?://[A-Za-z0-9.\-]+(:[0-9]{1,5})?$`)
)

// Validate normalizes and validates the configuration.
func (c *Config) Validate() error {
	if !Platforms[c.Platform] {
		return fmt.Errorf("platform must be one of s3, r2, b2")
	}
	if !bucketRe.MatchString(c.Bucket) {
		return errors.New("bucket name is not a valid S3 bucket name (3-63 chars, lowercase letters/digits/dot/dash)")
	}
	if c.Platform == PlatformR2 || c.Platform == PlatformB2 {
		if !endpointRe.MatchString(strings.TrimRight(c.Endpoint, "/")) {
			return errors.New("endpoint is required for r2/b2 (https://host[:port])")
		}
		c.Endpoint = strings.TrimRight(c.Endpoint, "/")
	} else if c.Endpoint != "" {
		if !endpointRe.MatchString(strings.TrimRight(c.Endpoint, "/")) {
			return errors.New("endpoint must be http(s)://host[:port] when set")
		}
		c.Endpoint = strings.TrimRight(c.Endpoint, "/")
	}
	switch {
	case c.Platform == PlatformR2:
		if c.Region == "" {
			c.Region = "auto"
		}
	case c.Platform == PlatformS3 && c.Region == "":
		c.Region = "us-east-1"
	case c.Platform == PlatformB2 && c.Region == "":
		return errors.New("region is required for b2 (see the B2 S3 endpoint's region)")
	}
	if c.AccessKey == "" || c.SecretKey == "" {
		return errors.New("access key and secret key are required")
	}
	if strings.ContainsAny(c.Prefix, "\x00") || strings.Contains(c.Prefix, "..") {
		return errors.New("prefix must not contain NUL or '..'")
	}
	if c.Prefix != "" && !prefixRe.MatchString(c.Prefix) {
		return errors.New("prefix may contain letters, digits, slash, dot, underscore, dash only")
	}
	c.Prefix = strings.Trim(c.Prefix, "/")
	if c.Prefix != "" {
		c.Prefix += "/"
	}
	return nil
}

// Object is one remote object from a listing.
type Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// Backend is the storage surface the backup pipeline uses. Fakes implement
// it for pipeline tests.
type Backend interface {
	// Put uploads r (size may be -1 if unknown) as key. Multipart is used
	// automatically for large payloads and aborted on failure.
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	// Get returns a reader over the object content plus its size.
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
	// Delete removes one object. Deleting an absent key is not an error.
	Delete(ctx context.Context, key string) error
	// List returns every object under prefix (paginated internally).
	List(ctx context.Context, prefix string) ([]Object, error)
	// Presign returns a short-lived GET URL for the key.
	Presign(ctx context.Context, key string, ttl time.Duration) (string, error)
	// Prefix returns the normalized prefix all keys live under.
	Prefix() string
}

// BackupKey is the ciphertext object key for a job.
func (c Config) BackupKey(databaseID, jobID int64) string {
	return fmt.Sprintf("%sdatabases/%d/backup-%d.dump.age", c.Prefix, databaseID, jobID)
}

// ManifestKey is the manifest object key for a job (the REMOTE COMMIT MARK,
// protocol C: publishing it makes the backup remotely available).
func (c Config) ManifestKey(databaseID, jobID int64) string {
	return fmt.Sprintf("%sdatabases/%d/backup-%d.manifest.json", c.Prefix, databaseID, jobID)
}

// DiagnosticKey returns a unique canary key under the diagnostic namespace,
// which is separate from the backup namespace (dev-plan Phase 3 task 2).
func (c Config) DiagnosticKey() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%sdiagnostic/%s/canary.txt", c.Prefix, hex.EncodeToString(b))
}
