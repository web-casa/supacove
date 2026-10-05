// Package platform implements connection-string analysis for supported
// PostgreSQL platforms (Supabase, Neon, Railway) and generates platform-
// aware recovery guidance. Detection uses endpoint characteristics only —
// never credentials or API calls.
package platform

import (
	"strings"

	"github.com/cloudfan/supabackup/backend/internal/i18n"
)

// Platform represents a known PostgreSQL hosting platform.
type Platform string

const (
	Supabase Platform = "supabase"
	Neon     Platform = "neon"
	Railway  Platform = "railway"
	Generic  Platform = "generic"
)

// normalizeHost lowercases and strips one trailing dot (FQDN form).
func normalizeHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	return strings.TrimSuffix(h, ".")
}

// hostSuffix matches host against a domain suffix on a DNS-label boundary:
// "db.supabase.co" matches ".supabase.co"; "evilsupabase.co" does not.
func hostSuffix(host, suffix string) bool {
	return host == suffix || strings.HasSuffix(host, "."+suffix)
}

// Detect identifies the platform from a connection URI's host
// characteristics. Matching is suffix-with-label-boundary after
// normalization — a host like "supabase.com.evil.invalid" or
// "notneon.tech.example" is NOT the platform it embeds (phase-5 review
// P2-01). Railway private-network hostnames (.railway.internal) count as
// Railway: they are only resolvable inside Railway's network.
func Detect(host string) Platform {
	h := normalizeHost(host)
	switch {
	case hostSuffix(h, "supabase.co") || hostSuffix(h, "supabase.com"):
		return Supabase
	case hostSuffix(h, "neon.tech"):
		return Neon
	case hostSuffix(h, "rlwy.net") || hostSuffix(h, "railway.internal"):
		return Railway
	default:
		return Generic
	}
}

// PoolingHint returns a human-readable warning if the host/port suggests a
// pooled connection that is incompatible with pg_dump. Both languages are
// returned so the caller picks: API responses localize via Accept-Language,
// persisted job records stay English.
//
// Detection covers both pooling markers documented by the platforms:
//   - Supabase pooler hostnames (pooler.<ref>.supabase.com): port 6543 is
//     the transaction pooler (incompatible), 5432 the session pooler (OK).
//   - Neon pooled endpoints embed a "-pooler" label in the hostname
//     (ep-xxx-pooler.<region>.aws.neon.tech) and are transaction-mode
//     pgbouncer regardless of port; the direct endpoint omits the label.
func PoolingHint(host string, port string) i18n.Msg {
	h := normalizeHost(host)
	p := port
	if p == "" {
		p = "5432"
	}
	switch {
	// Supabase session/transaction pooler.
	case hostSuffix(h, "supabase.co") || hostSuffix(h, "supabase.com"):
		if strings.HasPrefix(h, "pooler.") || strings.Contains(h, ".pooler.") {
			if p == "6543" {
				return i18n.Msg{
					En: "Supabase transaction pooler (port 6543) is NOT compatible with pg_dump. Use the session pooler (port 5432) or the direct connection string.",
					Zh: "Supabase transaction pooler（端口 6543）与 pg_dump 不兼容。请使用 session pooler（端口 5432）或直连字符串。",
				}
			}
			return i18n.Msg{} // session pooler is OK
		}
		return i18n.Msg{}
	// Neon: the -pooler label marks the pooled endpoint on ANY port.
	case hostSuffix(h, "neon.tech"):
		if p == "6543" {
			return i18n.Msg{
				En: "Neon pooled connection (port 6543) is NOT compatible with pg_dump. Use the direct connection string (port 5432, no '-pooler' in the host).",
				Zh: "Neon 池化连接（端口 6543）与 pg_dump 不兼容。请使用直连字符串（端口 5432，主机名中不含“-pooler”）。",
			}
		}
		if containsLabel(h, "-pooler") {
			return i18n.Msg{
				En: "This Neon endpoint is a POOLED endpoint ('-pooler' host) and is NOT compatible with pg_dump, regardless of port. Use the direct connection string (same host without '-pooler', port 5432).",
				Zh: "这个 Neon 端点是池化端点（主机名含“-pooler”），无论端口如何都与 pg_dump 不兼容。请使用直连字符串（同一主机去掉“-pooler”，端口 5432）。",
			}
		}
		return i18n.Msg{}
	}
	return i18n.Msg{}
}

// containsLabel reports whether part contains marker such that it is
// preceded and followed by label separators (start, '.', '-', or end), so
// "not-pooler.example" style false embedding is still accepted deliberately
// for "-pooler" because Neon embeds the marker inside the endpoint label
// itself (ep-xxx-pooler).
func containsLabel(host, marker string) bool {
	return strings.Contains(host, marker+".") || strings.HasSuffix(host, marker)
}

// RecoveryNotes returns platform-specific recovery guidance lines for the
// restore.sh recovery kit. The text must stay consistent with what the
// backup ACTUALLY contains: a full pg_dump of the database — every user
// schema at dump time — and nothing from platform services outside the
// database (phase-5 review P1-10).
func RecoveryNotes(p Platform) []string {
	switch p {
	case Supabase:
		return []string{
			"Supabase: this archive is a FULL pg_dump of the Postgres database.",
			"It contains ALL user schemas present at dump time — on Supabase that",
			"includes the managed 'auth' and 'storage' schemas (auth user records,",
			"storage object metadata).",
			"NOT included: Storage file blobs (they live in object storage, not in",
			"the database), Edge Function code, Auth/Storage service configuration,",
			"and platform-level settings.",
			"",
			"IMPORTANT: this generic script does NOT implement a Supabase-to-",
			"Supabase migration. Restoring into a new Supabase project conflicts",
			"with its managed schemas, system roles and hosted extensions. Restore",
			"into a plain PostgreSQL instance (any major >= the source), or follow",
			"Supabase's official backup/restore guide for project-to-project moves.",
			"By default the script refuses a non-empty target; an override exists",
			"but does not resolve role/extension conflicts on its own.",
		}
	case Neon:
		return []string{
			"Neon: this archive is a FULL pg_dump of the database branch's",
			"database, including all user schemas.",
			"NOT included: Neon platform resources — branches, computes, roles",
			"managed by the console, and other databases on the project.",
			"",
			"Restore into a branch/database created via the Neon console or API,",
			"using the DIRECT (non-pooled) connection string — never a '-pooler'",
			"endpoint; pgbouncer transaction mode breaks pg_restore.",
		}
	case Railway:
		return []string{
			"Railway: this archive is a FULL pg_dump of the database, including",
			"all user schemas.",
			"NOT included: the Railway service, its environment variables, and",
			"other plugins.",
			"",
			"Restore into a new Railway Postgres plugin instance via its TCP proxy",
			"connection string.",
		}
	default:
		return []string{
			"Generic PostgreSQL: this archive is a FULL pg_dump of the database,",
			"including all user schemas. Restore into any PostgreSQL instance with",
			"a compatible or newer major version; recreate required roles first",
			"(they are listed in the manifest) or accept --no-owner semantics.",
		}
	}
}
