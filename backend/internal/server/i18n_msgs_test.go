package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/web-casa/supacove/backend/internal/i18n"
)

// Review R2-P2-01: the unreachable-fire-time message embeds a quoted
// expression that contains spaces; the localization must keep it intact and
// leave the English output byte-identical to the original error.
func TestCronMsgKeepsQuotedExpression(t *testing.T) {
	ctx := i18n.WithLang(context.Background(), i18n.ZhCN)
	orig := errors.New(`cron expression "0 0 31 2 *" has no reachable fire time`)
	if got := cronMsg(context.Background(), orig); got != orig.Error() {
		t.Errorf("English output drifted:\n got  %q\n want %q", got, orig.Error())
	}
	if got := cronMsg(ctx, orig); got != `cron 表达式 "0 0 31 2 *" 没有可达的触发时间` {
		t.Errorf("Chinese output wrong: %q", got)
	}
	// Parser-failure branch keeps the third-party detail.
	orig2 := errors.New(`invalid cron expression "* * *": expected exactly 5 fields, found 3`)
	if got := cronMsg(ctx, orig2); got != `无效的 cron 表达式 "* * *": expected exactly 5 fields, found 3` {
		t.Errorf("Chinese parse-failure wrong: %q", got)
	}
	// Unknown messages pass through untouched.
	other := errors.New("something else entirely")
	if cronMsg(ctx, other) != other.Error() {
		t.Error("unknown message must pass through")
	}
}

func TestPgURIMsg(t *testing.T) {
	ctx := i18n.WithLang(context.Background(), i18n.ZhCN)
	if got := pgURIMsg(ctx, errors.New("URI scheme must be postgres:// or postgresql://")); got != "URI 协议必须是 postgres:// 或 postgresql://" {
		t.Errorf("fixed reason: %q", got)
	}
	if got := pgURIMsg(ctx, errors.New(`invalid port "abc"`)); got != `端口无效："abc"` {
		t.Errorf("port reason: %q", got)
	}
	if got := pgURIMsg(ctx, errors.New("weird unknown")); got != "weird unknown" {
		t.Errorf("unknown must pass through: %q", got)
	}
}

func TestDestinationMsg(t *testing.T) {
	ctx := i18n.WithLang(context.Background(), i18n.ZhCN)
	secrets := []string{"real-access-key", "real-secret-key"}
	if got := destinationMsg(ctx, errors.New("access key and secret key are required"), secrets); got != "access key 和 secret key 为必填项" {
		t.Errorf("fixed reason: %q", got)
	}
	// Provider diagnostics keep their redaction.
	if got := destinationMsg(ctx, errors.New("diagnostic test failed for real-secret-key"), secrets); got != "diagnostic test failed for [REDACTED]" {
		t.Errorf("provider diagnostics: %q", got)
	}
	// Fixed texts survive even when the keys are tiny (would mangle text).
	tiny := destinationMsg(ctx, errors.New("name must be 1-100 characters"), []string{"a", "b"})
	if tiny != "name 长度需为 1–100 个字符" {
		t.Errorf("fixed text after tiny keys: %q", tiny)
	}
}

// Review R2 round 2: guard the production Router wiring itself — an /api
// panic must answer in the request language through the real middleware
// chain, not just a hand-assembled one. The login handler dereferences the
// auth store, so a nil store makes it panic on any login attempt.
func TestRouterPanicAnswersInRequestLanguage(t *testing.T) {
	srv := newI18nProbeServer(t)
	handler := srv.Router()

	req := httptestRequest("POST", "/api/auth/login", `{"username":"panic-probe","password":"panic-probe-password"}`, "zh-CN")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "意外的内部错误") {
		t.Errorf("zh panic body: %s", rec.Body.String())
	}

	req = httptestRequest("POST", "/api/auth/login", `{"username":"panic-probe","password":"panic-probe-password"}`, "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unexpected internal error") {
		t.Errorf("en panic body: %s", rec.Body.String())
	}
	if i18n.FromRequest(nil) != i18n.En {
		t.Error("sanity: nil request default")
	}
}
