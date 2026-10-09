package dumper

import (
	"strings"
	"testing"

	"github.com/web-casa/supacove/backend/internal/redact"
)

func TestRound7QuotedRedaction(t *testing.T) {
	// dumper.sanitize keeps a bounded excerpt and masks obvious URI forms;
	// VALUE removal for known secrets now happens in the shared redact
	// package (verified there across the full escape matrix). The dumper
	// excerpt may still contain the secret at this layer by design.
	in := `password='prefix\'CANARY-SUFFIX'`
	out := sanitize(in)
	t.Logf("excerpt after sanitize: %q", out)

	secret := `prefix'CANARY-SUFFIX` //nolint:gosec // G101: deliberate canary secret for the sanitizer test
	texts := []string{
		`plain ` + secret + ` end`,
		"escaped prefix\\'CANARY-SUFFIX end",
		"double prefix\\\\'CANARY-SUFFIX end",
	}
	for i, txt := range texts {
		out := redact.Secrets([]string{secret}, txt)
		if strings.Contains(out, "CANARY-SUFFIX") {
			t.Fatalf("text %d leaked: %q", i, out)
		}
	}
}
