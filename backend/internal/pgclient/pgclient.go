// Package pgclient tests and inspects target PostgreSQL servers, and shares
// its connection semantics with the pg_dump executor so both see the same
// host/user/db/TLS view (dev-plan P2 task 5).
package pgclient

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ErrClass classifies failures into the seven job error classes plus unknown
// (dev-plan P2 task 4). Classification is derived from libpq/pgx error text
// patterns at the point of failure.
type ErrClass string

const (
	ClassNetwork    ErrClass = "network"
	ClassAuth       ErrClass = "auth"
	ClassPermission ErrClass = "permission"
	ClassClientVer  ErrClass = "client_version"
	ClassDisk       ErrClass = "disk"
	ClassStorageUp  ErrClass = "storage_upload"
	ClassVerify     ErrClass = "verification"
	ClassUnknown    ErrClass = "unknown"
)

// Allowed URI query parameters. Everything else — notably passfile,
// service, sslkey, sslcert, options — is rejected: credentials and
// connection behaviour must flow through this application only (dev-plan
// P2 task 2).
var allowedParams = map[string]bool{
	"sslmode":          true,
	"connect_timeout":  true,
	"application_name": true,
}

var sslmodes = map[string]bool{
	"disable": true, "allow": true, "prefer": true, "require": true,
	"verify-ca": true, "verify-full": true,
}

// ConnInfo is the parsed, validated view of a target database. It contains
// everything pgx and pg_dump need and nothing else.
type ConnInfo struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string // validated libpq sslmode; default "prefer"
	Timeout  string // connect_timeout seconds, empty = driver default
	AppName  string
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9.\-_]+$`)

// ParseURI validates and decomposes a postgres:// connection URI. The
// password never leaves this struct except to PGPASSFILE / pgx.
func ParseURI(raw string) (*ConnInfo, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("parse connection URI: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, errors.New("URI scheme must be postgres:// or postgresql://")
	}
	if u.Host == "" {
		return nil, errors.New("URI is missing the host")
	}
	host := u.Hostname()
	if !hostRe.MatchString(host) && host != "localhost" {
		return nil, fmt.Errorf("invalid host %q", host)
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	} else if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return nil, fmt.Errorf("invalid port %q", port)
	}
	if u.User == nil || u.User.Username() == "" {
		return nil, errors.New("URI is missing the user")
	}
	pw, _ := u.User.Password()
	if u.Path == "" || u.Path == "/" {
		return nil, errors.New("URI is missing the database name")
	}
	dbname := strings.TrimPrefix(u.Path, "/")

	q := u.Query()
	ci := &ConnInfo{Host: host, Port: port, User: u.User.Username(), Password: pw, DBName: dbname, SSLMode: "prefer"}
	for key, vals := range q {
		if len(vals) != 1 {
			return nil, fmt.Errorf("parameter %q must appear exactly once", key)
		}
		v := vals[0]
		switch key {
		case "sslmode":
			if !sslmodes[v] {
				return nil, fmt.Errorf("invalid sslmode %q", v)
			}
			ci.SSLMode = v
		case "connect_timeout":
			if n, err := strconv.Atoi(v); err != nil || n < 0 || n > 600 {
				return nil, fmt.Errorf("invalid connect_timeout %q", v)
			}
			ci.Timeout = v
		case "application_name":
			ci.AppName = v
		default:
			return nil, fmt.Errorf("unsupported connection parameter %q (only sslmode, connect_timeout, application_name are allowed)", key)
		}
	}
	return ci, nil
}

// DSN returns the libpq keyword/value conninfo WITHOUT any password — the
// password travels exclusively via PGPASSFILE (protocol A). Values are
// single-quoted and escaped per libpq conninfo rules.
func (c *ConnInfo) DSN() string {
	esc := func(v string) string {
		if v == "" {
			return v
		}
		if !strings.ContainsAny(v, " \t\n\\'") {
			return v
		}
		return "'" + strings.ReplaceAll(strings.ReplaceAll(v, "\\", "\\\\"), "'", "\\'") + "'"
	}
	parts := []string{
		"host=" + esc(c.Host),
		"port=" + esc(c.Port),
		"user=" + esc(c.User),
		"dbname=" + esc(c.DBName),
		"sslmode=" + esc(c.SSLMode),
	}
	if c.Timeout != "" {
		parts = append(parts, "connect_timeout="+esc(c.Timeout))
	}
	if c.AppName != "" {
		parts = append(parts, "application_name="+esc(c.AppName))
	}
	return strings.Join(parts, " ")
}

// KeywordView is the credential-free summary safe for logs and APIs.
func (c *ConnInfo) KeywordView() string {
	return fmt.Sprintf("%s:%s/%s (user %s, sslmode %s)", c.Host, c.Port, c.DBName, c.User, c.SSLMode)
}

var pgVersionRe = regexp.MustCompile(`PostgreSQL (\d+)\.`)

// ServerMajor extracts the major version from a server version string such
// as "PostgreSQL 18.4 (Debian ...)".
func ServerMajor(version string) (int, error) {
	m := pgVersionRe.FindStringSubmatch(version)
	if m == nil {
		return 0, fmt.Errorf("unrecognized server version string %q", version)
	}
	return strconv.Atoi(m[1])
}

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

// CollectDependencies gathers the restore-relevant facts recorded in the
// manifest (dev-plan P2 task 6, protocol E): extensions, referenced roles,
// large objects, foreign tables.
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

	// Roles that own objects in user schemas: restoring into a fresh cluster
	// needs these roles to exist first (manifest dependency, protocol E).
	rows, err = conn.Query(ctx, `
        SELECT DISTINCT r.rolname
        FROM pg_class c
        JOIN pg_roles r ON r.oid = c.relowner
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
        UNION
        SELECT DISTINCT r.rolname
        FROM pg_proc p
        JOIN pg_roles r ON r.oid = p.proowner
        JOIN pg_namespace n ON n.oid = p.pronamespace
        WHERE n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
        ORDER BY 1`)
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

// Extension and Dependencies are manifest (protocol E) payloads.
type Extension struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Dependencies struct {
	Extensions       []Extension `json:"extensions"`
	Roles            []string    `json:"roles"`
	HasLargeObjects  bool        `json:"hasLargeObjects"`
	HasForeignTables bool        `json:"hasForeignTables"`
}

// ClassifyError maps a connection/query failure to an error class using
// libpq/pgx message patterns (dev-plan seven classes).
func ClassifyError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	cls := ClassUnknown
	switch {
	case strings.Contains(msg, "password authentication failed"),
		strings.Contains(msg, "no password supplied"),
		strings.Contains(msg, "authentication failed"):
		cls = ClassAuth
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "no such host"),
		strings.Contains(msg, "connection timed out"),
		strings.Contains(msg, "network unreachable"),
		strings.Contains(msg, "host is unreachable"),
		strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "dial tcp"):
		cls = ClassNetwork
	case strings.Contains(msg, "permission denied"),
		strings.Contains(msg, "privilege"):
		cls = ClassPermission
	case strings.Contains(msg, "no such file or directory") && strings.Contains(msg, ".sock"):
		cls = ClassNetwork
	case strings.Contains(msg, "unsupported ssl"),
		strings.Contains(msg, "ssl is not enabled"),
		strings.Contains(msg, "tls"):
		cls = ClassNetwork
	}
	if cls == ClassUnknown {
		return err
	}
	return fmt.Errorf("%w", &Classified{Class: cls, Err: err})
}

// Classified attaches an error class to an underlying failure.
type Classified struct {
	Class ErrClass
	Err   error
}

func (e *Classified) Error() string { return e.Err.Error() }
func (e *Classified) Unwrap() error { return e.Err }
