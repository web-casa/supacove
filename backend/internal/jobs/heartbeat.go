// Package jobs — dead-man's switch (Phase 7): after each successful backup
// the system pings a configured external heartbeat URL. An external service
// (e.g. healthchecks.io) monitors for silence and alerts when the expected
// ping does not arrive. This complements (not replaces) webhook
// notifications: the dead-man's switch catches "backups stopped happening"
// while webhooks catch "this specific backup failed".
package jobs

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// SetHeartbeatURL configures the external dead-man's switch ping URL.
// Must be called before Start. Empty string disables the heartbeat.
func (r *Runner) SetHeartbeatURL(url string) { r.heartbeatURL = url }

// pingHeartbeat sends a GET to the configured heartbeat URL. This is
// fire-and-forget: a failure is logged but never blocks the backup pipeline.
// The external service alerts on silence — we do NOT need to handle the
// response body.
func (r *Runner) pingHeartbeat() {
	if r.heartbeatURL == "" {
		return
	}
	// Async: never block the worker. The external service alerts on silence,
	// so a failed ping is recoverable on the next successful backup.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.heartbeatURL, nil)
		if err != nil {
			r.log.Error("heartbeat request build failed", "err", err)
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			// Do NOT log the URL: it may contain bearer tokens for the
			// external healthcheck service.
			r.log.Error("heartbeat ping failed", "err_type", fmt.Sprintf("%T", err))
			return
		}
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			r.log.Error("heartbeat rejected", "status", resp.StatusCode)
		}
	}()
}
