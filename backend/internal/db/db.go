// Package db manages the SQLite metadata store: connection with explicit
// PRAGMAs on every connection, OS-level advisory locking for single-instance
// enforcement, pre-migration consistent backups, and goose migrations.
//
// The store holds control-plane data only (config, tasks, manifests); backup
// artifacts live in the user's object storage (dev-plan §0).
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	_ "modernc.org/sqlite" // driver "sqlite"
)

// ErrLocked is returned when another supabackup instance already holds the
// advisory lock on this data directory.
var ErrLocked = errors.New("another supabackup instance is using this data directory")

const lockFileName = "supabackup.lock"

// Store wraps the SQLite handle plus the advisory-lock file descriptor.
type Store struct {
	DB       *sql.DB
	lockFile *os.File
	dataDir  string
}

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
// short transactions, so a race ends with exactly one winner.
func OpenForCLI(dataDir string) (*Store, error) {
	return open(dataDir, false)
}

func open(dataDir string, takeLock bool) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
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

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate",
		filepath.Join(dataDir, "supabackup.db"))
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
	return &Store{DB: database, lockFile: f, dataDir: dataDir}, nil
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
