// Package staging manages the local ciphertext staging area: directory
// layout, space pre-checks, and orphan recovery at startup (dev-plan P2
// task 7). Only this package decides which file names are "live".
package staging

import (
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
	return st.Bavail * uint64(st.Bsize), nil
}

// OrphanCleanup removes staging leftovers that belong to no live job:
// .inprogress files (a crash mid-dump) and job-creds-* directories. Called
// at startup while no worker is running. Committed artifacts are NEVER
// removed here — they are the product; their lifecycle is retention's
// business (Phase 3), not orphan cleanup's.
func (s *Staging) OrphanCleanup(liveJobIDs map[int64]bool) (removed []string, err error) {
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		full := filepath.Join(s.Dir, name)
		switch {
		case strings.HasSuffix(name, ".inprogress"):
			_ = os.Remove(full)
			removed = append(removed, name)
		case strings.HasPrefix(name, "job-creds-"):
			_ = os.RemoveAll(full)
			removed = append(removed, name)
		case artifactRe.MatchString(name):
			// Committed artifact of some job — live ones keep it.
			// (Retention in Phase 3; orphan logic never touches these.)
		}
	}
	_ = liveJobIDs
	return removed, nil
}
