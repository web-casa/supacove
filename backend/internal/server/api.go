package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"runtime"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/api"
	"github.com/cloudfan/supabackup/backend/internal/auth"
)

// apiService implements the generated strict interface; all authorization is
// enforced by the guard middleware before these handlers run.
type apiService struct {
	srv *Server
}

func (a *apiService) GetHealthz(_ context.Context, _ api.GetHealthzRequestObject) (api.GetHealthzResponseObject, error) {
	return api.GetHealthz200JSONResponse{Status: api.Ok}, nil
}

func (a *apiService) GetReady(_ context.Context, _ api.GetReadyRequestObject) (api.GetReadyResponseObject, error) {
	unavailable := func() (api.GetReadyResponseObject, error) {
		return api.GetReady503JSONResponse{Status: api.Unavailable}, nil
	}
	ready, err := a.srv.store.SchemaReady()
	if err != nil {
		a.srv.log.Error("readiness schema check", "err", err)
		return unavailable()
	}
	if !ready {
		return unavailable()
	}
	if err := a.srv.store.DB.Ping(); err != nil {
		a.srv.log.Error("readiness ping", "err", err)
		return unavailable()
	}
	return api.GetReady200JSONResponse{Status: api.Ok}, nil
}

func (a *apiService) GetHealthDetails(ctx context.Context, _ api.GetHealthDetailsRequestObject) (api.GetHealthDetailsResponseObject, error) {
	_ = userFrom(ctx) // guaranteed non-nil by guard; handler exists for diagnostics only
	st := a.srv.store.DB.Stats()
	return api.GetHealthDetails200JSONResponse{
		Version:         a.srv.build.Version,
		Commit:          a.srv.build.Commit,
		GoVersion:       runtime.Version(),
		UptimeSeconds:   int64(time.Since(a.srv.started) / time.Second),
		SqliteOpenConns: &st.OpenConnections,
	}, nil
}

func (a *apiService) PostAuthBootstrap(ctx context.Context, request api.PostAuthBootstrapRequestObject) (api.PostAuthBootstrapResponseObject, error) {
	body := request.Body
	if body == nil {
		return api.PostAuthBootstrap400JSONResponse{Code: "invalid_request", Message: "request body required"}, nil
	}
	if err := auth.ValidateUsername(body.Username); err != nil {
		return api.PostAuthBootstrap400JSONResponse{Code: "invalid_request", Message: err.Error()}, nil
	}
	if err := auth.ValidatePassword(body.Password); err != nil {
		return api.PostAuthBootstrap400JSONResponse{Code: "invalid_request", Message: err.Error()}, nil
	}

	user, err := a.srv.auth.Bootstrap(ctx, body.Token, body.Username, body.Password)
	switch {
	case errors.Is(err, auth.ErrAlreadyInitialized):
		return api.PostAuthBootstrap409JSONResponse{Code: "already_initialized", Message: "an admin user already exists; use `supabackup reset-password` locally"}, nil
	case errors.Is(err, auth.ErrInvalidToken):
		return api.PostAuthBootstrap403JSONResponse{Code: "invalid_bootstrap_token", Message: "bootstrap token is invalid, already used, or expired"}, nil
	case err != nil:
		a.srv.log.Error("bootstrap", "err", err)
		return api.PostAuthBootstrap400JSONResponse{Code: "internal", Message: "bootstrap failed"}, nil
	}

	jar := jarFrom(ctx)
	if raw, err := a.srv.auth.CreateSession(ctx, user.ID, a.srv.cfg.SessionTTL); err == nil && jar != nil {
		for _, c := range a.srv.newSessionCookies(raw) {
			jar.SetCookie(c)
		}
	}
	return api.PostAuthBootstrap201JSONResponse(api.User{Id: user.ID, Username: user.Username, CreatedAt: user.CreatedAt}), nil
}

func (a *apiService) PostAuthLogin(ctx context.Context, request api.PostAuthLoginRequestObject) (api.PostAuthLoginResponseObject, error) {
	body := request.Body
	if body == nil {
		return api.PostAuthLogin400JSONResponse{Code: "invalid_request", Message: "request body required"}, nil
	}
	ip := clientIPFrom(ctx)
	if !a.srv.lim.Allow(ip) {
		return api.PostAuthLogin429JSONResponse{Code: "rate_limited", Message: "too many attempts, try again later"}, nil
	}

	user, err := a.srv.auth.Login(ctx, body.Username, body.Password)
	if errors.Is(err, auth.ErrBadCredentials) {
		a.srv.lim.Fail(ip)
		// Deliberately vague: no username oracle.
		return api.PostAuthLogin401JSONResponse{Code: "invalid_credentials", Message: "invalid username or password"}, nil
	}
	if err != nil {
		a.srv.log.Error("login", "err", err)
		return api.PostAuthLogin400JSONResponse{Code: "internal", Message: "login failed"}, nil
	}
	a.srv.lim.Reset(ip)

	jar := jarFrom(ctx)
	if raw, err := a.srv.auth.CreateSession(ctx, user.ID, a.srv.cfg.SessionTTL); err == nil && jar != nil {
		for _, c := range a.srv.newSessionCookies(raw) {
			jar.SetCookie(c)
		}
	}
	return api.PostAuthLogin200JSONResponse(api.User{Id: user.ID, Username: user.Username, CreatedAt: user.CreatedAt}), nil
}

func (a *apiService) PostAuthLogout(ctx context.Context, _ api.PostAuthLogoutRequestObject) (api.PostAuthLogoutResponseObject, error) {
	if raw := sessionRawFrom(ctx); raw != "" {
		if err := a.srv.auth.DeleteSession(ctx, raw); err != nil {
			a.srv.log.Error("logout", "err", err)
		}
	}
	if jar := jarFrom(ctx); jar != nil {
		for _, c := range a.srv.clearAuthCookies() {
			jar.SetCookie(c)
		}
	}
	return api.PostAuthLogout204Response{}, nil
}

func (a *apiService) GetAuthMe(ctx context.Context, _ api.GetAuthMeRequestObject) (api.GetAuthMeResponseObject, error) {
	user := userFrom(ctx)
	if user == nil {
		return api.GetAuthMe401JSONResponse{Code: "unauthenticated", Message: "login required"}, nil
	}
	return api.GetAuthMe200JSONResponse(api.User{Id: user.ID, Username: user.Username, CreatedAt: user.CreatedAt}), nil
}

// context helpers for values the guard middleware resolves from the raw request
// (strict handlers do not receive *http.Request).

const (
	ctxClientIP ctxKey = iota + 100
	ctxSessionRaw
)

func clientIPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(ctxClientIP).(string)
	return ip
}

func sessionRawFrom(ctx context.Context) string {
	raw, _ := ctx.Value(ctxSessionRaw).(string)
	return raw
}

func clientIP(req *http.Request) string {
	// chi RealIP may have rewritten RemoteAddr from X-Forwarded-For; trust that
	// only when deployed behind the operator's own proxy (documented).
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}
