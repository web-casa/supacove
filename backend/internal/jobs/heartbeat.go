// Package jobs — dead-man's switch (Phase 7): after each successful backup
// the system pings the database's configured external heartbeat URL. An
// external service (e.g. healthchecks.io) monitors for silence and alerts
// when the expected ping does not arrive.
//
// Pinging is bound to REAL freshness (dev-plan Phase 7 task 2):
//   - the success ping only fires when the backup was REMOTELY COMMITTED
//     (or local-only by design) AND the snapshot is still fresh: a backup
//     that sat in the queue so long that its EXPORT SNAPSHOT is older than
//     period+grace does NOT ping — the stale ciphertext must not silence
//     the monitor;
//   - failures ping url+"/fail" (healthchecks.io convention) so a failing
//     backup is visible immediately, not only via silence;
//   - per-database configuration (url/period/grace) with the process-wide
//     SB_HEARTBEAT_URL as the fallback URL.
package jobs

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// heartbeatDecision captures why a success ping was (not) sent — logged so
// silence is always explainable from the server logs.
type heartbeatDecision struct {
	url    string
	skip   string // non-empty = skipped with this reason
	kind   string // "" | "/fail"
	lateBy time.Duration
}

// heartbeatFor loads the per-database heartbeat config (period/grace) and
// applies the freshness gate.
func (r *Runner) heartbeatFor(dbID int64, dumpStart time.Time, remoteCommitted, hasDestination bool) heartbeatDecision {
	dec := heartbeatDecision{}
	var url string
	var period, grace int
	err := r.authDB.QueryRow(`
		SELECT COALESCE(NULLIF(heartbeat_url,''), ?), heartbeat_period_hours, heartbeat_grace_hours
		FROM databases WHERE id = ? AND deleted_at IS NULL`,
		r.heartbeatURL, dbID).Scan(&url, &period, &grace)
	if err != nil || url == "" {
		return dec // no heartbeat configured (or row vanished): silent no-op
	}
	dec.url = url

	// A backup that is not remotely committed did not reach the durability
	// level a dead-man switch should vouch for — UNLESS the database is
	// local-only by design (no destination assigned).
	if hasDestination && !remoteCommitted {
		dec.skip = "backup is not remotely committed; the heartbeat must not vouch for it"
		return dec
	}

	// Snapshot age gate: started_at ≈ the export snapshot instant. If the
	// data is already older than period+grace, the external monitor SHOULD
	// alert — a late upload must not mask the gap (dev-plan: 旧密文晚上传
	// 成功不得消除超期).
	if period > 0 {
		maxAge := time.Duration(period+grace) * time.Hour
		age := time.Since(dumpStart)
		if age > maxAge {
			dec.skip = fmt.Sprintf("snapshot is stale (age %s > period+grace %s); the monitor must alert", age.Round(time.Second), maxAge)
			dec.lateBy = age - maxAge
			return dec
		}
	}
	return dec
}

// pingSuccess sends the success ping for a backup that passed the freshness
// gate, and records last_heartbeat_at. Fire-and-forget with a bounded
// window; never blocks the backup pipeline.
func (r *Runner) pingSuccess(dbID int64, dec heartbeatDecision) {
	if dec.url == "" || dec.skip != "" {
		r.log.Warn("heartbeat success ping skipped", "database_id", dbID, "reason", dec.skip)
		return
	}
	r.ping(dbID, dec.url, "")
}

// pingFail signals a backup failure to the dead-man switch (url+"/fail").
func (r *Runner) pingFail(dbID int64) {
	var url string
	if err := r.authDB.QueryRow(`
		SELECT COALESCE(NULLIF(heartbeat_url,''), ?)
		FROM databases WHERE id = ? AND deleted_at IS NULL`,
		r.heartbeatURL, dbID).Scan(&url); err != nil || url == "" {
		return
	}
	r.ping(dbID, url, "/fail")
}

// ping performs the GET (with the healthchecks.io convention suffix).
func (r *Runner) ping(dbID int64, url, suffix string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+suffix, nil)
		if err != nil {
			r.log.Error("heartbeat request build failed", "err_type", fmt.Sprintf("%T", err))
			return
		}
		resp, err := heartbeatClient.Do(req)
		if err != nil {
			// Do NOT log the URL: it may contain bearer tokens for the
			// external healthcheck service.
			r.log.Error("heartbeat ping failed", "suffix", suffix, "err_type", fmt.Sprintf("%T", err))
			return
		}
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			r.log.Error("heartbeat rejected", "status", resp.StatusCode)
		}
	}()
	if _, err := r.authDB.Exec(`
		UPDATE databases SET last_heartbeat_at = strftime('%s','now') WHERE id = ?`, dbID); err != nil {
		r.log.Error("heartbeat timestamp write failed", "database_id", dbID, "err", err)
	}
}
