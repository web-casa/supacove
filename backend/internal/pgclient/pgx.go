package pgclient

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// pgxConfig builds a pgx config from the shared ConnInfo so pgx and pg_dump
// see identical semantics (dev-plan P2 task 5). TLS: sslmode flows straight
// through; verify-full is the documented recommendation for remote targets,
// and anything weaker stays visible in the API status.
func pgxConfig(c *ConnInfo) (*pgx.ConnConfig, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		pgxQuoteString(c.Host),
		pgxQuoteString(c.Port),
		pgxQuoteString(c.User),
		pgxQuoteString(c.Password),
		pgxQuoteString(c.DBName),
		pgxQuoteString(c.SSLMode),
	)
	if c.Timeout != "" {
		dsn += " connect_timeout=" + c.Timeout
	}
	if c.AppName != "" {
		dsn += " application_name=" + pgxQuoteString(c.AppName+"_supabackup")
	} else {
		dsn += " application_name=supabackup"
	}
	return pgx.ParseConfig(dsn)
}

func pgxConnect(ctx context.Context, cfg *pgx.ConnConfig) (*pgx.Conn, error) {
	return pgx.ConnectConfig(ctx, cfg)
}

// pgxQuoteString quotes a keyword/value string element per libpq rules
// (same escaping the dumper uses for its conninfo).
func pgxQuoteString(v string) string {
	if !strings.ContainsAny(v, " \t\n\\'") {
		return v
	}
	return "'" + strings.ReplaceAll(strings.ReplaceAll(v, "\\", "\\\\"), "'", "\\'") + "'"
}

var _ = context.Background
