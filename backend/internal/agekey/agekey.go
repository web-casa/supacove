// Package agekey manages the age key lifecycle per protocol B (dev-plan
// §0.5): the instance holds ONLY the recipient (public key) used to encrypt
// backups; the identity (private key) is displayed once at generation for
// offline storage and never persisted by this application. Losing the
// identity makes historical ciphertext permanently unreadable — that is by
// design and is communicated at generation time.
package agekey

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"filippo.io/age"
)

// Generate creates a fresh X25519 identity. The returned identity string
// (AGE-SECRET-KEY-…) must be shown exactly once; only the recipient is
// stored by the application.
func Generate() (identity, recipient string, err error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", "", fmt.Errorf("generate age identity: %w", err)
	}
	return id.String(), id.Recipient().String(), nil
}

// Fingerprint is the stable key ID recorded in the instance and in every
// manifest: the first 16 hex chars of SHA-256 over the recipient string.
func Fingerprint(recipient string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(recipient)))
	return hex.EncodeToString(sum[:16])
}

// ParseIdentity parses an identity string (with optional comment lines, as
// produced by age-keygen and by our CLI output): the LAST non-empty,
// non-comment line is the key.
func ParseIdentity(identity string) (*age.X25519Identity, error) {
	var line string
	for l := range strings.SplitSeq(strings.TrimSpace(identity), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		line = l
	}
	if line == "" {
		return nil, fmt.Errorf("parse age identity: no key line found")
	}
	id, err := age.ParseX25519Identity(line)
	if err != nil {
		return nil, fmt.Errorf("parse age identity: %w", err)
	}
	return id, nil
}

// EncryptStream encrypts everything read from r to w for the recipient.
// The caller MUST fully consume w's side effect: the age writer only emits
// its final chunk on Close, which this function performs — callers treat a
// returned error as a failed artifact (protocol A commit chain).
func EncryptStream(recipient string, r io.Reader, w io.Writer) error {
	rcp, err := age.ParseX25519Recipient(strings.TrimSpace(recipient))
	if err != nil {
		return fmt.Errorf("parse age recipient: %w", err)
	}
	aw, err := age.Encrypt(w, rcp)
	if err != nil {
		return fmt.Errorf("start age encryption: %w", err)
	}
	if _, err := io.Copy(aw, r); err != nil {
		aw.Close()
		return fmt.Errorf("copy into age writer: %w", err)
	}
	if err := aw.Close(); err != nil {
		return fmt.Errorf("close age writer: %w", err)
	}
	return nil
}

// DecryptStream decrypts r (age-encrypted for the matching recipient) into w
// using the identity. Authentication at the final chunk is part of the age
// format: truncated or tampered input errors here.
func DecryptStream(identity string, r io.Reader, w io.Writer) error {
	id, err := ParseIdentity(identity)
	if err != nil {
		return err
	}
	ar, err := age.Decrypt(r, id)
	if err != nil {
		return fmt.Errorf("start age decryption: %w", err)
	}
	if _, err := io.Copy(w, ar); err != nil {
		return fmt.Errorf("copy out of age reader: %w", err)
	}
	return nil
}
