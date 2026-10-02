package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(upAuthGeneration, downAuthGeneration)
}

// upAuthGeneration adds users.auth_generation conditionally so every
// historical database shape upgrades cleanly (review round 2, R2-P1-01):
//   - original 0001 (no column): the column is added;
//   - the briefly-shipped modified 0001 that already had the column: no-op;
//   - a database already at the final schema: no-op.
func upAuthGeneration(ctx context.Context, tx *sql.Tx) error {
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name = 'auth_generation'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil // column already present; record the version as applied
	}
	_, err := tx.ExecContext(ctx,
		`ALTER TABLE users ADD COLUMN auth_generation INTEGER NOT NULL DEFAULT 0`)
	return err
}

func downAuthGeneration(ctx context.Context, tx *sql.Tx) error {
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name = 'auth_generation'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `ALTER TABLE users DROP COLUMN auth_generation`)
	return err
}
