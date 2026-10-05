package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudfan/supabackup/backend/internal/config"
	"github.com/cloudfan/supabackup/backend/internal/db"
	"github.com/cloudfan/supabackup/backend/internal/i18n"
	"github.com/cloudfan/supabackup/backend/internal/limiter"
)

// Regression for review P2-02: the language middleware must wrap the
// recoverer (outer), or a panicked /api request answers in English no matter
// what Accept-Language said — the recoverer writes its 500 from the outer
// request, whose context never receives inner WithContext values.
func TestRecovererRespondsInRequestLanguage(t *testing.T) {
	store, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	defer store.DB.Close()
	srv := New(store, nil, &config.Config{}, make([]byte, 32), limiter.New(),
		slog.New(slog.NewTextHandler(io.Discard, nil)), BuildInfo{Version: "test"})

	boom := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("boom") })
	// Same relative order as Router(): i18n outside the recoverer.
	h := i18n.Middleware(srv.recoverer(boom))

	req := httptest.NewRequest("GET", "/api/anything", nil)
	req.Header.Set("Accept-Language", "zh-CN")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "意外的内部错误") {
		t.Errorf("panic body not localized: %s", rec.Body.String())
	}

	// And English stays the default when no language is requested.
	req = httptest.NewRequest("GET", "/api/anything", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "unexpected internal error") {
		t.Errorf("default body not English: %s", rec.Body.String())
	}
}
