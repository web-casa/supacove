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
// Failures are logged, never propagated (notification is best-effort).
func (s *Scheduler) postWebhook(url string, payload map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	body, err := json.Marshal(payload)
	if err != nil {
		s.log.Error("webhook marshal", "err", err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		s.log.Error("webhook request", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Supabackup-Event", fmt.Sprintf("%v", payload["event"]))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.log.Error("webhook delivery failed", "url", url, "err", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		s.log.Error("webhook delivery rejected", "url", url, "status", resp.StatusCode)
	}
}
