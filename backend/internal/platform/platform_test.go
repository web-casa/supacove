package platform

import (
	"testing"
)

func TestDetectPlatforms(t *testing.T) {
	cases := []struct {
		host string
		want Platform
	}{
		{"db.refproject.supabase.co", Supabase},
		{"ep-cool-name-123456.us-east-2.aws.neon.tech", Neon},
		{"proxy.rlwy.net", Railway},
		{"localhost", Generic},
		{"db.example.com", Generic},
	}
	for _, tc := range cases {
		got := Detect(tc.host)
		if got != tc.want {
			t.Errorf("Detect(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

func TestPoolingHints(t *testing.T) {
	if h := PoolingHint("pooler.supabase.com", "6543"); h == "" {
		t.Error("transaction pooler must produce a warning")
	}
	if h := PoolingHint("pooler.supabase.com", "5432"); h != "" {
		t.Errorf("session pooler should be OK: %q", h)
	}
	if h := PoolingHint("ep-name.neon.tech", "6543"); h == "" {
		t.Error("Neon pooled connection must produce a warning")
	}
}

func TestRecoveryNotesHasContent(t *testing.T) {
	for _, p := range []Platform{Supabase, Neon, Railway, Generic} {
		notes := RecoveryNotes(p)
		if len(notes) == 0 {
			t.Errorf("platform %q has no recovery notes", p)
		}
	}
}
