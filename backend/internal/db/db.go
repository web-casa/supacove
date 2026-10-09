// Package db manages the SQLite metadata store: connection with explicit
// PRAGMAs on every connection, OS-level advisory locking for single-instance
// enforcement, one-shot migration of the pre-rename (supabackup) file names,
// pre-migration consistent backups, and goose migrations.
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
	"strings"
	"syscall"

	_ "modernc.org/sqlite" // driver "sqlite"

	// Go migrations (mixed with the embedded SQL files) self-register via init().
	_ "github.com/web-casa/supacove/backend/internal/db/migrations"
)

// ErrLocked is returned when another supacove instance already holds the
// advisory lock on this data directory.
var ErrLocked = errors.New("another supacove instance is using this data directory")

// ErrLegacyInstance is returned when a pre-rename (supabackup) binary still
// holds the legacy advisory lock: the generations must never run beside each
// other on one data directory.
var ErrLegacyInstance = errors.New("a legacy supabackup instance is still running on this data directory; stop it (and any legacy CLI commands) before starting supacove")

// ErrMigrationPending is returned by the lockless (CLI) open when the data
// directory still uses the pre-rename file names: only a locked open may
// migrate them.
var ErrMigrationPending = errors.New("data directory still uses the pre-rename supabackup file names; start the server once (it migrates them) with the legacy instance stopped")

// ErrInconsistentState is returned when the data directory mixes current and
// legacy files in a combination no automatic action may resolve.
var ErrInconsistentState = errors.New("inconsistent mix of supabackup and supacove files in the data directory")

// ErrNoLock is returned by Migrate when called on a lockless (CLI) store —
// migrations must never run beside a serving instance (review P1-06).
var ErrNoLock = errors.New("migrations require the exclusive instance lock; CLI commands must not migrate")

// SQLite file names, current and pre-rename ("legacy"). A locked Open
// migrates legacy names once (see migrateLegacyFiles); the legacy lock file
// is deliberately NOT renamed — a legacy binary flocks it, so holding it for
// the whole process lifetime is what keeps the two generations mutually
// exclusive during the compatibility window.
const (
	dbFileName         = "supacove.db"
	dbWalFileName      = dbFileName + "-wal"
	dbShmFileName      = dbFileName + "-shm"
	lockFileName       = "supacove.lock"
	legacyDBFileName   = "supabackup.db"
	legacyWalFileName  = legacyDBFileName + "-wal"
	legacyShmFileName  = legacyDBFileName + "-shm"
	legacyLockFileName = "supabackup.lock"
)

// Store wraps the SQLite handle plus the advisory-lock file descriptors.
type Store struct {
	DB       *sql.DB
	lockFile *os.File
	// legacyLockFile holds the pre-rename lock (supabackup.lock) for the
	// entire process lifetime; nil on lockless (CLI) stores.
	legacyLockFile *os.File
	dataDir        string
	// migrations is the goose source; a field so tests can inject an overlay
	// FS with an extra pending migration to exercise the upgrade path.
	migrations fs.FS
}

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// Open acquires the exclusive advisory lock (server mode) and opens SQLite.
// The lock prevents two supacove servers from sharing a data directory; the
// legacy lock prevents a supabackup binary from running beside this one. A
// locked Open is also the only path that migrates the pre-rename file names.
func Open(dataDir string) (*Store, error) {
	return open(dataDir, true)
}

// OpenForCLI opens SQLite without the advisory lock. CLI utility commands
// (bootstrap, reset-password) must run while the server holds LOCK_EX —
// flock's shared mode cannot coexist with an exclusive holder, so utilities
// do not participate in the lock at all. Cross-process write safety comes
// from SQLite WAL mode plus busy_timeout; bootstrap/reset-password run in
// short transactions, so a race ends with exactly one winner. CLI stores are
// forbidden from running migrations (see Migrate) and from migrating the
// pre-rename file names: if those are still present the open is refused with
// ErrMigrationPending instead of risking a side-by-side empty database.
func OpenForCLI(dataDir string) (*Store, error) {
	return open(dataDir, false)
}

// dirState is the classification of a data directory's SQLite files.
type dirState int

const (
	dirFresh         dirState = iota // no database files at all
	dirLegacyPending                 // legacy main present, current main absent: migrate
	dirCurrent                       // current main present, no legacy files
)

// dataDirState is the existence snapshot of the current- and legacy-named
// SQLite files; classify is the single source of truth for which startup
// states are legal (rename plan §4.3.2).
type dataDirState struct {
	legacyMain, legacyWal, legacyShm bool
	main, wal, shm                   bool
}

// probeDataDir stats the six file names that may exist in a data directory.
func probeDataDir(dataDir string) (dataDirState, error) {
	var st dataDirState
	for _, e := range []struct {
		name string
		dst  *bool
	}{
		{legacyDBFileName, &st.legacyMain},
		{legacyWalFileName, &st.legacyWal},
		{legacyShmFileName, &st.legacyShm},
		{dbFileName, &st.main},
		{dbWalFileName, &st.wal},
		{dbShmFileName, &st.shm},
	} {
		_, err := os.Stat(filepath.Join(dataDir, e.name))
		switch {
		case err == nil:
			*e.dst = true
		case errors.Is(err, os.ErrNotExist):
			// absent
		default:
			return st, fmt.Errorf("stat %s: %w", e.name, err)
		}
	}
	return st, nil
}

// presentList names every current/legacy database file that exists, for
// error messages (lock files are irrelevant and never listed).
func (st dataDirState) presentList() string {
	var names []string
	for _, e := range []struct {
		on   bool
		name string
	}{
		{st.legacyMain, legacyDBFileName},
		{st.legacyWal, legacyWalFileName},
		{st.legacyShm, legacyShmFileName},
		{st.main, dbFileName},
		{st.wal, dbWalFileName},
		{st.shm, dbShmFileName},
	} {
		if e.on {
			names = append(names, e.name)
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// classify maps the snapshot to one of the three legal states. Everything
// else is an error naming the offending files, so the operator (not the
// binary) resolves it: dual mains, a current main beside legacy sidecars,
// any sidecars without a main, or same-kind duplicates can never be
// auto-picked.
func (st dataDirState) classify() (dirState, error) {
	sidecarList := func() string {
		var names []string
		for _, e := range []struct {
			on   bool
			name string
		}{{st.legacyWal, legacyWalFileName}, {st.legacyShm, legacyShmFileName}, {st.wal, dbWalFileName}, {st.shm, dbShmFileName}} {
			if e.on {
				names = append(names, e.name)
			}
		}
		return strings.Join(names, ", ")
	}
	switch {
	case !st.legacyMain && !st.main:
		if st.legacyWal || st.legacyShm || st.wal || st.shm {
			return dirFresh, fmt.Errorf("%w: sidecar files without a main database (%s)", ErrInconsistentState, sidecarList())
		}
		return dirFresh, nil
	case st.legacyMain && st.main:
		return dirLegacyPending, fmt.Errorf(
			"%w: both %s and %s exist — stop ALL supacove and legacy supabackup processes, then remove the wrong one (a 0-byte %s left behind by an aborted legacy start can simply be deleted; otherwise keep the complete file and restore from a backup if unsure)",
			ErrInconsistentState, legacyDBFileName, dbFileName, legacyDBFileName)
	case st.legacyMain:
		if st.legacyWal && st.wal {
			return dirLegacyPending, fmt.Errorf("%w: both %s and %s exist", ErrInconsistentState, legacyWalFileName, dbWalFileName)
		}
		if st.legacyShm && st.shm {
			return dirLegacyPending, fmt.Errorf("%w: both %s and %s exist", ErrInconsistentState, legacyShmFileName, dbShmFileName)
		}
		return dirLegacyPending, nil
	default: // current main only
		if st.legacyWal || st.legacyShm {
			return dirCurrent, fmt.Errorf(
				"%w: current %s beside legacy sidecars (%s) — a legacy WAL/SHM must never be paired onto the current database; stop all processes and resolve manually",
				ErrInconsistentState, dbFileName, sidecarList())
		}
		return dirCurrent, nil
	}
}

// migrateLegacyFiles renames the legacy-named SQLite files onto the current
// names. The caller must hold BOTH advisory locks. Sidecars move first and
// the main database LAST: an interruption at any point leaves a state the
// next run resumes (per-file "current absent && legacy present → rename"),
// whereas moving the main database first would let the next start skip
// migration and open a database missing its WAL. The directory is synced
// after every rename so a power loss cannot surface a half-applied sequence
// (rename plan §4.3.3).
func migrateLegacyFiles(dataDir string) error {
	for _, pair := range []struct{ legacy, current string }{
		{legacyWalFileName, dbWalFileName},
		{legacyShmFileName, dbShmFileName},
		{legacyDBFileName, dbFileName},
	} {
		legacyPath := filepath.Join(dataDir, pair.legacy)
		if _, err := os.Stat(legacyPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat %s: %w", pair.legacy, err)
		}
		if _, err := os.Stat(filepath.Join(dataDir, pair.current)); err == nil {
			return fmt.Errorf("%w: %s already exists while %s does too", ErrInconsistentState, pair.current, pair.legacy)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", pair.current, err)
		}
		if err := os.Rename(legacyPath, filepath.Join(dataDir, pair.current)); err != nil {
			return fmt.Errorf("migrate %s -> %s: %w", pair.legacy, pair.current, err)
		}
		if err := fsyncDir(dataDir); err != nil {
			return fmt.Errorf("sync data dir after renaming %s: %w", pair.legacy, err)
		}
	}
	// The end state must be exactly "current".
	after, err := probeDataDir(dataDir)
	if err != nil {
		return err
	}
	if s, cerr := after.classify(); cerr != nil || s != dirCurrent {
		return fmt.Errorf("%w: file-name migration finished in an unexpected state", ErrInconsistentState)
	}
	return nil
}

// fsyncDir persists directory entries so renames survive a power loss.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	serr := d.Sync()
	cerr := d.Close()
	if serr != nil {
		return serr
	}
	return cerr
}

// acquireFlock creates (owner-only) and exclusively locks an advisory lock
// file without blocking.
func acquireFlock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, ErrLocked
	}
	return file, nil
}

func open(dataDir string, takeLock bool) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	// Tighten an existing directory: MkdirAll does not chmod, and the driver
	// would otherwise create db/wal/shm under the process umask (review
	// P1-05).
	if err := os.Chmod(dataDir, 0o700); err != nil { //nolint:gosec // G302: 0700 on the data dir IS the required mode
		return nil, fmt.Errorf("tighten data dir permissions: %w", err)
	}

	var f *os.File
	var legacyF *os.File
	if takeLock {
		nf, err := acquireFlock(filepath.Join(dataDir, lockFileName))
		if err != nil {
			return nil, err // ErrLocked, or the lock file could not be opened
		}
		f = nf
		// Hold the legacy lock unconditionally for the whole process
		// lifetime — creating it when absent: a legacy (supabackup) binary
		// never takes supacove.lock, so this is the only thing that keeps
		// the two generations mutually exclusive during the compatibility
		// window, including on fresh or restored data directories.
		lf, err := acquireFlock(filepath.Join(dataDir, legacyLockFileName))
		if err != nil {
			f.Close()
			if errors.Is(err, ErrLocked) {
				return nil, ErrLegacyInstance
			}
			return nil, err
		}
		legacyF = lf
	}

	// Classify BEFORE touching anything: only the three legal states may
	// proceed, and only a locked open migrates. The current main database is
	// pre-created only after this point, so a lockless CLI can never spawn
	// an empty current database beside a legacy one.
	st, err := probeDataDir(dataDir)
	if err != nil {
		closeLock(legacyF)
		closeLock(f)
		return nil, err
	}
	state, cerr := st.classify()
	if cerr != nil {
		closeLock(legacyF)
		closeLock(f)
		return nil, fmt.Errorf(
			"%w; present files: %s; stop ALL supacove and legacy supabackup processes (server and CLI) before resolving",
			cerr, st.presentList())
	}
	switch state {
	case dirLegacyPending:
		if !takeLock {
			return nil, ErrMigrationPending
		}
		if err := migrateLegacyFiles(dataDir); err != nil {
			closeLock(legacyF)
			closeLock(f)
			return nil, err
		}
	case dirFresh, dirCurrent:
		// Nothing to move.
	}

	// Tighten the current-named files owner-only. This runs after the
	// migration so renamed files are covered as well.
	for _, name := range []string{dbFileName, dbWalFileName, dbShmFileName} {
		p := filepath.Join(dataDir, name)
		if err := os.Chmod(p, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			closeLock(legacyF)
			closeLock(f)
			return nil, fmt.Errorf("tighten %s permissions: %w", name, err)
		}
	}
	// Pre-create the DB file owner-only, AFTER the locks and any migration:
	// a legacy supabackup binary pre-creates its file before taking the
	// lock, and that ordering is exactly how an aborted legacy start litters
	// a migrated directory; supacove must not repeat the mistake.
	dbPath := filepath.Join(dataDir, dbFileName)
	if pf, err := os.OpenFile(dbPath, os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_ = pf.Close()
		_ = os.Chmod(dbPath, 0o600)
	}

	abs, err := filepath.Abs(dbPath)
	if err != nil {
		closeLock(legacyF)
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
		closeLock(legacyF)
		closeLock(f)
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single writer process; a small pool keeps write transactions short.
	database.SetMaxOpenConns(4)
	database.SetMaxIdleConns(4)
	if err := database.Ping(); err != nil {
		database.Close()
		closeLock(legacyF)
		closeLock(f)
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return &Store{DB: database, lockFile: f, legacyLockFile: legacyF, dataDir: dataDir, migrations: embeddedMigrations}, nil
}

// Close releases the locks and closes the database. Locks are released in
// reverse acquisition order (legacy first, current last): closing the
// current lock first would let another process acquire it and then mis-read
// our still-held legacy lock as a running legacy instance.
func (s *Store) Close() error {
	err := s.DB.Close()
	closeLock(s.legacyLockFile)
	closeLock(s.lockFile)
	return err
}

// closeLock releases an advisory lock if this store holds one. The OS also
// releases it automatically when the process dies, so a stale lock file is
// never a false positive (dev-plan P1: OS advisory lock, not lock-file
// existence checks).
func closeLock(f *os.File) {
	if f != nil {
		f.Close()
	}
}
