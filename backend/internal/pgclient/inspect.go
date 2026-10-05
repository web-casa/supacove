package pgclient

import (
	"context"
	"strings"
)

// TestResult carries what the kernel needs from a successful connection test.
type TestResult struct {
	ServerVersion string
	ServerMajor   int
}

// Test connects with pgx and reports the server version. The pgx
// configuration is built from the SAME ConnInfo the dumper uses, so both
// paths share host/user/db/TLS semantics by construction.
func Test(ctx context.Context, c *ConnInfo) (*TestResult, error) {
	cfg, err := pgxConfig(c)
	if err != nil {
		return nil, err
	}
	conn, err := pgxConnect(ctx, cfg)
	if err != nil {
		return nil, ClassifyError(err)
	}
	defer conn.Close(context.WithoutCancel(ctx))

	var version string
	if err := conn.QueryRow(ctx, `SELECT version()`).Scan(&version); err != nil {
		return nil, ClassifyError(err)
	}
	major, err := ServerMajor(version)
	if err != nil {
		return nil, err
	}
	return &TestResult{ServerVersion: version, ServerMajor: major}, nil
}

// Extension and Dependencies are manifest (protocol E) payloads. The role
// query covers relationship owners, routine owners, schema owners, ACL
// grantees and default-ACL references — a fresh cluster needs all of these
// roles to exist before pg_restore (round-1 review P1-10).
type Extension struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Dependencies struct {
	Extensions       []Extension `json:"extensions"`
	Roles            []string    `json:"roles"`
	HasLargeObjects  bool        `json:"hasLargeObjects"`
	HasForeignTables bool        `json:"hasForeignTables"`
	ServerEncoding   string      `json:"serverEncoding"`
	// TableCount is the number of user tables at dump time. It is the
	// expected-object-set baseline restore verification compares against
	// (phase-5 review P1-08): a restore that completes without error but
	// loses tables must NOT be reported as verified.
	TableCount int64 `json:"tableCount"`
	// SourceDBBytes is the physical size of the source database
	// (pg_database_size) at collection time — the first of the three volume
	// metrics (dev-plan §0; measured口径: reported by the server, NOT
	// derived from the archive). 0 = unknown.
	SourceDBBytes int64 `json:"sourceDBBytes"`
}

// CollectDependencies gathers the restore-relevant facts recorded in the
// manifest (dev-plan P2 task 6, protocol E).
func CollectDependencies(ctx context.Context, c *ConnInfo) (*Dependencies, error) {
	cfg, err := pgxConfig(c)
	if err != nil {
		return nil, err
	}
	conn, err := pgxConnect(ctx, cfg)
	if err != nil {
		return nil, ClassifyError(err)
	}
	defer conn.Close(context.WithoutCancel(ctx))

	d := &Dependencies{}

	if err := conn.QueryRow(ctx,
		`SELECT pg_encoding_to_char(encoding) FROM pg_database WHERE datname = current_database()`).
		Scan(&d.ServerEncoding); err != nil {
		return nil, ClassifyError(err)
	}

	rows, err := conn.Query(ctx,
		`SELECT extname, COALESCE(extversion,'') FROM pg_extension ORDER BY 1`)
	if err != nil {
		return nil, ClassifyError(err)
	}
	for rows.Next() {
		var name, ver string
		if err := rows.Scan(&name, &ver); err != nil {
			rows.Close()
			return nil, ClassifyError(err)
		}
		d.Extensions = append(d.Extensions, Extension{Name: name, Version: ver})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, ClassifyError(err)
	}

	if err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_largeobject_metadata)`).Scan(&d.HasLargeObjects); err != nil {
		return nil, ClassifyError(err)
	}
	if err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_foreign_table)`).Scan(&d.HasForeignTables); err != nil {
		return nil, ClassifyError(err)
	}
	if err := conn.QueryRow(ctx,
		`SELECT count(*) FROM pg_tables
		 WHERE schemaname NOT IN ('pg_catalog','information_schema','pg_toast')`).
		Scan(&d.TableCount); err != nil {
		return nil, ClassifyError(err)
	}
	// Physical size of the source database (best effort: a permission error
	// here must not fail the backup; 0 = unknown).
	_ = conn.QueryRow(ctx, `SELECT pg_database_size(current_database())`).
		Scan(&d.SourceDBBytes)

	// Shared dependency roles: relation owners, routine owners, schema owners,
	// ACL grantees and default-ACL privileges.
	rows, err = conn.Query(ctx, `
		SELECT DISTINCT rolname FROM (
		    SELECT r.rolname AS rolname
		    FROM pg_class c JOIN pg_roles r ON r.oid = c.relowner
		                     JOIN pg_namespace n ON n.oid = c.relnamespace
		    WHERE n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
		    UNION
		    SELECT r.rolname
		    FROM pg_proc p JOIN pg_roles r ON r.oid = p.proowner
		                   JOIN pg_namespace n ON n.oid = p.pronamespace
		    WHERE n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
		    UNION
		    SELECT r.rolname
		    FROM pg_namespace ns JOIN pg_roles r ON r.oid = ns.nspowner
		    WHERE ns.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
		    UNION
		    SELECT grantee::text
		    FROM information_schema.role_table_grants
		    WHERE table_schema NOT IN ('pg_catalog','information_schema')
		      AND grantee NOT IN ('PUBLIC','postgres')
		    UNION
		    SELECT r.rolname
		    FROM pg_default_acl d, pg_roles r
		    WHERE r.oid = d.defaclrole
		) u WHERE rolname IS NOT NULL ORDER BY 1`)
	if err != nil {
		return nil, ClassifyError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, ClassifyError(err)
		}
		d.Roles = append(d.Roles, name)
	}
	if err := rows.Err(); err != nil {
		return nil, ClassifyError(err)
	}
	return d, nil
}

// SanitizeMessage is the shared credential scrubber for stored error text:
// URI schemes are rewritten, then every `password = value` occurrence has
// its VALUE replaced — quoted values in full, bare values up to the next
// delimiter (phase-8 canary: the old replacer kept the secret body).
// Idempotent: an already-redacted value is left alone, so composing with
// redact.Secrets in either order is safe (run SanitizeMessage AFTER
// redact.Secrets to keep whole-secret matching intact; see the log path).
func SanitizeMessage(msg string) string {
	msg = redactURIUserinfo(msg)
	return redactKeywordValues(msg)
}

// asciiLower folds A-Z only — byte-for-byte length-preserving, unlike
// strings.ToLower (whose Unicode folding, e.g. İ, shifts byte offsets and
// made index arithmetic on the original string wrong — round-2 review).
func asciiLower(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// redactURIUserinfo rewrites postgres URIs, dropping the entire authority
// userinfo: postgresql://user:secret@host/db → postgres-uri://[REDACTED]/db.
// (The old scheme-prefix replacer kept user:secret — a round-1 review note.)
func redactURIUserinfo(msg string) string {
	lower := asciiLower(msg)
	var out strings.Builder
	i := 0
	for i < len(msg) {
		idx := strings.Index(lower[i:], "postgres")
		if idx < 0 {
			out.WriteString(msg[i:])
			return out.String()
		}
		abs := i + idx
		rest := lower[abs:]
		if strings.HasPrefix(rest, "postgresql://") || strings.HasPrefix(rest, "postgres://") {
			schemeLen := len("postgres://")
			if strings.HasPrefix(rest, "postgresql://") {
				schemeLen = len("postgresql://")
			}
			out.WriteString(msg[i:abs])
			out.WriteString("postgres-uri://[REDACTED]")
			// Skip the authority: through the next '/', whitespace, or end.
			authStart := abs + schemeLen
			e := authStart
			for e < len(msg) && msg[e] != '/' && msg[e] != ' ' && msg[e] != '\t' {
				e++
			}
			i = e // keep the path (dbname)
			continue
		}
		out.WriteString(msg[i : abs+len("postgres")])
		i = abs + len("postgres")
	}
	return out.String()
}

// redactKeywordValues single-pass scanner: finds each case-insensitive
// `password` keyword followed by optional whitespace, '=', optional
// whitespace, then redacts the value (quoted → through the closing quote
// with escapes; bare → to the next delimiter).
func redactKeywordValues(msg string) string {
	const marker = "[REDACTED]"
	lower := asciiLower(msg)
	var out strings.Builder
	i := 0
	writeTailFrom := func(from int) { out.WriteString(msg[from:]) }
	for i < len(msg) {
		j := strings.Index(lower[i:], "password")
		if j < 0 {
			writeTailFrom(i)
			return out.String()
		}
		abs := i + j
		// Must be the whole keyword (not part of e.g. "spassword").
		if abs > 0 && isKeywordChar(lower[abs-1]) {
			out.WriteString(msg[i : abs+len("password")])
			i = abs + len("password")
			continue
		}
		k := abs + len("password")
		m := k
		for m < len(msg) && (msg[m] == ' ' || msg[m] == '\t') {
			m++
		}
		if m >= len(msg) || msg[m] != '=' {
			// Not an assignment; keep scanning after the keyword.
			out.WriteString(msg[i:m])
			i = m
			continue
		}
		m++ // past '='
		for m < len(msg) && (msg[m] == ' ' || msg[m] == '\t') {
			m++
		}
		// Copy through the '=' and padding.
		out.WriteString(msg[i:m])
		if strings.HasPrefix(msg[m:], marker) {
			out.WriteString(marker) // already redacted: idempotent
			i = m + len(marker)
			continue
		}
		if m < len(msg) && (msg[m] == '\'' || msg[m] == '"') {
			q := msg[m]
			e := m + 1
			for e < len(msg) {
				if msg[e] == '\\' {
					e += 2
					continue
				}
				if msg[e] == q {
					break
				}
				e++
			}
			if e >= len(msg) {
				out.WriteString(marker) // unterminated quote: redact the tail
				return out.String()
			}
			out.WriteString(marker) // includes the quotes
			i = e + 1
			continue
		}
		e := m
		for e < len(msg) {
			if msg[e] == '\\' && e+1 < len(msg) {
				e += 2 // escaped byte (e.g. '\ ') belongs to the value
				continue
			}
			if strings.ContainsRune(" \t,'\")]", rune(msg[e])) {
				break
			}
			e++
		}
		out.WriteString(marker)
		i = e
	}
	return out.String()
}

func isKeywordChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}
