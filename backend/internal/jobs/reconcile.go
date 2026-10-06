// Package jobs — bucket reconciliation (dev-plan Phase 3 task 5): a
// read-only comparison of remote objects against job references. Nothing is
// deleted: "确认归属前禁用清理".
package jobs

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ReconcileReport is the read-only reconciliation outcome for one
// destination.
type ReconcileReport struct {
	DestinationID int64     `json:"destinationId"`
	GeneratedAt   time.Time `json:"generatedAt"`
	RemoteObjects int       `json:"remoteObjects"`
	Matched       int       `json:"matched"`
	// Orphaned: remote objects that no committed job references. Could be
	// leftovers from failed uploads, deleted jobs, or foreign objects under
	// our prefix.
	Orphaned []string `json:"orphaned"`
	// Missing: committed jobs whose ciphertext or manifest object is absent
	// remotely. These backups are NOT fully remote-committed despite their
	// local state.
	Missing []string `json:"missing"`
	// Uncommitted: jobs with remote_state='uploading' whose objects exist
	// remotely (a crash mid-upload). Not remotely committed, not deletable
	// until归属 is confirmed.
	Uncommitted []string `json:"uncommitted"`
}

// Reconcile runs the read-only comparison for one destination.
func (r *Runner) Reconcile(ctx context.Context, destID int64) (*ReconcileReport, error) {
	dest, err := r.GetDestination(ctx, destID)
	if err != nil {
		return nil, err
	}
	backend, err := r.BuildBackend(ctx, dest)
	if err != nil {
		return nil, err
	}

	report := &ReconcileReport{
		DestinationID: destID,
		GeneratedAt:   time.Now().UTC(),
	}

	objects, err := backend.List(ctx, dest.Prefix)
	if err != nil {
		return nil, err
	}
	report.RemoteObjects = len(objects)
	remote := make(map[string]bool, len(objects))
	for _, o := range objects {
		// Diagnostic canaries are ours but never backup references.
		if strings.Contains(o.Key, "/diagnostic/") {
			continue
		}
		remote[o.Key] = true
	}

	rows, err := r.authDB.QueryContext(ctx, `
		SELECT id, remote_state, remote_object_key, remote_manifest_key
		FROM jobs
		WHERE destination_id = ? AND remote_state != ''`, destID)
	if err != nil {
		return nil, err
	}
	type ref struct{ state, objKey, manKey string }
	byJob := make(map[int64]ref)
	for rows.Next() {
		var id int64
		var rf ref
		if err := rows.Scan(&id, &rf.state, &rf.objKey, &rf.manKey); err != nil {
			rows.Close() //nolint:sqlclosecheck // rows are fully consumed and closed BEFORE the write loop (round-2 P2-02): holding a read cursor across writes is the hazard this rule misses
			return nil, err
		}
		byJob[id] = rf
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	expected := map[string]bool{}
	for id, rf := range byJob {
		switch rf.state {
		case "committed":
			for _, k := range []string{rf.objKey, rf.manKey} {
				if k == "" {
					continue
				}
				expected[k] = true
				if remote[k] {
					report.Matched++
				} else {
					report.Missing = append(report.Missing,
						fmt.Sprintf("job %d: %s", id, k))
				}
			}
		case "uploading":
			for _, k := range []string{rf.objKey, rf.manKey} {
				if k != "" && remote[k] {
					report.Uncommitted = append(report.Uncommitted,
						fmt.Sprintf("job %d: %s", id, k))
				}
			}
		}
	}

	for _, o := range objects {
		if strings.Contains(o.Key, "/diagnostic/") {
			continue
		}
		if !expected[o.Key] {
			report.Orphaned = append(report.Orphaned, o.Key)
		}
	}

	r.log.Info("reconciliation finished", "destination", destID,
		"remote", report.RemoteObjects, "matched", report.Matched,
		"orphaned", len(report.Orphaned), "missing", len(report.Missing),
		"uncommitted", len(report.Uncommitted))
	return report, nil
}
