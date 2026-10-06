package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. m=64MiB is a deliberate trade-off for small self-hosted
// boxes; review before raising (each login allocates this much).
const (
	argonTime    = 2
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// kdfConcurrency bounds global Argon2id executions so concurrent anonymous
// requests cannot exhaust memory (review P0-02/P1-01). Handlers reject with
// 503 via ErrKDFBusy when the budget is exhausted.
var kdfSem = make(chan struct{}, 4)

// acquireKDF takes one slot from the global KDF budget, honoring ctx
// cancellation while waiting.
func acquireKDF(ctx context.Context) (func(), error) {
	select {
	case kdfSem <- struct{}{}:
		return func() { <-kdfSem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		// Budget exhausted: fail fast rather than queue unboundedly.
		return nil, ErrKDFBusy
	}
}

// HashPassword returns a PHC-formatted Argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// phcParamsRE fully consumes the parameter field of the single supported
// profile; Sscanf-style prefix matching would accept trailing junk such as
// "v=19junk" or "p=1,unknown=9" (review P2-02 / round 3 NOT_FIXED).
var phcVersionRE = regexp.MustCompile(`^v=19$`)
var phcParamsRE = regexp.MustCompile(`^m=([0-9]+),t=([0-9]+),p=([0-9]+)$`)

// VerifyPassword checks a password against a PHC-formatted Argon2id hash,
// strictly within the single supported profile: unknown formats, unsupported
// versions, or out-of-range parameters fail closed without executing the KDF
// (review P2-02).
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false
	}
	if !phcVersionRE.MatchString(parts[2]) {
		return false
	}
	pm := phcParamsRE.FindStringSubmatch(parts[3])
	if pm == nil {
		return false
	}
	m64, _ := strconv.ParseUint(pm[1], 10, 32)
	t64, _ := strconv.ParseUint(pm[2], 10, 32)
	p64, _ := strconv.ParseUint(pm[3], 10, 8)
	m, t, p := uint32(m64), uint32(t64), uint8(p64)
	// Bound parameters: malformed or hostile hashes must neither panic nor
	// allocate without limits. Only the profile we ourselves produce passes.
	if m < 8*1024 || m > 512*1024 || t < 1 || t > 16 || p < 1 || p > 4 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != argonSaltLen {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != argonKeyLen {
		return false
	}
	// len(want) is argonKeyLen (a small constant) — the conversion cannot
	// overflow; keep gosec G115 satisfied with an explicit bound check.
	keyLen := len(want)
	if keyLen <= 0 || keyLen > 1<<20 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(keyLen))
	return subtle.ConstantTimeCompare(got, want) == 1
}
