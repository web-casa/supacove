package i18n

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFromRequest(t *testing.T) {
	cases := []struct {
		header string
		want   Lang
	}{
		{"", En},
		{"en", En},
		{"en-US,en;q=0.9", En},
		{"zh", ZhCN},
		{"zh-CN", ZhCN},
		{"zh-TW", ZhCN}, // any zh* maps to the one Chinese catalog
		{"zh-CN,zh;q=0.9,en;q=0.8", ZhCN},
		{"en;q=0.5,zh;q=0.9", ZhCN},
		{"fr,de;q=0.7", En},  // unsupported languages fall back to English
		{"zh;q=0", En},       // explicitly excluded
		{"en;q=0.5,zh;q=0", En},
		{"zh;q=abc", En}, // malformed q skipped, no tags left
		{" , ;;", En},
	}
	for _, tc := range cases {
		r, _ := http.NewRequest("GET", "/", nil)
		if tc.header != "" {
			r.Header.Set("Accept-Language", tc.header)
		}
		if got := FromRequest(r); got != tc.want {
			t.Errorf("FromRequest(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
	if FromRequest(nil) != En {
		t.Error("nil request must yield English")
	}
}

func TestMiddlewareAndContext(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "zh-CN;q=0.8,en;q=0.9")
	// q=0.9 en wins over zh-CN;q=0.8 — order does not beat q-values.
	var inner Lang
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		inner = FromContext(req.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), r)
	if inner != En {
		t.Errorf("context lang = %q, want en (highest q wins)", inner)
	}

	r.Header.Set("Accept-Language", "zh-CN;q=0.9,en;q=0.5")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if inner != ZhCN {
		t.Errorf("context lang = %q, want zh-CN", inner)
	}
	// No middleware: default English, never a panic.
	if FromContext(context.Background()) != En {
		t.Error("bare context must default to English")
	}
	if T(context.Background(), "en", "zh") != "en" {
		t.Error("T on a bare context must return English")
	}
}

func TestMsg(t *testing.T) {
	m := Msg{En: "hello", Zh: "你好"}
	if m.T(En) != "hello" || m.T(ZhCN) != "你好" {
		t.Error("T must select the requested language")
	}
	partial := Msg{En: "only"}
	if partial.T(ZhCN) != "only" {
		t.Error("missing Zh must fall back to En")
	}
	if !(Msg{}.Empty()) || (Msg{En: "x"}).Empty() {
		t.Error("Empty must reflect both fields")
	}
}
