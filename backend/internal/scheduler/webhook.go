package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// postWebhook sends a JSON POST to a webhook URL with a 10s timeout.
// Failures are logged WITHOUT the URL (which carries bearer credentials in
// its path/query for most webhook providers); only the webhook name and
// status/error category are recorded (round-4 review P1-06).
func (s *Scheduler) postWebhook(name, url string, payload map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	body, err := json.Marshal(payload)
	if err != nil {
		s.log.Error("webhook marshal", "err", err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		// Do not log the URL (may contain bearer tokens).
		s.log.Error("webhook request build failed", "name", name)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Supabackup-Event", fmt.Sprintf("%v", payload["event"]))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Network error may contain the URL — log only the error category.
		s.log.Error("webhook delivery failed", "name", name, "err_type", fmt.Sprintf("%T", err))
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		s.log.Error("webhook delivery rejected", "name", name, "status", resp.StatusCode)
	}
}
