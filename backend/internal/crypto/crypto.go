// Package crypto provides the AES-GCM encryption used for credentials at
// rest (dev-plan §0.5 protocol B). Every encryption uses a fresh random
// nonce; authentication failures are explicit errors — never silent.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrAuthFailed means the ciphertext failed authentication: the master key
// is wrong or the data was corrupted. Callers must surface this explicitly
// (protocol B), e.g. "recorded credentials must be re-entered".
var ErrAuthFailed = errors.New("credential decryption failed: wrong master key or corrupted data")

// Encrypt seals plaintext with AES-256-GCM under key (32 bytes) and returns
// base64(nonce || ciphertext). Each call draws a unique random nonce.
func Encrypt(key, plaintext []byte) (string, error) {
	gcm, err := cipherFor(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a value produced by Encrypt. It returns ErrAuthFailed
// (wrapped) when authentication fails.
func Decrypt(key []byte, encoded string) ([]byte, error) {
	gcm, err := cipherFor(key)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: not base64", ErrAuthFailed)
	}
	if len(raw) < gcm.NonceSize() {
		return nil, fmt.Errorf("%w: truncated", ErrAuthFailed)
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrAuthFailed
	}
	return plain, nil
}

func cipherFor(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("master key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
