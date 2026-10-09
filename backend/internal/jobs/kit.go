// Package jobs — recovery kit backfill and lifecycle (phase-5 review
// P1-09): every succeeded backup with a committed manifest must have a
// persisted, downloadable recovery kit; startup regenerates any that a
// historical write failure left behind.
package jobs

import (
	"context"
	"os"
	"path/filepath"

	"github.com/web-casa/supacove/backend/internal/manifest"
	platformpkg "github.com/web-casa/supacove/backend/internal/platform"
	"github.com/web-casa/supacove/backend/internal/recovery"
)

// BackfillRecoveryKits regenerates missing recovery kits for succeeded jobs
// whose kit reference is empty or whose kit file vanished. It runs at
// startup, before the worker claims new jobs, and is idempotent. Failures
// are logged per job and never fatal — the kit is regenerable from the
// manifest, which remains the authoritative restore description.
func (r *Runner) BackfillRecoveryKits(ctx context.Context) {
	rows, err := r.authDB.QueryContext(ctx, `
		SELECT id, COALESCE(manifest_path,''), COALESCE(platform,''),
		       COALESCE(recovery_kit_path,'')
		FROM jobs
		WHERE status = 'succeeded' AND manifest_path != ''`)
	if err != nil {
		r.log.Error("kit backfill query failed", "err", err)
		return
	}
	type job struct {
		id       int64
		manifest string
		platform string
		kitPath  string
	}
	var jobsList []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.manifest, &j.platform, &j.kitPath); err != nil {
			rows.Close() //nolint:sqlclosecheck // rows are fully consumed and closed BEFORE the write loop (round-2 P2-02): holding a read cursor across writes is the hazard this rule misses
			r.log.Error("kit backfill scan failed", "err", err)
			return
		}
		jobsList = append(jobsList, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		r.log.Error("kit backfill iteration", "err", err)
		return
	}

	for _, j := range jobsList {
		if j.kitPath != "" {
			if _, serr := os.Stat(j.kitPath); serr == nil {
				continue // kit exists and is referenced: nothing to do
			}
		}
		mb, merr := os.ReadFile(j.manifest)
		if merr != nil {
			r.log.Warn("kit backfill: manifest unreadable", "job", j.id, "err", merr)
			continue
		}
		m, perr := manifest.Unmarshal(mb)
		if perr != nil {
			r.log.Warn("kit backfill: manifest unparseable", "job", j.id, "err", perr)
			continue
		}
		expectedTables := int64(-1)
		if manifest.HasTableCount(mb) {
			expectedTables = m.Dependencies.TableCount
		}
		platform := platformpkg.Platform(j.platform)
		if platform == "" {
			platform = platformpkg.Generic
		}
		kitPath := j.manifest + ".restore.sh"
		script := recovery.GenerateRestoreScriptWithTables(recovery.KitInput{
			BackupUUID:       m.BackupID,
			Platform:         platform,
			ArtifactFileName: m.Archive.FileName,
			SHA256:           m.Archive.SHA256,
			KeyID:            m.Archive.Encryption.KeyID,
		}, expectedTables)
		if werr := durableWriteFile(kitPath, []byte(script)); werr != nil {
			r.log.Error("kit backfill write failed", "job", j.id, "err", werr)
			continue
		}
		if _, uerr := r.authDB.ExecContext(ctx,
			`UPDATE jobs SET recovery_kit_path = ? WHERE id = ?`, kitPath, j.id); uerr != nil {
			r.log.Error("kit backfill reference write failed", "job", j.id, "err", uerr)
			continue
		}
		r.log.Info("recovery kit backfilled", "job", j.id, "path", filepath.Base(kitPath))
	}
}
