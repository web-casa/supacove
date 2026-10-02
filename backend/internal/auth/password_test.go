package auth

import (
	"strings"
	"testing"
)

func TestPasswordHashRoundtrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=2,p=1$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("correct password must verify")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Fatal("wrong password must not verify")
	}
}

func TestVerifyPasswordFailsClosedOnMalformedHash(t *testing.T) {
	for _, bad := range []string{
		"",
		"$bcrypt$abc",
		"$argon2id$v=999$m=1,t=1,p=1$",
		"$argon2id$v=19$m=65536,t=2,p=1$not-base64!!$also-bad",
	} {
		if VerifyPassword(bad, "anything") {
			t.Fatalf("malformed hash must not verify: %q", bad)
		}
	}
}

func TestPasswordSaltIsRandom(t *testing.T) {
	h1, _ := HashPassword("same password")
	h2, _ := HashPassword("same password")
	if h1 == h2 {
		t.Fatal("two hashes of the same password must differ (random salt)")
	}
}

func TestCredentialValidation(t *testing.T) {
	if err := ValidateUsername("ab"); err == nil {
		t.Error("too-short username must fail")
	}
	if err := ValidateUsername("has space"); err == nil {
		t.Error("username with space must fail")
	}
	if err := ValidateUsername("admin-01"); err != nil {
		t.Errorf("valid username rejected: %v", err)
	}
	if err := ValidatePassword("short"); err == nil {
		t.Error("short password must fail")
	}
	if err := ValidatePassword("long-enough-password"); err != nil {
		t.Errorf("valid password rejected: %v", err)
	}
}
