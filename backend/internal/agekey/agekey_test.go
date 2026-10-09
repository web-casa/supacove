package agekey

import (
	"bytes"
	"strings"
	"testing"
)

func TestGenerateAndFingerprint(t *testing.T) {
	id, rcp, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rcp, "age1") {
		t.Fatalf("recipient must be age1…, got %q", rcp)
	}
	if !strings.HasPrefix(id, "AGE-SECRET-KEY-") {
		t.Fatalf("identity format unexpected: %q", id[:20])
	}
	fp := Fingerprint(rcp)
	if len(fp) != 32 || fp == strings.Repeat("0", 32) {
		t.Fatalf("fingerprint malformed: %q", fp)
	}
	if Fingerprint(rcp) != fp {
		t.Fatal("fingerprint must be deterministic")
	}
	if Fingerprint(rcp+"x") == fp {
		t.Fatal("different recipient must produce a different fingerprint")
	}
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	id, rcp, _ := Generate()
	plain := []byte("backup payload with CANARY data")
	var enc bytes.Buffer
	if err := EncryptStream(rcp, bytes.NewReader(plain), &enc); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc.Bytes(), plain) {
		t.Fatal("ciphertext must not contain the plaintext")
	}
	var dec bytes.Buffer
	if err := DecryptStream(id, bytes.NewReader(enc.Bytes()), &dec); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec.Bytes(), plain) {
		t.Fatal("roundtrip mismatch")
	}
}

func TestDecryptWrongIdentityFails(t *testing.T) {
	_, rcp, _ := Generate()
	otherID, _, _ := Generate()
	var enc bytes.Buffer
	_ = EncryptStream(rcp, strings.NewReader("secret"), &enc)
	var dec bytes.Buffer
	if err := DecryptStream(otherID, bytes.NewReader(enc.Bytes()), &dec); err == nil {
		t.Fatal("wrong identity must fail authentication")
	}
}

func TestTruncatedCiphertextFails(t *testing.T) {
	id, rcp, _ := Generate()
	var enc bytes.Buffer
	_ = EncryptStream(rcp, strings.NewReader("secret payload"), &enc)
	var dec bytes.Buffer
	if err := DecryptStream(id, bytes.NewReader(enc.Bytes()[:enc.Len()/2]), &dec); err == nil {
		t.Fatal("truncated ciphertext must fail (age authenticates the final chunk)")
	}
}

func TestParseIdentityAcceptsCommentHeader(t *testing.T) {
	id, rcp, _ := Generate()
	withComment := "# created by supacove\n" + id
	parsed, err := ParseIdentity(withComment)
	if err != nil {
		t.Fatalf("comment header must be tolerated: %v", err)
	}
	if parsed.Recipient().String() != rcp {
		t.Fatal("parsed identity does not match its recipient")
	}
	if _, err := ParseIdentity("garbage"); err == nil {
		t.Fatal("garbage identity must fail")
	}
}
