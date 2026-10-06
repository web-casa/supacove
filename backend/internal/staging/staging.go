// Package staging manages the local ciphertext staging area: directory
// layout, space pre-checks, and orphan recovery at startup (dev-plan P2
// task 7). Only this package decides which file names are "live".
package staging

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// Layout (under <DataDir>/staging):
//
//	backup-job<id>.dump.age(.inprogress)   committed / in-flight artifacts
//	job-creds-*/                            per-job PGPASSFILE dirs (removed on exit)
type Staging struct {
	Dir string
}

func New(dataDir string) *Staging {
	return &Staging{Dir: filepath.Join(dataDir, "staging")}
}

func (s *Staging) Ensure() error {
	return os.MkdirAll(s.Dir, 0o700)
}

var artifactRe = regexp.MustCompile(`^backup-job(\d+)\.dump\.age$`)

// ArtifactPath is the committed artifact path for a job.
func (s *Staging) ArtifactPath(jobID int64) string {
	return filepath.Join(s.Dir, fmt.Sprintf("backup-job%d.dump.age", jobID))
}

// InProgressPath is the temp path used while a dump runs.
func (s *Staging) InProgressPath(jobID int64) string {
	return s.ArtifactPath(jobID) + ".inprogress"
}

// FreeBytes reports available bytes on the staging filesystem.
func (s *Staging) FreeBytes() (uint64, error) {
	if err := s.Ensure(); err != nil {
		return 0, err
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(s.Dir, &st); err != nil {
		return 0, err
	}
	if st.Bsize <= 0 {
		return 0, fmt.Errorf("statfs reported non-positive block size %d", st.Bsize)
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// OrphanCleanupStartup removes staging leftovers at startup — BEFORE the
// worker exists, so there are no live references to protect; the signature
// is intentionally exclusive about that (round-2 review P2-03). Committed
// artifacts are NEVER removed here. Per-item removal errors propagate.
func (s *Staging) OrphanCleanupStartup() (removed []string, err error) {
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		full := filepath.Join(s.Dir, name)
		switch {
		case strings.HasSuffix(name, ".inprogress"):
			if rerr := os.Remove(full); rerr != nil {
				errs = append(errs, fmt.Errorf("remove %s: %w", name, rerr))
				continue
			}
			removed = append(removed, name)
		case strings.HasPrefix(name, "job-creds-"), strings.HasPrefix(name, "verify-fetch-"),
			strings.HasPrefix(name, ".durable-"), strings.HasSuffix(name, ".manifest.json.tmp"):
			if rerr := os.RemoveAll(full); rerr != nil {
				errs = append(errs, fmt.Errorf("remove credential dir %s: %w", name, rerr))
				continue
			}
			removed = append(removed, name)
		case artifactRe.MatchString(name):
			// Committed artifact of some job — retention's business, not
			// orphan cleanup's (Phase 3).
		}
	}
	return removed, errors.Join(errs...)
}
