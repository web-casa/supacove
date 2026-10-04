// Package server wires the HTTP surface: chi middleware stack, the
// contract-generated API (oapi-codegen strict server), the auth/CSRF guard,
// and the embedded SPA.
package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/agekey"
	"github.com/cloudfan/supabackup/backend/internal/jobs"

	"github.com/cloudfan/supabackup/backend/internal/api"
	"github.com/cloudfan/supabackup/backend/internal/auth"
	"github.com/cloudfan/supabackup/backend/internal/config"
	"github.com/cloudfan/supabackup/backend/internal/db"
	"github.com/cloudfan/supabackup/backend/internal/limiter"
	"github.com/cloudfan/supabackup/backend/internal/web"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

const (
	csrfHeader = "X-CSRF-Token"
	// Body cap for the JSON control-plane endpoints reviewed in Phase 1.
	// Streaming backup endpoints (later phases) configure their own limits.
	apiBodyLimit = 16 << 10
)

// Cookie names. Production (Secure cookies) uses the __Host- prefix, which
// browsers pin to Path=/, no Domain, HTTPS only (review P2-01).
func (s *Server) sessionCookieName() string {
	if s.cfg.InsecureCookie {
		return "sb_session"
	}
	return "__Host-sb_session"
}

func (s *Server) csrfCookieName() string {
	if s.cfg.InsecureCookie {
		return "sb_csrf"
	}
	return "__Host-sb_csrf"
}

// BuildInfo is injected at link time.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

type Server struct {
	store      *db.Store
	auth       *auth.Store
	cfg        *config.Config
	key        []byte // application master secret; CSRF tokens are HMAC-bound to sessions with it
	lim        *limiter.Limiter
	log        *slog.Logger
	build      BuildInfo
	started    time.Time
	runner     *jobs.Runner // backup kernel (Phase 2); may be nil in pure-API tests
	stagingDir string
}

// recipientFor reads the configured age recipient from settings (protocol B).
// A database error is returned as an error — never silently reported as
// "not configured" (round-1 review P2-07).
func (s *Server) recipientFor(ctx context.Context) (string, error) {
	var r string
	err := s.store.DB.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = 'age_recipient'`).Scan(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return r, err
}

// RecipientFor is the exported accessor used by the backup kernel.
func (s *Server) RecipientFor(ctx context.Context) (string, error) {
	return s.recipientFor(ctx)
}

// storeRecipient persists the recipient and its fingerprint in ONE
// transaction — a partial update can never pair recipient B with key ID A
// (round-1 review P2-07).
func (s *Server) storeRecipient(ctx context.Context, recipient string) error {
	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	fingerprint := agekey.Fingerprint(recipient)
	for _, kv := range [][2]string{
		{"age_recipient", recipient},
		{"age_key_id", fingerprint},
	} {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, kv[0], kv[1]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetRunner attaches the backup kernel after construction (main.go owns the
// lifecycle: migrations → runner → server) and records the staging directory
// for local downloads.
func (s *Server) SetRunner(r *jobs.Runner, stagingDir string) {
	s.runner = r
	s.stagingDir = stagingDir
}

func (s *Server) Runner() *jobs.Runner { return s.runner }

func New(store *db.Store, authStore *auth.Store, cfg *config.Config, key []byte, lim *limiter.Limiter, log *slog.Logger, build BuildInfo) *Server {
	return &Server{
		store: store, auth: authStore, cfg: cfg, key: key, lim: lim,
		log: log, build: build, started: time.Now(),
	}
}

// Router assembles the full HTTP handler.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID, s.requestLogger, s.recoverer, s.secureHeaders)
	// Only trust X-Forwarded-For from explicitly configured proxy ranges
	// (review P0-01: unconditional RealIP let any client rotate spoofed
	// headers to bypass login rate limiting).
	if cidrs, err := s.cfg.TrustedProxyCIDRs(); err == nil && len(cidrs) > 0 {
		prefixes := make([]string, 0, len(cidrs))
		for _, c := range cidrs {
			prefixes = append(prefixes, c.String())
		}
		r.Use(chimw.ClientIPFromXFF(prefixes...))
	}
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})

	strictOpts := api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			// Decode failures must use the contract Error shape; an
			// oversized body is 413, everything else is a 400.
			var mbe *http.MaxBytesError
			if errorsAs(err, &mbe) {
				writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large",
					"request body exceeds the allowed size")
				return
			}
			writeError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			s.log.Error("handler error", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "unexpected internal error")
		},
	}
	apiRouter := chi.NewRouter()
	apiRouter.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	})
	apiRouter.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})
	// HandlerWithOptions (not HandlerFromMux) so that std-layer parameter
	// errors (e.g. the contract-required CSRF header on logout) also use the
	// JSON Error shape instead of text/plain http.Error (review round 3,
	// P1-09 remainder).
	apiHandler := api.HandlerWithOptions(api.NewStrictHandlerWithOptions(&apiService{srv: s}, nil, strictOpts), api.ChiServerOptions{
		BaseRouter: apiRouter,
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		},
	})
	if mux, ok := apiHandler.(*chi.Mux); ok {
		mux.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "not_found", "resource not found")
		})
		mux.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		})
	}
	// The API mounts under the real /api prefix (spec servers.url = /api); the
	// SPA wildcard is registered separately, so the two never fight for the
	// root wildcard node.
	r.Group(func(g chi.Router) {
		g.Use(s.cookieJar) // lets strict handlers set cookies before the response is written
		g.Use(s.guard)     // auth + CSRF + origin + API hygiene (default-deny)
		g.Get("/metrics", s.handleMetrics)
		g.Mount("/api", apiHandler)
	})

	r.Handle("/*", s.staticHandler())
	return r
}

// requestLogger logs one structured line per request.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ww := chimw.NewWrapResponseWriter(w, req.ProtoMajor)
		start := time.Now()
		defer func() {
			s.log.Info("http",
				"method", req.Method,
				"path", req.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", chimw.GetReqID(req.Context()),
			)
		}()
		next.ServeHTTP(ww, req)
	})
}

// recoverer converts handler panics into the contract Error shape.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			if rv := recover(); rv != nil {
				s.log.Error("panic recovered", "panic", fmt.Sprint(rv), "stack", string(debug.Stack()))
				if strings.HasPrefix(req.URL.Path, "/api") {
					writeError(w, http.StatusInternalServerError, "internal", "unexpected internal error")
				} else {
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}
		}()
		next.ServeHTTP(w, req)
	})
}

// secureHeaders applies baseline hardening to every response.
func (s *Server) secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; connect-src 'self'")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, req)
	})
}

// anonymousAPI lists the (method, path) pairs reachable without a session.
// Everything else under /api requires authentication — default deny, so a
// future protected operation cannot become anonymous by forgetting to add it
// to a table (review P2-01).
var anonymousAPI = map[string]map[string]bool{
	http.MethodGet: {
		"/api/healthz": true,
		"/api/ready":   true,
	},
	http.MethodPost: {
		"/api/auth/bootstrap": true,
		"/api/auth/login":     true,
	},
}

// loginPath is rate limited before request-body decoding (review P0-02).
const loginPath = "/api/auth/login"

// guard enforces, for /api/*: no-store, body cap, same-origin on state
// changes, session requirement (default deny), and session-bound CSRF on
// protected mutations. Login rate limiting happens here, before any body
// decoding or password hashing.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.HasPrefix(req.URL.Path, "/api") {
			next.ServeHTTP(w, req)
			return
		}
		w.Header().Set("Cache-Control", "no-store")

		// Cap JSON body size before any decoding or hashing work.
		if req.Body != nil {
			req.Body = http.MaxBytesReader(w, req.Body, apiBodyLimit)
		}
		// JSON media type is required and must be exactly application/json
		// (parameters like charset are fine; jsonp or missing types are not —
		// review round 2, P1-09 remainder).
		if !safeMethod(req.Method) && req.Body != nil && req.ContentLength != 0 {
			ct := req.Header.Get("Content-Type")
			mt, _, ok := strings.Cut(strings.ToLower(ct), ";")
			mt = strings.TrimSpace(mt)
			if !ok && mt == "" {
				mt = strings.TrimSpace(strings.ToLower(ct))
			}
			if mt != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
					"Content-Type must be application/json")
				return
			}
		}
		// A repeated Origin header is never a valid browser signature.
		if len(req.Header.Values("Origin")) > 1 {
			writeError(w, http.StatusForbidden, "cross_origin", "invalid Origin header")
			return
		}
		if !safeMethod(req.Method) && !s.sameOrigin(req) {
			writeError(w, http.StatusForbidden, "cross_origin", "cross-origin state changes are rejected")
			return
		}

		// The ANONYMOUS auth endpoints (login, bootstrap only) get bounded,
		// fully-read JSON plus pre-decode rate limiting. CRITICAL: this
		// fast path must never cover protected endpoints — a logout with a
		// JSON body must still pass the session and CSRF checks below, or the
		// server-side session would survive a supposed logout (review round 3,
		// R3-P1-02).
		if !safeMethod(req.Method) && anonymousAPI[req.Method][req.URL.Path] {
			// Rate limit BEFORE reading or decoding anything, including empty
			// bodies (review P0-02 remainder: empty login bodies must not
			// bypass the limiter).
			ip := s.clientIP(req)
			if !s.lim.Allow(ip) {
				writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
				return
			}
			if req.ContentLength != 0 {
				if _, ok := s.readWholeJSON(w, req); !ok {
					return // response already written
				}
			}
			lw := &loginAttemptWriter{ResponseWriter: w}
			next.ServeHTTP(lw, req)
			switch lw.status {
			case http.StatusOK, http.StatusCreated:
				s.lim.Reset(ip)
			case http.StatusUnauthorized, http.StatusBadRequest, http.StatusForbidden:
				s.lim.Fail(ip)
			}
			return
		}

		if anonymousAPI[req.Method][req.URL.Path] {
			// Anonymous request without a body (or GET): straight through.
			next.ServeHTTP(w, req)
			return
		}

		user, raw, err := s.session(req)
		if err != nil {
			s.log.Error("session lookup", "err", err)
			writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "session store unavailable")
			return
		}
		if user == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "login required")
			return
		}
		if !safeMethod(req.Method) && !s.csrfValid(raw, req) {
			writeError(w, http.StatusForbidden, "csrf", "missing or invalid CSRF token")
			return
		}
		ctx := withUser(req.Context(), user)
		ctx = context.WithValue(ctx, ctxSessionRaw, raw)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

// readWholeJSON reads the (already size-capped) request body completely and
// verifies it is exactly one JSON value, then rewires the body for downstream
// decoding. Failures write the error response and return false.
func (s *Server) readWholeJSON(w http.ResponseWriter, req *http.Request) ([]byte, bool) {
	raw, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		var mbe *http.MaxBytesError
		if errorsAs(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large",
				"request body exceeds the allowed size")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request", "could not read request body")
		}
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(new(json.RawMessage)); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return nil, false
	}
	// A second Decode MUST hit exactly EOF: More() only looks inside the
	// current container and would accept trailing garbage (review round 3,
	// P0-02 remainder).
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must contain exactly one JSON value")
		return nil, false
	}
	req.Body = io.NopCloser(bytes.NewReader(raw))
	req.ContentLength = int64(len(raw))
	return raw, true
}

// loginAttemptWriter records the status code so the guard can credit or debit
// the rate limiter after the handler ran.
type loginAttemptWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (l *loginAttemptWriter) WriteHeader(code int) {
	if !l.wroteHeader {
		l.status = code
		l.wroteHeader = true
	}
	l.ResponseWriter.WriteHeader(code)
}

func (l *loginAttemptWriter) Write(b []byte) (int, error) {
	if !l.wroteHeader {
		l.status = http.StatusOK
		l.wroteHeader = true
	}
	return l.ResponseWriter.Write(b)
}

func safeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// sameOrigin verifies the Origin header (when present) targets this server:
// scheme, host and effective port must all match (review P1-01). With
// SB_PUBLIC_ORIGIN configured, only that exact origin passes — the mechanism
// for TLS-terminating proxies, instead of trusting client-sent headers.
func (s *Server) sameOrigin(req *http.Request) bool {
	origin := req.Header.Get("Origin")
	if origin == "" {
		return true // non-browser clients; session + CSRF checks still apply
	}
	oScheme, oHost, oPort, err := originParts(origin)
	if err != nil {
		return false
	}
	var eScheme, eHost, ePort string
	if s.cfg.PublicOrigin != "" {
		eScheme, eHost, ePort, err = originParts(s.cfg.PublicOrigin)
		if err != nil {
			return false // misconfiguration fails closed
		}
	} else {
		eScheme = requestScheme(req)
		eHost, ePort, err = hostPort(eScheme, req.Host)
		if err != nil {
			return false
		}
	}
	return oScheme == eScheme && oHost == eHost && oPort == ePort
}

func hostPort(scheme, hostport string) (string, string, error) {
	h, p, err := net.SplitHostPort(hostport)
	if err != nil {
		h, p = hostport, ""
	}
	h = strings.ToLower(strings.Trim(h, "[]"))
	if h == "" {
		return "", "", errors.New("empty host")
	}
	if ip := net.ParseIP(h); ip != nil {
		h = ip.String()
	}
	return h, effectivePort(scheme, h, p), nil
}

// session resolves the session cookie: (user, rawID, nil) valid; (nil, "", nil)
// no/invalid session; (nil, "", err) storage failure.
func (s *Server) session(req *http.Request) (*auth.User, string, error) {
	c, err := req.Cookie(s.sessionCookieName())
	if err != nil || c.Value == "" {
		return nil, "", nil
	}
	u, err := s.auth.UserForSession(req.Context(), c.Value)
	if err != nil || u == nil {
		return nil, "", err
	}
	return u, c.Value, nil
}

// csrfValid checks the header against an HMAC bound to the current session id
// and the master secret (review P2-01): a stolen CSRF cookie alone is useless
// without the session, and values are not client-chosen.
func (s *Server) csrfValid(sessionRaw string, req *http.Request) bool {
	if sessionRaw == "" {
		return false
	}
	return auth.SecureEqual(s.csrfToken(sessionRaw), req.Header.Get(csrfHeader))
}

func (s *Server) csrfToken(sessionRaw string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte("csrf:" + sessionRaw))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// context plumbing for session user + cookie jar.

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxCookies
	ctxSessionRaw
	ctxClientIP
)

func withUser(ctx context.Context, u *auth.User) context.Context {
	return context.WithValue(ctx, ctxUser, u)
}

func userFrom(ctx context.Context) *auth.User {
	u, _ := ctx.Value(ctxUser).(*auth.User)
	return u
}

// cookieJar injects a cookie recorder and the client IP so strict handlers
// (which only receive a request) can set cookies and rate-limit by source.
func (s *Server) cookieJar(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		jar := &cookieJar{w: w}
		ctx := context.WithValue(req.Context(), ctxCookies, jar)
		ctx = context.WithValue(ctx, ctxClientIP, s.clientIP(req))
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

type cookieJar struct {
	w http.ResponseWriter
}

func (c *cookieJar) SetCookie(cookie *http.Cookie) {
	http.SetCookie(c.w, cookie)
}

func jarFrom(ctx context.Context) *cookieJar {
	jar, _ := ctx.Value(ctxCookies).(*cookieJar)
	return jar
}

// clientIP returns the connection peer, or the X-Forwarded-For result when the
// peer itself is a trusted proxy (chi ClientIPFromXFF has already evaluated
// that and stashed it in the context).
func (s *Server) clientIP(req *http.Request) string {
	if ip := chimw.GetClientIP(req.Context()); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

func (s *Server) newSessionCookies(sessionRaw string, ttl time.Duration) []*http.Cookie {
	secure := !s.cfg.InsecureCookie
	return []*http.Cookie{
		{
			Name: s.sessionCookieName(), Value: sessionRaw, Path: "/",
			HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
			MaxAge: int(ttl / time.Second),
		},
		{
			Name: s.csrfCookieName(), Value: s.csrfToken(sessionRaw), Path: "/",
			HttpOnly: false, // double submit: the SPA must read it to echo the header
			Secure:   secure, SameSite: http.SameSiteStrictMode,
			MaxAge: int(ttl / time.Second),
		},
	}
}

func (s *Server) clearAuthCookies() []*http.Cookie {
	secure := !s.cfg.InsecureCookie
	return []*http.Cookie{
		{Name: s.sessionCookieName(), Value: "", Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: -1},
		{Name: s.csrfCookieName(), Value: "", Path: "/", Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: -1},
	}
}

// staticHandler serves the embedded SPA with SPA fallback and sane caching:
// index.html is no-store; hashed assets are immutable. When the frontend has
// never been built (dist contains no index.html) it answers 503 with a clear
// hint instead of an empty page.
func (s *Server) staticHandler() http.Handler {
	dist, err := fsSub(web.Dist, "dist")
	if err != nil {
		panic(fmt.Sprintf("embedded web dist missing: %v", err))
	}
	fileServer := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		path := strings.TrimPrefix(req.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if st, err := fsStat(dist, path); err == nil && !st.IsDir() && !strings.HasSuffix(path, ".html") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			fileServer.ServeHTTP(w, req)
			return
		}
		if _, err := fsStat(dist, "index.html"); err != nil {
			writeError(w, http.StatusServiceUnavailable, "frontend_not_built",
				"web UI is not built into this binary; run `make build`")
			return
		}
		// SPA fallback: client-side routing paths and index itself.
		w.Header().Set("Cache-Control", "no-store")
		req.URL.Path = "/"
		fileServer.ServeHTTP(w, req)
	})
}
