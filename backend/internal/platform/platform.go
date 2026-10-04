// Package platform implements connection-string analysis for supported
// PostgreSQL platforms (Supabase, Neon, Railway) and generates platform-
// aware recovery guidance. Detection uses endpoint characteristics only —
// never credentials or API calls.
package platform

import (
	"strings"
)

// Platform represents a known PostgreSQL hosting platform.
type Platform string

const (
	Supabase Platform = "supabase"
	Neon     Platform = "neon"
	Railway  Platform = "railway"
	Generic  Platform = "generic"
)

// Detect identifies the platform from a connection URI's host characteristics.
func Detect(host string) Platform {
	h := strings.ToLower(host)
	switch {
	case strings.Contains(h, "supabase.co") || strings.Contains(h, "supabase.com"):
		return Supabase
	case strings.Contains(h, "neon.tech"):
		return Neon
	case strings.Contains(h, "proxy.rlwy.net") || strings.Contains(h, "rlwy.net"):
		return Railway
	default:
		return Generic
	}
}

// PoolingHint returns a human-readable warning if the host/port suggests a
// pooled connection that is incompatible with pg_dump.
func PoolingHint(host string, port string) string {
	h := strings.ToLower(host)
	p := port
	if p == "" {
		p = "5432"
	}
	switch {
	case strings.Contains(h, "pooler.supabase.com") && p == "6543":
		return "Supabase transaction pooler (port 6543) is NOT compatible with pg_dump. Use the session pooler (port 5432) or the direct connection string."
	case strings.Contains(h, "pooler.supabase.com") && p == "5432":
		return "" // session pooler is OK
	case strings.Contains(h, "neon.tech") && p == "6543":
		return "Neon pooled connection (port 6543) is NOT compatible with pg_dump. Use the direct connection string (port 5432)."
	}
	return ""
}

// RecoveryNotes returns platform-specific recovery guidance lines for the
// restore.sh recovery kit.
func RecoveryNotes(p Platform) []string {
	switch p {
	case Supabase:
		return []string{
			"Supabase: this backup contains your database schema and data.",
			"Auth users, Storage objects, and Edge Functions are NOT included",
			"(they live in separate Supabase services).",
			"",
			"To restore into a NEW Supabase project:",
			"  1. Create the project and note its connection string.",
			"  2. Run pg_restore --exit-on-error --no-owner -d <new_project_url>.",
			"  3. Supabase managed schemas (auth, storage) already exist —",
			"     use --no-owner to avoid ownership conflicts.",
			"  4. Re-create RLS policies that reference auth.users if needed.",
			"",
			"Vault secrets and platform-level settings require manual recreation.",
		}
	case Neon:
		return []string{
			"Neon: this backup contains your database schema and data.",
			"Branches, computes, and roles are Neon platform resources —",
			"restore into a branch created via the Neon console or API.",
			"",
			"Use the direct (non-pooled) connection string for pg_restore.",
		}
	case Railway:
		return []string{
			"Railway: this backup contains your database schema and data.",
			"Railway service configuration and environment variables are",
			"platform resources — restore into a new Railway database plugin.",
			"",
			"Use the TCP proxy connection string for external access.",
		}
	default:
		return []string{
			"Generic PostgreSQL: restore into any PostgreSQL instance with",
			"a compatible or newer major version.",
		}
	}
}
