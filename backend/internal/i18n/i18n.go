// Package i18n — minimal server-side message localization for API responses.
// Messages are defined inline as {En, Zh} pairs at their call sites (no keyed
// catalog, so the two translations cannot drift apart unseen); the response
// language comes from the request's Accept-Language header, parsed once by
// Middleware and carried in the request context. English is the fallback for
// everything unrecognized, and also the language of persisted records: job
// error messages are historical facts, not per-request responses.
package i18n

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Lang is a BCP-47-ish response language.
type Lang string

const (
	En   Lang = "en"
	ZhCN Lang = "zh-CN"
)

type ctxKey struct{}

// Middleware parses Accept-Language once and stores the chosen language in
// the request context for the handlers downstream.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, FromRequest(r))))
	})
}

// FromContext returns the request language chosen by Middleware (English
// outside an i18n-aware chain, e.g. in background jobs or tests).
func FromContext(ctx context.Context) Lang {
	if lang, ok := ctx.Value(ctxKey{}).(Lang); ok {
		return lang
	}
	return En
}

// Msg carries the same text in the supported languages. En doubles as the
// identifier/contract text, so it must always be non-empty for a real message.
type Msg struct {
	En string
	Zh string
}

// Empty reports whether the message carries no text at all.
func (m Msg) Empty() bool { return m.En == "" && m.Zh == "" }

// T returns the text for lang, falling back to English.
func (m Msg) T(lang Lang) string {
	if lang == ZhCN && m.Zh != "" {
		return m.Zh
	}
	return m.En
}

// T resolves an inline message pair for the language in ctx.
func T(ctx context.Context, en, zh string) string {
	return Msg{En: en, Zh: zh}.T(FromContext(ctx))
}

// FromRequest picks the response language from Accept-Language. The tag with
// the highest q-value wins (earlier position breaks ties); any zh* variant
// maps to the single supported Chinese catalog, everything else to English.
// A missing or unparseable header yields English. Weights are validated per
// RFC 9110 §12.4.2 (case-insensitive "q", 0–1, at most three decimals);
// malformed entries skip their tag rather than guess.
func FromRequest(r *http.Request) Lang {
	if r == nil {
		return En
	}
	header := r.Header.Get("Accept-Language")
	if header == "" {
		return En
	}
	bestQ := -1.0
	zh := false
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag, params, _ := strings.Cut(part, ";")
		q := 1.0
		if strings.TrimSpace(params) != "" {
			v, err := parseQ(params)
			if err != nil {
				continue
			}
			q = v
		}
		if q > bestQ {
			bestQ = q
			zh = strings.HasPrefix(strings.TrimSpace(strings.ToLower(tag)), "zh")
		}
	}
	if bestQ <= 0 {
		return En
	}
	if zh {
		return ZhCN
	}
	return En
}

// qvalueRe matches an HTTP qvalue: 0–1 with at most three decimals.
var qvalueRe = regexp.MustCompile(`^(?:0(?:\.\d{0,3})?|1(?:\.0{0,3})?)$`)

// parseQ extracts and validates the q parameter from "; q=…" (parameters may
// appear in any case; only q carries meaning here).
func parseQ(params string) (float64, error) {
	_, value, ok := strings.Cut(strings.ToLower(strings.TrimSpace(params)), "=")
	if !ok {
		return 0, errors.New("no q value")
	}
	value = strings.TrimSpace(value)
	if !qvalueRe.MatchString(value) {
		return 0, errors.New("malformed qvalue")
	}
	return strconv.ParseFloat(value, 64)
}
