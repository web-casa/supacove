package server

import (
	"net/http"
	"testing"
)

// Regression: /metrics carries database names, staging sizes and job
// outcomes, so it must not be reachable without a session — it sits outside
// the /api prefix that the guard's default-deny covers (overall review,
// security domain P1).
func TestMetricsRequiresSession(t *testing.T) {
	env := newTestEnv(t)
	env.bootstrapAdmin(t)

	anon := &http.Client{} // no cookie jar: unauthenticated
	resp, err := anon.Get(env.base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous /metrics = %d, want 401", resp.StatusCode)
	}

	// The authenticated path is unchanged (server_phase7_test covers content).
	resp2, err := env.client.Get(env.base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("authenticated /metrics = %d, want 200", resp2.StatusCode)
	}
	if h := resp2.Header.Get("Cache-Control"); h != "no-store" {
		t.Errorf("metrics Cache-Control = %q, want no-store", h)
	}
}
