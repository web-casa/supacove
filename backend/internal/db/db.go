// Package db manages the SQLite metadata store: connection with explicit
// PRAGMAs on every connection, OS-level advisory locking for single-instance
// enforcement, pre-migration consistent backups, and goose migrations.
//
// The store holds control-plane data only (config, tasks, manifests); backup
// artifacts live in the user's object storage (dev-plan §0).
package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"syscall"

	_ "modernc.org/sqlite" // driver "sqlite"

	// Go migrations (mixed with the embedded SQL files) self-register via init().
	_ "github.com/cloudfan/supabackup/backend/internal/db/migrations"
)

// ErrLocked is returned when another supabackup instance already holds the
// advisory lock on this data directory.
var ErrLocked = errors.New("another supabackup instance is using this data directory")

// ErrNoLock is returned by Migrate when called on a lockless (CLI) store —
// migrations must never run beside a serving instance (review P1-06).
var ErrNoLock = errors.New("migrations require the exclusive instance lock; CLI commands must not migrate")

const lockFileName = "supabackup.lock"

// Store wraps the SQLite handle plus the advisory-lock file descriptor.
type Store struct {
	DB       *sql.DB
	lockFile *os.File
	dataDir  string
	// migrations is the goose source; a field so tests can inject an overlay
	// FS with an extra pending migration to exercise the upgrade path.
	migrations fs.FS
}

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// Open acquires the exclusive advisory lock (server mode) and opens SQLite.
// The lock prevents two supabackup servers from sharing a data directory.
func Open(dataDir string) (*Store, error) {
	return open(dataDir, true)
}

// OpenForCLI opens SQLite without the advisory lock. CLI utility commands
// (bootstrap, reset-password) must run while the server holds LOCK_EX —
// flock's shared mode cannot coexist with an exclusive holder, so utilities
// do not participate in the lock at all. Cross-process write safety comes
// from SQLite WAL mode plus busy_timeout; bootstrap/reset-password run in
// short transactions, so a race ends with exactly one winner. CLI stores are
// forbidden from running migrations (see Migrate).
func OpenForCLI(dataDir string) (*Store, error) {
	return open(dataDir, false)
}

func open(dataDir string, takeLock bool) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	// Tighten an existing directory and pre-create the DB file owner-only:
	// MkdirAll does not chmod, and the driver would otherwise create db/wal/shm
	// under the process umask (review P1-05).
	if err := os.Chmod(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("tighten data dir permissions: %w", err)
	}
	for _, name := range []string{"supabackup.db", "supabackup.db-wal", "supabackup.db-shm"} {
		p := filepath.Join(dataDir, name)
		if err := os.Chmod(p, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("tighten %s permissions: %w", name, err)
		}
	}
	dbPath := filepath.Join(dataDir, "supabackup.db")
	if pf, err := os.OpenFile(dbPath, os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_ = pf.Close()
		_ = os.Chmod(dbPath, 0o600) // OpenFile already created it 0600; umask belt
	}

	var f *os.File
	if takeLock {
		lockPath := filepath.Join(dataDir, lockFileName)
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open lock file: %w", err)
		}
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			file.Close()
			return nil, ErrLocked
		}
		f = file
	}

	abs, err := filepath.Abs(filepath.Join(dataDir, "supabackup.db"))
	if err != nil {
		closeLock(f)
		return nil, err
	}
	// Build the DSN via net/url so special characters in the path (?, #, %,
	// spaces, quotes) cannot alter URI parameters or alias two data directories
	// onto one database (review P1-07).
	dsn := (&url.URL{Scheme: "file", Path: abs}).String() + "?" + url.Values{
		"_pragma": []string{
			"busy_timeout(5000)",
			"foreign_keys(1)",
			"journal_mode(WAL)",
			"synchronous(NORMAL)",
		},
		"_txlock": []string{"immediate"},
	}.Encode()
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		closeLock(f)
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single writer process; a small pool keeps write transactions short.
	database.SetMaxOpenConns(4)
	database.SetMaxIdleConns(4)
	if err := database.Ping(); err != nil {
		database.Close()
		closeLock(f)
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return &Store{DB: database, lockFile: f, dataDir: dataDir, migrations: embeddedMigrations}, nil
}

// Close releases the lock and closes the database.
func (s *Store) Close() error {
	err := s.DB.Close()
	closeLock(s.lockFile)
	return err
}

// closeLock releases the advisory lock if this store holds one. The OS also
// releases it automatically when the process dies, so a stale lock file is
// never a false positive (dev-plan P1: OS advisory lock, not lock-file
// existence checks).
func closeLock(f *os.File) {
	if f != nil {
		f.Close()
	}
}
