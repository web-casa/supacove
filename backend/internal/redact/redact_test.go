package redact

import (
	"strings"
	"testing"
	"time"
)

// TestSecretsMatrix mirrors the review's 5 secrets x 7 representations
// matrix (round-10 review: 31/35 -> the 4 failures were composed escapes
// with 3+ consecutive backslashes after JSON encoding).
func TestSecretsMatrix(t *testing.T) {
	secretsList := []string{
		`plain-secret`,
		`with'quote`,
		`with\backslash`,
		`both\'mixed`,
		`triple\\\stack`,
	}
	reprs := map[string]func(string) string{
		"raw":            func(s string) string { return s },
		"quoteEscaped":   func(s string) string { return strings.ReplaceAll(s, "'", `\'`) },
		"backslashDbl":   func(s string) string { return strings.ReplaceAll(s, `\`, `\\`) },
		"combined":       func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", `\'`) },
		"json":           func(s string) string { return strings.ReplaceAll(s, `\`, `\\`) },
		"jsonOfQuoteEsc": func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "'", `\'`), `\`, `\\`) },
		"tripleBackslash": func(s string) string {
			return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\\\`), "'", `\\'`)
		},
	}
	for _, sec := range secretsList {
		for name, fn := range reprs {
			form := fn(sec)
			got := Secrets([]string{sec}, "before "+form+" after")
			if strings.Contains(got, sec) || strings.Contains(got, form) {
				t.Errorf("%s / %s: secret survived: %q", sec, name, got)
			}
		}
	}
}

// TestMarkerTerminates (round-8 review R8-P1-01): marker-identical or
// marker-substring secrets must not loop.
func TestMarkerTerminates(t *testing.T) {
	done := make(chan string, 1)
	go func() {
		done <- Secrets([]string{"[REDACTED]"}, "value [REDACTED] value")
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop with marker-identical secret")
	}
	done2 := make(chan string, 1)
	go func() {
		done2 <- Secrets([]string{"REDACTED"}, "value REDACTED value")
	}()
	select {
	case <-done2:
	case <-time.After(5 * time.Second):
		t.Fatal("loop with marker-substring secret")
	}
}
