package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddMigrationContext(upJobsDurationSecs, downJobsDurationSecs)
}

// upJobsDurationSecs repairs jobs.duration_secs for databases that were
// migrated by an intermediate binary whose 0004 Up section had been
// retro-edited (review round-2, migration audit): such upgrades ran the
// ORIGINAL 0004 without the column and never receive it afterwards, so
// stats queries fail with "no such column". Fresh installs already have it
// (the corrected 0004), making this a conditional no-op there — the same
// convergence pattern as 0003_auth_generation.
func upJobsDurationSecs(ctx context.Context, tx *sql.Tx) error {
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('jobs') WHERE name = 'duration_secs'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil // column already present; record the version as applied
	}
	_, err := tx.ExecContext(ctx,
		`ALTER TABLE jobs ADD COLUMN duration_secs REAL NOT NULL DEFAULT 0`)
	return err
}

// downJobsDurationSecs is deliberately a NO-OP (round-3 R3-P2-03): on
// databases where 0013 repaired a missing column this migration does not
// OWN duration_secs — the current 0004 defines it. A rollback that dropped
// it would break v12 code that queries the column and destroy recorded
// data. Rollback past this point is therefore not supported for that
// column; fresh installs keep it via 0004 regardless.
func downJobsDurationSecs(ctx context.Context, tx *sql.Tx) error {
	return nil
}
