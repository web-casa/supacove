package platform

import (
	"strings"
	"testing"
)

func TestDetectPlatforms(t *testing.T) {
	cases := []struct {
		host string
		want Platform
	}{
		// positive cases
		{"db.refproject.supabase.co", Supabase},
		{"db.refproject.supabase.co.", Supabase}, // FQDN trailing dot
		{"DB.REF.SUPABASE.COM", Supabase},        // case-insensitive
		{"ep-cool-name-123456.us-east-2.aws.neon.tech", Neon},
		{"ep-cool-123456-pooler.eu-central-1.aws.neon.tech", Neon},
		{"proxy.rlwy.net", Railway},
		{"monorail.proxy.rlwy.net", Railway},
		{"postgres.railway.internal", Railway}, // Railway private network
		{"localhost", Generic},
		{"db.example.com", Generic},
		// pseudo-suffix attacks must NOT classify (P2-01)
		{"supabase.com.evil.invalid", Generic},
		{"evilsupabase.co", Generic},
		{"notneon.tech.example", Generic},
		{"neon.tech.evil.invalid", Generic},
		{"proxy.rlwy.net.evil.invalid", Generic},
		{"rlwy.net.example.com", Generic},
	}
	for _, tc := range cases {
		if got := Detect(tc.host); got != tc.want {
			t.Errorf("Detect(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

func TestPoolingHints(t *testing.T) {
	// A warning must carry BOTH languages; no warning means a truly empty Msg.
	expectWarn := func(name, host, port string) {
		t.Helper()
		h := PoolingHint(host, port)
		if h.Empty() || h.En == "" || h.Zh == "" {
			t.Errorf("%s must warn in both languages, got %+v", name, h)
		}
	}
	expectOK := func(name, host, port string) {
		t.Helper()
		if h := PoolingHint(host, port); !h.Empty() {
			t.Errorf("%s should not warn: %+v", name, h)
		}
	}
	expectWarn("Supabase transaction pooler", "pooler.supabase.com", "6543")
	expectOK("Supabase session pooler", "pooler.supabase.com", "5432")
	expectOK("direct Supabase host", "db.ref.supabase.co", "6543")
	// Neon port marker
	expectWarn("Neon pooled port", "ep-name.neon.tech", "6543")
	// Neon -pooler HOSTNAME marker on the default port (P2-01: the review's
	// exact false-negative case — pooled endpoint, port 5432, no warning).
	expectWarn("Neon -pooler endpoint", "ep-cool-123456-pooler.eu-central-1.aws.neon.tech", "5432")
	expectOK("Neon direct endpoint", "ep-cool-123456.eu-central-1.aws.neon.tech", "5432")
	// lookalike hosts must not inherit platform hints
	expectOK("lookalike host", "pooler.supabase.com.evil.invalid", "6543")
}

func TestRecoveryNotesScope(t *testing.T) {
	// P1-10: the Supabase guide must NOT claim auth/storage schemas are
	// excluded — the backup is a FULL database dump that includes them.
	notes := strings.Join(RecoveryNotes(Supabase), "\n")
	if !strings.Contains(notes, "auth") || !strings.Contains(notes, "storage") {
		t.Error("Supabase notes must disclose the managed schemas' presence in the dump")
	}
	lower := strings.ToLower(notes)
	if strings.Contains(lower, "edge functions are not included") && !strings.Contains(lower, "in object storage") {
		t.Error("Supabase notes still carry the wrong blanket 'not included' claim")
	}
	if !strings.Contains(notes, "NOT implement a Supabase-to-") {
		t.Error("Supabase notes must state the generic script is not a project-to-project migration path")
	}
	for _, p := range []Platform{Supabase, Neon, Railway, Generic} {
		if len(RecoveryNotes(p)) == 0 {
			t.Errorf("platform %q has no recovery notes", p)
		}
	}
}

// TestResolve: a recognizable host wins; otherwise the registered platform
// stands (a self-hosted Supabase has an arbitrary hostname).
func TestResolve(t *testing.T) {
	cases := []struct {
		host, registered string
		want             Platform
	}{
		{"db.ref.supabase.co", "generic", Supabase},
		{"ep-x.neon.tech", "supabase", Neon}, // the host is the stronger signal
		{"10.0.0.5", "supabase", Supabase},
		{"db.internal", "neon", Neon},
		{"db.internal", "generic", Generic},
		{"db.internal", "", Generic},
		{"db.internal", "anything-else", Generic},
	}
	for _, c := range cases {
		if got := Resolve(c.host, c.registered); got != c.want {
			t.Errorf("Resolve(%q, %q) = %q, want %q", c.host, c.registered, got, c.want)
		}
	}
}
