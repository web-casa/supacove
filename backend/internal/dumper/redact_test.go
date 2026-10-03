package dumper

import (
	"strings"
	"testing"
	"time"
)

// TestRedactEscapeMatrix (round-5..9 reviews P1-02): the secret must be
// removed in every escape composition that conninfo quoting and JSON
// encoding actually produce, including composed forms.
func TestRedactEscapeMatrix(t *testing.T) {
	sec := `prefix'CANARY-SUFFIX`
	forms := map[string]string{
		"raw":           sec,
		"quoteEscaped":  `prefix\'CANARY-SUFFIX`,
		"backslashDbl":  strings.ReplaceAll(`prefix\'CANARY-SUFFIX`, `\`, `\\`),
		"jsonEscaped":   strings.ReplaceAll(sec, "'", `\'`),
		"doubleEscaped": strings.ReplaceAll(strings.ReplaceAll(sec, "'", `\'`), `\`, `\\`),
	}
	for name, form := range forms {
		got := RedactKnownSecrets([]string{sec}, "before "+form+" after")
		if strings.Contains(got, "CANARY-SUFFIX") || strings.Contains(got, "prefix") {
			t.Errorf("%s: secret survived: %q", name, got)
		}
	}
	// Empty secret is a no-op.
	if got := RedactKnownSecrets([]string{""}, "unchanged text"); got != "unchanged text" {
		t.Errorf("empty secret changed text: %q", got)
	}
}

// TestRedactMarkerSecretTerminates (round-8 review R8-P1-01): a secret equal
// to (or containing) the redaction marker must not loop forever.
func TestRedactMarkerSecretTerminates(t *testing.T) {
	done := make(chan string, 1)
	go func() {
		done <- RedactKnownSecrets([]string{"[REDACTED]"}, "value [REDACTED] value")
	}()
	select {
	case got := <-done:
		t.Logf("returned: %q", got)
	case <-time.After(5 * time.Second):
		t.Fatal("redaction looped forever with a marker-identical secret")
	}
	done2 := make(chan string, 1)
	go func() {
		done2 <- RedactKnownSecrets([]string{"REDACTED"}, "value REDACTED value")
	}()
	select {
	case <-done2:
	case <-time.After(5 * time.Second):
		t.Fatal("redaction looped forever with a marker-substring secret")
	}
}
