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
	if h := PoolingHint("pooler.supabase.com", "6543"); h.En == "" {
		t.Error("Supabase transaction pooler must produce a warning")
	}
	if h := PoolingHint("pooler.supabase.com", "5432"); h.En != "" {
		t.Errorf("Supabase session pooler should be OK: %q", h)
	}
	if h := PoolingHint("db.ref.supabase.co", "6543"); h.En != "" {
		t.Errorf("direct Supabase host should not warn on 6543: %q", h)
	}
	// Neon port marker
	if h := PoolingHint("ep-name.neon.tech", "6543"); h.En == "" {
		t.Error("Neon pooled connection must produce a warning")
	}
	// Neon -pooler HOSTNAME marker on the default port (P2-01: the review's
	// exact false-negative case — pooled endpoint, port 5432, no warning).
	if h := PoolingHint("ep-cool-123456-pooler.eu-central-1.aws.neon.tech", "5432"); h.En == "" {
		t.Error("Neon '-pooler' endpoint must warn even on port 5432")
	}
	if h := PoolingHint("ep-cool-123456.eu-central-1.aws.neon.tech", "5432"); h.En != "" {
		t.Errorf("Neon direct endpoint should not warn: %q", h)
	}
	// lookalike hosts must not inherit platform hints
	if h := PoolingHint("pooler.supabase.com.evil.invalid", "6543"); h.En != "" {
		t.Errorf("lookalike host must not warn: %q", h)
	}
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
