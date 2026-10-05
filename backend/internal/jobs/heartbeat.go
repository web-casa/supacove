// Package jobs — dead-man's switch (Phase 7): after each successful backup
// the system pings the database's configured external heartbeat URL. An
// external service (e.g. healthchecks.io) monitors for silence and alerts
// when the expected ping does not arrive.
//
// Pinging is bound to REAL freshness (dev-plan Phase 7 task 2):
//   - the success ping only fires when the backup was REMOTELY COMMITTED
//     (or local-only by design — the documented v1 local-only exception)
//     AND the SNAPSHOT age is gated: a backup whose export snapshot is
//     older than period+grace does NOT ping — the stale ciphertext must not
//     silence the monitor (round-1 review P1-05: the age gate applies to
//     the inherited fallback URL too; without a period there is no silence
//     semantics, so the backup must not be vouched for at all);
//   - failures ping url+"/fail" (healthchecks.io convention; the suffix is
//     inserted BEFORE any query string), so a failing backup is visible
//     immediately, not only via silence. There is no /start signal in v1:
//     a start ping would require queue-aware grace math the per-database
//     period model does not carry yet (v1.0 item);
//   - per-database configuration wins over the process-wide fallback URL;
//     the reserved value "-" explicitly DISABLES the heartbeat for one
//     database even when a fallback is configured.
package jobs

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// heartbeatDecision captures why a success ping was (not) sent — logged so
// silence is always explainable from the server logs. disabled is explicit
// (the "-" marker) and also silences fail pings; a plain skip never does.
type heartbeatDecision struct {
	url      string
	skip     string // non-empty = skipped with this reason
	disabled bool   // explicit "-" marker: no success AND no fail signal
	lateBy   time.Duration
}

// heartbeatFor loads the per-database heartbeat config and applies the
// freshness gate. Empty db URL inherits the process fallback UNLESS it is
// the explicit disable marker "-".
func (r *Runner) heartbeatFor(dbID int64, dumpStart time.Time, remoteCommitted, hasDestination bool) heartbeatDecision {
	dec := heartbeatDecision{}
	var dbURL string
	var period, grace int
	err := r.authDB.QueryRow(`
		SELECT heartbeat_url, heartbeat_period_hours, heartbeat_grace_hours
		FROM databases WHERE id = ? AND deleted_at IS NULL`, dbID).
		Scan(&dbURL, &period, &grace)
	if err != nil {
		return dec // row vanished: silent no-op
	}
	url := strings.TrimSpace(dbURL)
	if url == "-" {
		dec.disabled = true
		dec.skip = "heartbeat explicitly disabled for this database"
		return dec
	}
	if url == "" {
		url = strings.TrimSpace(r.heartbeatURL) // process fallback
	}
	if url == "" {
		return dec // no heartbeat configured
	}
	dec.url = url

	// A backup that is not remotely committed did not reach the durability
	// level a dead-man switch should vouch for — UNLESS the database is
	// local-only by design (no destination assigned).
	if hasDestination && !remoteCommitted {
		dec.skip = "backup is not remotely committed; the heartbeat must not vouch for it"
		return dec
	}

	// The age gate is UNCONDITIONAL (round-1 review P1-05): without a
	// period there are no silence semantics, so a success ping would vouch
	// for backups of unknown freshness. period must be configured.
	if period <= 0 {
		dec.skip = "no heartbeat period configured; the backup cannot be vouched for (set heartbeatPeriodHours)"
		return dec
	}
	maxAge := time.Duration(period+grace) * time.Hour
	age := time.Since(dumpStart)
	if age > maxAge {
		dec.skip = fmt.Sprintf("snapshot is stale (age %s > period+grace %s); the monitor must alert", age.Round(time.Second), maxAge)
		dec.lateBy = age - maxAge
		return dec
	}
	return dec
}

// pingSuccess sends the success ping for a backup that passed the freshness
// gate. Fire-and-forget with a bounded window; never blocks the backup
// pipeline. last_heartbeat_at records SUCCESSFUL pings only (round-1
// review P2-03: a failed attempt must not display as a heartbeat).
func (r *Runner) pingSuccess(dbID int64, dec heartbeatDecision) {
	if dec.url == "" || dec.skip != "" {
		r.log.Warn("heartbeat success ping skipped", "database_id", dbID, "reason", dec.skip)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, dec.url, nil)
		if err != nil {
			r.log.Error("heartbeat request build failed", "err_type", fmt.Sprintf("%T", err))
			return
		}
		resp, err := heartbeatClient.Do(req)
		if err != nil {
			// Do NOT log the URL: it may contain bearer tokens.
			r.log.Error("heartbeat ping failed", "err_type", fmt.Sprintf("%T", err))
			return
		}
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			r.log.Error("heartbeat rejected", "status", resp.StatusCode)
			return
		}
		if _, err := r.authDB.Exec(`
			UPDATE databases SET last_heartbeat_at = strftime('%s','now') WHERE id = ?`, dbID); err != nil {
			r.log.Error("heartbeat timestamp write failed", "database_id", dbID, "err", err)
		}
	}()
}

// pingFail signals a backup failure to the dead-man switch (url+"/fail").
func (r *Runner) pingFail(dbID int64) {
	dec := r.heartbeatFor(dbID, time.Now(), true, false)
	if dec.url == "" || dec.disabled {
		return
	}
	// The fail signal ignores the freshness gate (a failing backup is
	// exactly what the monitor wants to know about), but keeps the
	// disable marker honored.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, withURLPathSuffix(dec.url, "/fail"), nil)
		if err != nil {
			r.log.Error("heartbeat fail request build failed", "err_type", fmt.Sprintf("%T", err))
			return
		}
		resp, err := heartbeatClient.Do(req)
		if err != nil {
			r.log.Error("heartbeat fail ping failed", "err_type", fmt.Sprintf("%T", err))
			return
		}
		resp.Body.Close()
	}()
}

// withURLPathSuffix inserts suffix into the URL PATH (before any query or
// fragment), so healthchecks-style "?token=…" URLs keep the token in the
// query instead of swallowing "/fail" into a parameter value (P1-04).
func withURLPathSuffix(rawURL, suffix string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		// Unparseable: the request would fail anyway; fall back to plain
		// concatenation before the query marker.
		if i := strings.IndexByte(rawURL, '?'); i >= 0 {
			return rawURL[:i] + suffix + rawURL[i:]
		}
		return rawURL + suffix
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + suffix
	return u.String()
}
