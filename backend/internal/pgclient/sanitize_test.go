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
		{"İ password=secret", "İ password=[REDACTED]"}, // U+0130 must not shift byte offsets
		{"İ postgres://u:secret@host/db", "İ postgres-uri://[REDACTED]/db"},
		{"postgres://u:p@host error reading /tmp/x", "postgres-uri://[REDACTED] error reading /tmp/x"},
		{"password=alpha\\ beta host=x", "password=[REDACTED] host=x"}, // escaped space is part of the value
	}
	for _, c := range cases {
		if got := SanitizeMessage(c.in); got != c.want {
			t.Errorf("SanitizeMessage(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Composition (round-3): the supported order is Secrets → Sanitize —
	// whole secret removed first, syntax scrub second. The reverse order
	// truncates the body and is NOT supported (documented on SanitizeMessage).
	sanitizedOnce := SanitizeMessage("dial failed password=alpha,beta host=x")
	if sanitizedOnce != "dial failed password=[REDACTED],beta host=x" {
		t.Errorf("first pass: %q", sanitizedOnce)
	}
	if got := SanitizeMessage(sanitizedOnce); got != sanitizedOnce {
		t.Errorf("not idempotent: %q vs %q", sanitizedOnce, got)
	}
	// Newline after the URI authority must terminate it (diagnostic text on
	// the next line survives).
	if got := SanitizeMessage("postgres://u:p@host\nerror reading /tmp/x"); got != "postgres-uri://[REDACTED]\nerror reading /tmp/x" {
		t.Errorf("newline authority: %q", got)
	}
}
