package pgclient

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgxAllowedKeys is the second defense line behind URI whitelisting: pgx
// itself refuses any connstring key outside this set (round-1 review P1-03).
var pgxAllowedKeys = []string{
	"host", "port", "user", "password", "dbname", "sslmode",
	"connect_timeout", "application_name",
}

// pgxConfig builds a pgx config from the shared ConnInfo so pgx and pg_dump
// see identical semantics (dev-plan P2 task 5). The password is injected
// programmatically AFTER parse (never into a printable connstring), and the
// allowed-key list keeps hostile parameters out even if the whitelist above
// ever regresses.
func pgxConfig(c *ConnInfo) (*pgx.ConnConfig, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s dbname=%s sslmode=%s",
		QuoteConninfo(c.Host),
		QuoteConninfo(c.Port),
		QuoteConninfo(c.User),
		QuoteConninfo(c.DBName),
		QuoteConninfo(c.SSLMode),
	)
	if c.Timeout != "" {
		dsn += " connect_timeout=" + QuoteConninfo(c.Timeout)
	}
	opts := pgx.ParseConfigOptions{
		ParseConfigOptions: pgconn.ParseConfigOptions{
			ConnStringAllowedKeys: pgxAllowedKeys,
		},
	}
	cfg, err := pgx.ParseConfigWithOptions(dsn, opts)
	if err != nil {
		return nil, err
	}
	cfg.Password = c.Password
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["application_name"] = "supabackup"
	return cfg, nil
}

func pgxConnect(ctx context.Context, cfg *pgx.ConnConfig) (*pgx.Conn, error) {
	return pgx.ConnectConfig(ctx, cfg)
}
