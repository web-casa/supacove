// Package server wires the HTTP surface: chi middleware stack, the
// contract-generated API (oapi-codegen strict server), the auth/CSRF guard,
// and the embedded SPA.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

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
	sessionCookie = "sb_session"
	csrfCookie    = "sb_csrf"
	csrfHeader    = "X-CSRF-Token"
)

// BuildInfo is injected at link time.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

type Server struct {
	store   *db.Store
	auth    *auth.Store
	cfg     *config.Config
	lim     *limiter.Limiter
	log     *slog.Logger
	build   BuildInfo
	started time.Time
}

func New(store *db.Store, authStore *auth.Store, cfg *config.Config, lim *limiter.Limiter, log *slog.Logger, build BuildInfo) *Server {
	return &Server{
		store: store, auth: authStore, cfg: cfg, lim: lim,
		log: log, build: build, started: time.Now(),
	}
}

// Router assembles the full HTTP handler.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID, chimw.RealIP, s.requestLogger, chimw.Recoverer, s.secureHeaders)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})

	apiHandler := api.HandlerFromMux(api.NewStrictHandler(&apiService{srv: s}, nil), chi.NewRouter())
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
		g.Use(s.guard)     // auth + CSRF + origin + API hygiene
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

// paths that require an authenticated session (spec: cookieAuth security).
var sessionRequired = map[string]bool{
	"/api/auth/me":        true,
	"/api/auth/logout":    true,
	"/api/health/details": true,
}

// guard enforces, for /api/*: no-store, same-origin on state changes,
// sessions on protected paths, and double-submit CSRF on protected mutations.
// Login/bootstrap stay reachable anonymously but are still origin-checked.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.HasPrefix(req.URL.Path, "/api") {
			next.ServeHTTP(w, req)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-API", "supabackup")

		if !safeMethod(req.Method) && !s.sameOrigin(req) {
			writeError(w, http.StatusForbidden, "cross_origin", "cross-origin state changes are rejected")
			return
		}
		if !sessionRequired[req.URL.Path] {
			next.ServeHTTP(w, req)
			return
		}
		user := s.sessionUser(req)
		if user == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "login required")
			return
		}
		if !safeMethod(req.Method) && !s.csrfValid(req) {
			writeError(w, http.StatusForbidden, "csrf", "missing or invalid CSRF token")
			return
		}
		raw, _ := req.Cookie(sessionCookie)
		ctx := withUser(req.Context(), user)
		ctx = context.WithValue(ctx, ctxSessionRaw, raw.Value)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func safeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// sameOrigin verifies the Origin header (when present) targets this server.
func (s *Server) sameOrigin(req *http.Request) bool {
	origin := req.Header.Get("Origin")
	if origin == "" {
		return true // non-browser clients; session-based protection still applies
	}
	_, host := schemeAndHost(origin)
	return host != "" && subtleHostEqual(host, req.Host)
}

// sessionUser resolves the session cookie to a user, or nil.
func (s *Server) sessionUser(req *http.Request) *auth.User {
	c, err := req.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	u, err := s.auth.UserForSession(req.Context(), c.Value)
	if err != nil {
		s.log.Error("session lookup", "err", err)
		return nil
	}
	return u
}

func (s *Server) csrfValid(req *http.Request) bool {
	c, err := req.Cookie(csrfCookie)
	if err != nil || c.Value == "" {
		return false
	}
	return auth.SecureEqual(c.Value, req.Header.Get(csrfHeader))
}

// context plumbing for session user + cookie jar.

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxCookies
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
		ctx = context.WithValue(ctx, ctxClientIP, clientIP(req))
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

func (s *Server) newSessionCookies(sessionRaw string) []*http.Cookie {
	return []*http.Cookie{
		{
			Name:     sessionCookie,
			Value:    sessionRaw,
			Path:     "/",
			HttpOnly: true,
			Secure:   !s.cfg.InsecureCookie,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   int(s.cfg.SessionTTL / time.Second),
		},
		{
			Name:     csrfCookie,
			Value:    newCSRFToken(),
			Path:     "/",
			HttpOnly: false, // double-submit: the SPA must read it to echo the header
			Secure:   !s.cfg.InsecureCookie,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   int(s.cfg.SessionTTL / time.Second),
		},
	}
}

func (s *Server) clearAuthCookies() []*http.Cookie {
	return []*http.Cookie{
		{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: !s.cfg.InsecureCookie, SameSite: http.SameSiteStrictMode, MaxAge: -1},
		{Name: csrfCookie, Value: "", Path: "/", Secure: !s.cfg.InsecureCookie, SameSite: http.SameSiteStrictMode, MaxAge: -1},
	}
}

// staticHandler serves the embedded SPA with SPA fallback and sane caching:
// index.html is no-store; hashed assets are immutable.
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
		// SPA fallback: client-side routing paths and index itself.
		w.Header().Set("Cache-Control", "no-store")
		req.URL.Path = "/"
		fileServer.ServeHTTP(w, req)
	})
}
