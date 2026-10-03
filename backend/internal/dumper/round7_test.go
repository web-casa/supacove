package dumper

import (
	"strings"
	"testing"
)

func TestRound7QuotedRedaction(t *testing.T) {
	// The exact leak form from round-7 review: conninfo quoting with an
	// escaped quote inside the secret.
	in := `password='prefix\'CANARY-SUFFIX'`
	out := sanitize(in)
	if strings.Contains(out, "CANARY-SUFFIX") {
		t.Fatalf("escaped-quote secret survived: %q", out)
	}
	if strings.Contains(out, "CANARY") {
		t.Fatalf("partial leak: %q", out)
	}

	// RedactKnownSecrets with the known value removes raw/escaped/double-escaped.
	secret := `prefix'CANARY-SUFFIX`
	texts := []string{
		`plain ` + secret + ` end`,
		"escaped prefix\\'CANARY-SUFFIX end",
		"double prefix\\\\'CANARY-SUFFIX end",
	}
	for i, txt := range texts {
		out := RedactKnownSecrets([]string{secret}, txt)
		if strings.Contains(out, "CANARY-SUFFIX") {
			t.Fatalf("text %d leaked: %q", i, out)
		}
	}
}
