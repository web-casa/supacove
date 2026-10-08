// Package jobs — destination CRUD (BYOS configuration) with encrypted
// secrets (protocol B) and the backend factory (round-trip through
// internal/storage).
package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudfan/supabackup/backend/internal/crypto"
	"github.com/cloudfan/supabackup/backend/internal/storage"
)

// DestinationView is the API-safe destination (no secrets).
type DestinationView struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Platform       string `json:"platform"`
	Endpoint       string `json:"endpoint"`
	Region         string `json:"region"`
	Bucket         string `json:"bucket"`
	Prefix         string `json:"prefix"`
	VerifyReadback bool   `json:"verifyReadback"`
	KeepRemote     int    `json:"keepRemote"`
	KeepDays       int    `json:"keepDays"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}

// Destination is the full destination including the DECRYPTED secret —
// exists only inside the process, never serialized to the API.
type Destination struct {
	ID             int64
	Name           string
	Platform       string
	Endpoint       string
	Region         string
	Bucket         string
	Prefix         string
	AccessKey      string
	SecretKey      string
	VerifyReadback bool
	KeepRemote     int
	KeepDays       int
	CreatedAt      int64
	UpdatedAt      int64
}

// View returns the API-safe projection.
func (d *Destination) View() *DestinationView {
	return &DestinationView{
		ID: d.ID, Name: d.Name, Platform: d.Platform, Endpoint: d.Endpoint,
		Region: d.Region, Bucket: d.Bucket, Prefix: d.Prefix,
		VerifyReadback: d.VerifyReadback, KeepRemote: d.KeepRemote,
		KeepDays: d.KeepDays, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

// StorageConfig builds the storage-layer configuration.
func (d *Destination) StorageConfig() storage.Config {
	return storage.Config{
		Platform:  d.Platform,
		Endpoint:  d.Endpoint,
		Region:    d.Region,
		Bucket:    d.Bucket,
		Prefix:    d.Prefix,
		AccessKey: d.AccessKey,
		SecretKey: d.SecretKey,
	}
}

// Secrets returns the credential values for redaction.
func (d *Destination) Secrets() []string {
	return []string{d.SecretKey, d.AccessKey}
}

const destColumns = `id, name, platform, endpoint, region, bucket, prefix,
	access_key, secret_encrypted, verify_readback, keep_remote, keep_days,
	created_at, updated_at`

var ErrDestinationNotFound = errors.New("destination not found")
var ErrDestinationNameExists = errors.New("destination name already exists")

// ErrDestinationInUse refuses deleting a destination that databases still
// point at: their backups would fail from then on.
var ErrDestinationInUse = errors.New("destination is still assigned to a database")

// CreateDestination validates, live-tests, then stores a new destination.
// The live diagnostic test runs BEFORE the INSERT: an unreachable or
// misconfigured destination is never recorded.
func (r *Runner) CreateDestination(ctx context.Context, in Destination, verify bool) (int64, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return 0, errors.New("name must be 1-100 characters")
	}
	cfg := in.StorageConfig()
	if err := cfg.Validate(); err != nil {
		return 0, err
	}
	if verify {
		store, err := storage.New(ctx, cfg, r.log)
		if err != nil {
			return 0, err
		}
		if err := store.DiagnosticTest(ctx); err != nil {
			return 0, fmt.Errorf("diagnostic test failed: %w", err)
		}
	}
	enc, err := crypto.Encrypt(r.key, []byte(in.SecretKey))
	if err != nil {
		return 0, err
	}
	var id int64
	err = r.authDB.QueryRowContext(ctx,
		`INSERT INTO destinations (name, platform, endpoint, region, bucket, prefix,
		    access_key, secret_encrypted, verify_readback, keep_remote, keep_days,
		    created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, strftime('%s','now'), strftime('%s','now'))
		 RETURNING id`,
		in.Name, cfg.Platform, cfg.Endpoint, cfg.Region, cfg.Bucket, cfg.Prefix,
		in.AccessKey, enc, boolToInt(in.VerifyReadback),
		clampKeep(in.KeepRemote, 1), maxInt(in.KeepDays, 0)).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "idx_destinations_name_live") ||
			strings.Contains(err.Error(), "UNIQUE constraint failed: destinations.name") {
			return 0, ErrDestinationNameExists
		}
		return 0, err
	}
	return id, nil
}

// GetDestination loads one destination WITH the decrypted secret.
func (r *Runner) GetDestination(ctx context.Context, id int64) (*Destination, error) {
	var d Destination
	var enc string
	var verify int
	err := r.authDB.QueryRowContext(ctx,
		`SELECT `+destColumns+` FROM destinations WHERE id = ? AND deleted_at IS NULL`, id).
		Scan(&d.ID, &d.Name, &d.Platform, &d.Endpoint, &d.Region, &d.Bucket, &d.Prefix,
			&d.AccessKey, &enc, &verify, &d.KeepRemote, &d.KeepDays, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDestinationNotFound
	}
	if err != nil {
		return nil, err
	}
	plain, err := crypto.Decrypt(r.key, enc)
	if err != nil {
		return nil, errors.New("destination secret unreadable — the master secret changed; re-create the destination")
	}
	d.SecretKey = string(plain)
	d.VerifyReadback = verify == 1
	return &d, nil
}

// ListDestinations returns all live destinations (no secrets).
func (r *Runner) ListDestinations(ctx context.Context) ([]*DestinationView, error) {
	rows, err := r.authDB.QueryContext(ctx, `
		SELECT id, name, platform, endpoint, region, bucket, prefix,
		       verify_readback, keep_remote, keep_days, created_at, updated_at
		FROM destinations WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DestinationView
	for rows.Next() {
		d := &DestinationView{}
		if err := rows.Scan(&d.ID, &d.Name, &d.Platform, &d.Endpoint, &d.Region,
			&d.Bucket, &d.Prefix, &d.VerifyReadback, &d.KeepRemote, &d.KeepDays,
			&d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDestination soft-deletes, refusing while an upload for this
// destination is in flight (round-2 phase-2 review pattern: atomic guard)
// or while a database is still assigned to it. Both guards sit in the same
// UPDATE, so a concurrent assignment cannot slip in between.
func (r *Runner) DeleteDestination(ctx context.Context, id int64) error {
	res, err := r.authDB.ExecContext(ctx, `
		UPDATE destinations SET
		  name = name || ' (deleted #' || id || '-' || lower(hex(randomblob(6))) || ')',
		  deleted_at = strftime('%s','now'), updated_at = strftime('%s','now')
		WHERE id = ? AND deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM databases
		                  WHERE databases.destination_id = destinations.id
		                  AND databases.deleted_at IS NULL)
		  AND NOT EXISTS (SELECT 1 FROM jobs
		                  WHERE jobs.destination_id = destinations.id
		                  AND jobs.status IN ('pending','running'))`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var live, assigned int
		if err := r.authDB.QueryRowContext(ctx, `
			SELECT (SELECT COUNT(*) FROM destinations WHERE id = ? AND deleted_at IS NULL),
			       (SELECT COUNT(*) FROM databases WHERE destination_id = ? AND deleted_at IS NULL)`,
			id, id).Scan(&live, &assigned); err == nil && live == 1 {
			if assigned > 0 {
				return ErrDestinationInUse
			}
			return errors.New("an upload for this destination is in flight")
		}
		return ErrDestinationNotFound
	}
	return nil
}

// DestinationForDatabase resolves the destination assigned to a database,
// or nil when none is assigned (local-only mode).
func (r *Runner) DestinationForDatabase(ctx context.Context, dbID int64) (*Destination, error) {
	var destID sql.NullInt64
	if err := r.authDB.QueryRowContext(ctx,
		`SELECT destination_id FROM databases WHERE id = ? AND deleted_at IS NULL`, dbID).
		Scan(&destID); err != nil {
		return nil, err
	}
	if !destID.Valid {
		return nil, nil
	}
	return r.GetDestination(ctx, destID.Int64)
}

// BuildBackend constructs (and caches) the storage backend for a
// destination. Caching is keyed by destination ID; destination rows are
// create/delete only (no edit in v1), so a cached client never goes stale.
// Tests can replace the factory via SetBackendFactory.
func (r *Runner) BuildBackend(ctx context.Context, dest *Destination) (storage.Backend, error) {
	r.destMu.Lock()
	if b, ok := r.destBackends[dest.ID]; ok {
		r.destMu.Unlock()
		return b, nil
	}
	r.destMu.Unlock()

	// Double-check cache after build: a concurrent request may have
	// published first (round-3 review P1-09 data race on backendFactory).
	r.destMu.Lock()
	if b, ok := r.destBackends[dest.ID]; ok {
		r.destMu.Unlock()
		return b, nil
	}
	r.destMu.Unlock()

	factory := r.backendFactory
	if factory == nil {
		return nil, errors.New("backend factory not initialized")
	}
	b, err := factory(ctx, dest)
	if err != nil {
		return nil, err
	}
	r.destMu.Lock()
	if existing, ok := r.destBackends[dest.ID]; ok {
		r.destMu.Unlock()
		return existing, nil
	}
	r.destBackends[dest.ID] = b
	r.destMu.Unlock()
	return b, nil
}

// SetBackendFactory overrides backend construction. Must be called before
// Start (round-3 review P1-09: concurrent first access raced on an
// uninitialized factory).
func (r *Runner) SetBackendFactory(f func(ctx context.Context, dest *Destination) (storage.Backend, error)) {
	r.destMu.Lock()
	defer r.destMu.Unlock()
	r.backendFactory = f
}

// AssignDestination attaches (or clears) a destination on a database.
// Refused while a job for the database is active.
func (r *Runner) AssignDestination(ctx context.Context, dbID, destID int64) error {
	if destID != 0 {
		if _, err := r.GetDestination(ctx, destID); err != nil {
			return err
		}
	}
	var destParam any // NULL clears the assignment (round-3 review P2-01)
	if destID != 0 {
		destParam = destID
	}
	// The destination must still be live WHEN the row is written: the check
	// above and this UPDATE are separate statements, and a delete landing
	// between them would otherwise bind the database to a deleted
	// destination. Clearing (NULL) needs no such condition.
	res, err := r.authDB.ExecContext(ctx, `
		UPDATE databases SET destination_id = ?1, updated_at = strftime('%s','now')
		WHERE id = ?2 AND deleted_at IS NULL
		  AND (?1 IS NULL OR EXISTS (SELECT 1 FROM destinations
		                             WHERE destinations.id = ?1
		                             AND destinations.deleted_at IS NULL))
		  AND NOT EXISTS (SELECT 1 FROM jobs
		                  WHERE jobs.database_id = databases.id
		                  AND jobs.status IN ('pending','running'))`, destParam, dbID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if destID != 0 {
			if _, gerr := r.GetDestination(ctx, destID); gerr != nil {
				return gerr // deleted in the meantime
			}
		}
		return errors.New("database not found or a job is active")
	}
	return nil
}

func clampKeep(v, min int) int {
	if v < min {
		return min
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
