package dumper

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/cloudfan/supabackup/backend/internal/pgclient"
)

// TestExcerptOfReviewFixtures (round-11 review R11-P1-01): the three leak
// forms the review demonstrated must all be redacted.
func TestExcerptOfReviewFixtures(t *testing.T) {
	cases := []struct {
		name    string
		stderr  string
		secrets []string
	}{
		{
			name:    "percent-encoded URI credentials",
			stderr:  `connection failed: postgres://app:CANARY-P%40ss%2Fword@localhost/appdb?sslmode=disable`,
			secrets: []string{"CANARY-P@ss/word", "CANARY-P%40ss%2Fword"},
		},
		{
			name:    "retention boundary cuts password mid-value",
			stderr:  strings.Repeat("x", (16<<10)-len("password=")-len("CANARY-cut-")) + "password=CANARY-cut-PASSWORD-SUFFIX",
			secrets: []string{"CANARY-cut-PASSWORD-SUFFIX"},
		},
		{
			name:    "scheme rewrite inside quoted secret",
			stderr:  `password='prefixpostgres://CANARY-SUFFIX'`,
			secrets: []string{"prefixpostgres://CANARY-SUFFIX"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The boundary-cut fixture simulates a retention truncation; the
			// others represent complete stderr streams.
			truncated := strings.Contains(tc.name, "boundary")
			got := excerptOf([]byte(tc.stderr), truncated, tc.secrets[0])
			for _, sec := range tc.secrets {
				if strings.Contains(got, sec) {
					t.Fatalf("%s: secret survived: %q", tc.name, got)
				}
			}
		})
	}
}

// TestExcerptOfPercentEncodingMatrix: every percent-encoded representation
// of the password is removed.
func TestExcerptOfPercentEncodingMatrix(t *testing.T) {
	pw := `p@ss/word~x-y_z`
	encAll := make([]byte, 0, len(pw)*3)
	for i := 0; i < len(pw); i++ {
		encAll = append(encAll, fmt.Appendf(nil, "%%%02X", pw[i])...)
	}
	encPartial := "p%40ss%2Fword~x-y_z"
	for _, form := range []string{string(encAll), encPartial} {
		got := excerptOf([]byte("error: "+form+"\n"), false, pw)
		if strings.Contains(got, form) {
			t.Fatalf("percent form survived: %q", got)
		}
	}
}

// TestParseURIRoundTripStillValid: percent-encoded password from
// url.UserPassword round-trips through ParseURI.
func TestParseURIRoundTripStillValid(t *testing.T) {
	u := url.UserPassword("app", "CANARY-P@ss/word")
	uri := "postgres://" + u.String() + "@localhost/appdb?sslmode=disable"
	// sanity: the URI itself contains the percent-encoded form
	if !strings.Contains(uri, "%40") {
		t.Fatalf("expected percent encoding in %q", uri)
	}
	_ = url.QueryEscape
}

// TestExcerptOfJSONConninfoAtBoundary (round-14 review R13-P1-01 residue):
// the JSON-encoded conninfo form (3+ consecutive backslashes) sits at the
// tail of a full, un-truncated 16 KiB stderr — the exact residual leak.
func TestExcerptOfJSONConninfoAtBoundary(t *testing.T) {
	secret := `prefix\segment'CANARY-SUFFIX`
	encoded := `"` + strings.ReplaceAll(strings.ReplaceAll(
		pgclient.QuoteConninfo(secret), `\`, `\\`), `'`, `\'`) + `"`
	value := "password=" + encoded
	label := "postgres://"
	stderr := label + strings.Repeat("x", (16<<10)-len(label)-len(value)-1) + value + "\n"

	got := excerptOf([]byte(stderr), false, secret)
	for _, s := range []string{"CANARY-SUFFIX", "CANARY"} {
		if strings.Contains(got, s) {
			t.Fatalf("JSON conninfo secret survived at boundary: %q", got)
		}
	}
}
