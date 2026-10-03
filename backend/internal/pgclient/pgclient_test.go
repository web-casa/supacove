package pgclient

import (
	"strings"
	"testing"
)

func TestParseURIAcceptsAndNormalizes(t *testing.T) {
	ci, err := ParseURI("postgres://admin:se%3Acret@db.example.com:5433/appdb?sslmode=verify-full&connect_timeout=10")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ci.Host != "db.example.com" || ci.Port != "5433" || ci.User != "admin" ||
		ci.Password != "se:cret" || ci.DBName != "appdb" || ci.SSLMode != "verify-full" || ci.Timeout != "10" {
		t.Fatalf("unexpected ConnInfo: %+v", ci)
	}
	dsn := ci.DSN()
	if strings.Contains(dsn, "se:cret") || strings.Contains(dsn, "secret") {
		t.Fatal("DSN must never contain the password")
	}
	for _, want := range []string{"host='db.example.com'", "port='5433'", "user='admin'", "dbname='appdb'", "sslmode='verify-full'"} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("DSN missing %q: %s", want, dsn)
		}
	}
}

func TestParseURIRejectsDisallowed(t *testing.T) {
	for _, uri := range []string{
		"postgres://u:p@h/db?passfile=/etc/passwd",
		"postgres://u:p@h/db?service=prod",
		"postgres://u:p@h/db?sslkey=/tmp/key",
		"postgres://u:p@h/db?options=-c%20statement_timeout%3D0",
		"mysql://u:p@h/db",
		"postgres://h/db",  // no user
		"postgres://u:p@h", // no dbname
		"postgres://u:p@h/db?sslmode=bogus",
		"postgres://u:p@h/db?connect_timeout=99999",
		"", "   ",
	} {
		if _, err := ParseURI(uri); err == nil {
			t.Errorf("URI must be rejected: %q", uri)
		}
	}
}

func TestParseURIWhitelistedValues(t *testing.T) {
	for _, uri := range []string{
		"postgres://u:p@h/db?sslmode=disable",
		"postgres://u:p@h/db?sslmode=require",
		"postgres://u:p@h/db?sslmode=verify-ca",
		"postgres://u:p@h/db?sslmode=verify-full",
		"postgres://u:p@h/db?sslmode=prefer&application_name=myapp",
		"postgresql://u@localhost/mydb", // local target: implicit prefer is fine
	} {
		if _, err := ParseURI(uri); err != nil {
			t.Errorf("valid URI rejected: %q: %v", uri, err)
		}
	}
}

func TestDSNEscapesSpecialCharacters(t *testing.T) {
	ci := &ConnInfo{Host: "h", Port: "5432", User: "u", DBName: `db name\with'special`, SSLMode: "prefer"}
	dsn := ci.DSN()
	// The escaped dbname must be single-quoted with escaped inner quotes.
	if !strings.Contains(dsn, `dbname='db name\\with\'special'`) {
		t.Fatalf("dbname not escaped per libpq rules: %s", dsn)
	}
}

func TestClassifyErrorPatterns(t *testing.T) {
	cases := map[string]ErrClass{
		"pq: password authentication failed for user app":                                         ClassAuth,
		"failed to connect to `host=1.2.3.4`: dial tcp 1.2.3.4:5432: connect: connection refused": ClassNetwork,
		"pq: permission denied for table secrets":                                                 ClassPermission,
		"something completely different":                                                          ClassUnknown,
	}
	for msg, want := range cases {
		err := ClassifyError(testErr(msg))
		got := ClassUnknown
		var c *Classified
		if asC(err, &c) {
			got = c.Class
		}
		if got != want {
			t.Errorf("classify(%q) = %q, want %q", msg, got, want)
		}
	}
}

type testErrString string

func (e testErrString) Error() string { return string(e) }

func testErr(s string) error { return testErrString(s) }

func asC(err error, target **Classified) bool {
	for err != nil {
		if c, ok := err.(*Classified); ok {
			*target = c
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestServerMajorParsing(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    int
	}{
		{"PostgreSQL 18.4 (Debian 18.4-1)", 18},
		{"PostgreSQL 14.11", 14},
		{"PostgreSQL 17.2 on x86_64", 17},
	} {
		got, err := ServerMajor(tc.version)
		if err != nil || got != tc.want {
			t.Errorf("ServerMajor(%q) = %d, %v; want %d", tc.version, got, err, tc.want)
		}
	}
	if _, err := ServerMajor("not a version"); err == nil {
		t.Error("unrecognized version must error")
	}
}
