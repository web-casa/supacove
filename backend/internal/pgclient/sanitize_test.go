package pgclient

import "testing"

func TestSanitizeMessageValues(t *testing.T) {
	cases := []struct{ in, want string }{
		{"PASSWORD=one password=two", "PASSWORD=[REDACTED] password=[REDACTED]"},
		{"password='alpha beta' host=x", "password=[REDACTED] host=x"},
		{`password="a,b" x=1`, `password=[REDACTED] x=1`},
		{"password=alpha beta", "password=[REDACTED] beta"},
		{"password=alpha,beta", "password=[REDACTED],beta"},
		{"password=[REDACTED]", "password=[REDACTED]"},
		{"password = alpha", "password = [REDACTED]"},
		{"postgresql://u:secret@h/db", "postgres-uri://[REDACTED]/db"},
		{"postgres://u@h/db", "postgres-uri://[REDACTED]/db"},
		{"postgres://u:p@h", "postgres-uri://[REDACTED]"},
		{"no secrets here", "no secrets here"},
		{"notpassword=keep", "notpassword=keep"},
		{"password='it\\'s' x", "password=[REDACTED] x"},
	}
	for _, c := range cases {
		if got := SanitizeMessage(c.in); got != c.want {
			t.Errorf("SanitizeMessage(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Composition: Sanitize AFTER redact.Secrets must not break whole-secret
	// matching, and the marker must be stable.
	once := SanitizeMessage("dial failed password=alpha,beta host=x")
	twice := SanitizeMessage(once)
	if once != "dial failed password=[REDACTED],beta host=x" {
		t.Errorf("first pass: %q", once)
	}
	if twice != once {
		t.Errorf("not idempotent: %q vs %q", once, twice)
	}
}
