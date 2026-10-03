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

// SanitizeMessage is the shared credential scrubber for stored error text.
func SanitizeMessage(msg string) string {
	r := strings.NewReplacer(
		"password=", "password=[REDACTED]",
		"postgres://", "postgres-uri://[REDACTED]",
		"postgresql://", "postgres-uri://[REDACTED]",
	)
	return r.Replace(msg)
}
