// Package pgclient tests and inspects target PostgreSQL servers, and shares
// its connection semantics with the pg_dump executor so both see the same
// host/user/db/TLS view (dev-plan P2 task 5).
package pgclient

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ErrClass classifies failures into the seven job error classes plus unknown
// (dev-plan P2 task 4).
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
// P2 task 2). pgx additionally enforces ConnStringAllowedKeys as a second
// line of defense.
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
	SSLMode  string // validated libpq sslmode
	Timeout  string // connect_timeout seconds, empty = driver default
	AppName  string
	// ExplicitTLS records whether the user chose sslmode themselves.
	// Registration of NON-LOCAL targets without an explicit sslmode is
	// rejected (round-1 review P1-09: no silent plaintext/prefer fallback).
	ExplicitTLS bool
	IsLocal     bool
}

var hostLabelRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.\-]*[A-Za-z0-9])?$`)

// classifyHost accepts IPv4, IPv6 (from url.Hostname(), brackets already
// removed) and plain DNS names; rejects everything else. Round-1 review
// P2-09: legal IPv6 literals must not be refused.
func classifyHost(host string) error {
	if host == "" {
		return errors.New("empty host")
	}
	if strings.Contains(host, "%") || strings.Contains(host, ",") {
		return errors.New("zone identifiers and multi-host lists are not supported")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" {
			return errors.New("IPv6 zone identifiers are not supported")
		}
		return nil
	}
	if !hostLabelRe.MatchString(host) {
		return fmt.Errorf("invalid host %q", host)
	}
	return nil
}

// ParseURI validates and decomposes a postgres:// connection URI. The
// password never leaves this struct except to PGPASSFILE / pgx. Query parse
// errors are propagated, never silently downgraded (round-1 review P1-09).
func ParseURI(raw string) (*ConnInfo, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("connection URI is empty")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		// Never echo the input back: url.Error embeds the full URI with
		// credentials (round-1 review P1-02).
		return nil, errors.New("connection URI is not a valid URL")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, errors.New("URI scheme must be postgres:// or postgresql://")
	}
	if u.RawQuery != "" {
		if _, qerr := url.ParseQuery(u.RawQuery); qerr != nil {
			return nil, errors.New("connection URI query string is malformed")
		}
	}
	if u.Fragment != "" {
		return nil, errors.New("connection URI must not contain a fragment")
	}
	if u.Host == "" {
		return nil, errors.New("URI is missing the host")
	}
	host := u.Hostname()
	if err := classifyHost(host); err != nil {
		return nil, err
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
	if err := rejectConninfoControlBytes(host, port, u.User.Username(), pw, dbname); err != nil {
		return nil, err
	}

	q := u.Query()
	ci := &ConnInfo{Host: host, Port: port, User: u.User.Username(), Password: pw, DBName: dbname}
	explicitTLS := false
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
			explicitTLS = true
		case "connect_timeout":
			if n, err := strconv.Atoi(v); err != nil || n < 0 || n > 600 {
				return nil, fmt.Errorf("invalid connect_timeout %q", v)
			}
			ci.Timeout = v
		case "application_name":
			if len(v) > 100 {
				return nil, errors.New("application_name too long")
			}
			if err := rejectConninfoControlBytes(v); err != nil {
				return nil, fmt.Errorf("application_name: %w", err)
			}
			ci.AppName = v
		default:
			return nil, fmt.Errorf("unsupported connection parameter %q (only sslmode, connect_timeout, application_name are allowed)", key)
		}
	}
	if ci.SSLMode == "" {
		// Round-1 review P1-09: remote targets must choose TLS semantics
		// explicitly; localhost keeps the comfortable prefer default.
		ci.IsLocal = host == "localhost" || isLoopbackIP(host)
		if ci.IsLocal {
			ci.SSLMode = "prefer"
		} else {
			return nil, errors.New(
				"remote connections must set sslmode explicitly (recommend verify-full; allow/prefer/require/verify-ca/disable are accepted for explicit choices)")
		}
	}
	ci.ExplicitTLS = explicitTLS
	return ci, nil
}

func isLoopbackIP(host string) bool {
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// rejectConninfoControlBytes refuses characters that could break libpq
// keyword/value parsing or the PGPASSFILE line format, even after escaping:
// NUL and every byte libpq treats as whitespace (round-1 review P1-03).
func rejectConninfoControlBytes(fields ...string) error {
	for _, f := range fields {
		if strings.ContainsAny(f, "\x00\r\n\v\f") {
			return errors.New("connection fields must not contain control characters (NUL, CR, LF, VT, FF)")
		}
	}
	return nil
}

// QuoteConninfo renders one keyword/value element per libpq conninfo rules:
// every value is single-quoted; backslash and single quote are escaped. All
// values are quoted — including empty strings, whose unquoted form would be
// swallowed as a missing token (round-1 review P2-02).
func QuoteConninfo(v string) string {
	q := strings.ReplaceAll(v, "\\", "\\\\")
	q = strings.ReplaceAll(q, "'", "\\'")
	return "'" + q + "'"
}

// DSN returns the libpq keyword/value conninfo WITHOUT any password — the
// password travels exclusively via PGPASSFILE (protocol A).
func (c *ConnInfo) DSN() string {
	parts := []string{
		"host=" + QuoteConninfo(c.Host),
		"port=" + QuoteConninfo(c.Port),
		"user=" + QuoteConninfo(c.User),
		"dbname=" + QuoteConninfo(c.DBName),
		"sslmode=" + QuoteConninfo(c.SSLMode),
	}
	if c.Timeout != "" {
		parts = append(parts, "connect_timeout="+QuoteConninfo(c.Timeout))
	}
	if c.AppName != "" {
		parts = append(parts, "application_name="+QuoteConninfo(c.AppName))
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
		return 0, fmt.Errorf("unrecognized server version string")
	}
	return strconv.Atoi(m[1])
}
