package dumper

import (
	"strings"
	"testing"

	"github.com/web-casa/supacove/backend/internal/pgclient"
	"github.com/web-casa/supacove/backend/internal/redact"
)

// TestExcerptComposedEscapeAtBoundary (round-14/15 review): the composed
// backslash-doubled + quote-escaped form and the triple-backslash form sit
// at the tail of a full, un-truncated 16 KiB stderr.
func TestExcerptComposedEscapeAtBoundary(t *testing.T) {
	secret := `prefix\segment'CANARY-SUFFIX`
	forms := map[string]string{
		"conninfo": pgclient.QuoteConninfo(secret),
		"json":     strings.ReplaceAll(strings.ReplaceAll(pgclient.QuoteConninfo(secret), `\`, `\\`), `'`, `\'`),
		"triple":   `prefix\\\\segment\\\\\\'CANARY-SUFFIX`,
	}
	for name, form := range forms {
		value := "password=" + form
		label := "postgres://"
		stderr := label + strings.Repeat("x", (16<<10)-len(label)-len(value)-1) + value + "\n"

		excerpt := excerptOf([]byte(stderr), false, secret)
		// The jobs-layer redact.Secrets is the final defense; the dumper
		// layer already removed the known password.
		final := redact.Secrets([]string{secret}, excerpt)
		if strings.Contains(final, "CANARY-SUFFIX") {
			t.Errorf("%s: secret survived the full chain: %q -> %q", name, excerpt, final)
		}
	}
}
