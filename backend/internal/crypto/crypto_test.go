package crypto

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func testKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}

func TestRoundtripAndUniqueNonce(t *testing.T) {
	key := testKey()
	plain := []byte("postgres://app:CANARY-pw@db:5432/appdb")
	e1, err := Encrypt(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	e2, err := Encrypt(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if e1 == e2 {
		t.Fatal("unique nonce required: two encryptions of the same plaintext differ")
	}
	got, err := Decrypt(key, e1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("roundtrip mismatch: %q", got)
	}
}

func TestDecryptWrongKeyFailsExplicitly(t *testing.T) {
	e, _ := Encrypt(testKey(), []byte("secret"))
	other := testKey()
	other[0] ^= 0xFF
	_, err := Decrypt(other, e)
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("want ErrAuthFailed, got %v", err)
	}
}

func TestDecryptCorruptionFailsExplicitly(t *testing.T) {
	key := testKey()
	e, _ := Encrypt(key, []byte("secret"))
	corrupted := e[:len(e)-2] + "xx"
	_, err := Decrypt(key, corrupted)
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("corrupted ciphertext must be ErrAuthFailed, got %v", err)
	}
	if !strings.Contains(ErrAuthFailed.Error(), "credential decryption failed") {
		t.Fatal("auth failure message must be explicit about what happened")
	}
}

func TestRejectsBadKeySize(t *testing.T) {
	if _, err := Encrypt([]byte("short"), []byte("x")); err == nil {
		t.Fatal("non-32-byte key must be rejected")
	}
}
