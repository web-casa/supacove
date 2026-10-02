package server

import (
	"context"
	"errors"
	"runtime"
	"time"

	"github.com/cloudfan/supabackup/backend/internal/api"
	"github.com/cloudfan/supabackup/backend/internal/auth"
)

// errJSON builds a populated contract Error for the generated wrapper types.
func errJSON(code, message string) api.Error {
	return api.Error{Code: code, Message: message}
}

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
	if userFrom(ctx) == nil {
		// Defensive fail-safe: the guard guarantees a user here, but a future
		// refactor must not silently serve diagnostics anonymously.
		return api.GetHealthDetails401JSONResponse{Code: "unauthenticated", Message: "login required"}, nil
	}
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
	if body == nil || body.Token == "" || body.Username == "" || body.Password == "" {
		return api.PostAuthBootstrap400JSONResponse{Code: "invalid_request", Message: "token, username and password are required"}, nil
	}
	if err := auth.ValidateUsername(body.Username); err != nil {
		return api.PostAuthBootstrap400JSONResponse{Code: "invalid_request", Message: err.Error()}, nil
	}
	if err := auth.ValidatePassword(body.Password); err != nil {
		return api.PostAuthBootstrap400JSONResponse{Code: "invalid_request", Message: err.Error()}, nil
	}

	// Bootstrap creates the admin AND the first session in one transaction;
	// a failure leaves neither (review P1-02).
	user, sessionRaw, err := a.srv.auth.Bootstrap(ctx, body.Token, body.Username, body.Password)
	switch {
	case errors.Is(err, auth.ErrAlreadyInitialized):
		return api.PostAuthBootstrap409JSONResponse{Code: "already_initialized", Message: "an admin user already exists; use the CLI reset-password command locally"}, nil
	case errors.Is(err, auth.ErrInvalidToken):
		return api.PostAuthBootstrap403JSONResponse{Code: "invalid_bootstrap_token", Message: "bootstrap token is invalid, already used, or expired"}, nil
	case errors.Is(err, auth.ErrKDFBusy):
		return api.PostAuthBootstrap429JSONResponse{RateLimitedJSONResponse: api.RateLimitedJSONResponse(errJSON("rate_limited", "too many attempts, try again later"))}, nil
	case err != nil:
		a.srv.log.Error("bootstrap", "err", err)
		return api.PostAuthBootstrap500JSONResponse{InternalJSONResponse: api.InternalJSONResponse(api.Error(errJSON("internal", "bootstrap failed")))}, nil
	}

	if jar := jarFrom(ctx); jar != nil {
		for _, c := range a.srv.newSessionCookies(sessionRaw, auth.SessionTTL()) {
			jar.SetCookie(c)
		}
	}
	return api.PostAuthBootstrap201JSONResponse(api.User{Id: user.ID, Username: user.Username, CreatedAt: user.CreatedAt}), nil
}

func (a *apiService) PostAuthLogin(ctx context.Context, request api.PostAuthLoginRequestObject) (api.PostAuthLoginResponseObject, error) {
	body := request.Body
	if body == nil || body.Username == "" || body.Password == "" {
		return api.PostAuthLogin400JSONResponse{Code: "invalid_request", Message: "username and password are required"}, nil
	}
	// Runtime field limits mirror the contract (review P0-02 remainder):
	// oversized inputs are rejected before touching the KDF.
	if len(body.Username) > 64 || len(body.Password) > 128 {
		return api.PostAuthLogin400JSONResponse{Code: "invalid_request", Message: "username or password exceeds the allowed length"}, nil
	}

	// Rate limiting already happened in the guard before decoding; issue the
	// session through the generation-checked store method (review P1-03).
	user, sessionRaw, err := a.srv.auth.IssueSession(ctx, body.Username, body.Password, auth.SessionTTL())
	switch {
	case errors.Is(err, auth.ErrBadCredentials):
		// Deliberately vague: no username oracle. Failures are debited by the
		// guard from the response status.
		return api.PostAuthLogin401JSONResponse{Code: "invalid_credentials", Message: "invalid username or password"}, nil
	case errors.Is(err, auth.ErrKDFBusy):
		return api.PostAuthLogin503JSONResponse{OverloadedJSONResponse: api.OverloadedJSONResponse(api.Error(errJSON("overloaded", "password hashing busy, try again")))}, nil
	case err != nil:
		a.srv.log.Error("login", "err", err)
		return api.PostAuthLogin500JSONResponse{InternalJSONResponse: api.InternalJSONResponse(api.Error(errJSON("internal", "login failed")))}, nil
	}

	jar := jarFrom(ctx)
	if jar == nil {
		// Session could not be attached: refuse to claim login success.
		a.srv.log.Error("login: cookie jar missing")
		return api.PostAuthLogin500JSONResponse{InternalJSONResponse: api.InternalJSONResponse(api.Error(errJSON("internal", "login failed")))}, nil
	}
	for _, c := range a.srv.newSessionCookies(sessionRaw, auth.SessionTTL()) {
		jar.SetCookie(c)
	}
	return api.PostAuthLogin200JSONResponse(api.User{Id: user.ID, Username: user.Username, CreatedAt: user.CreatedAt}), nil
}

func (a *apiService) PostAuthLogout(ctx context.Context, _ api.PostAuthLogoutRequestObject) (api.PostAuthLogoutResponseObject, error) {
	raw := sessionRawFrom(ctx)
	if raw != "" {
		if err := a.srv.auth.DeleteSession(ctx, raw); err != nil {
			// Server-side revocation failed: do NOT report success even if the
			// browser clears its cookie (review P1-02).
			a.srv.log.Error("logout", "err", err)
			return api.PostAuthLogout500JSONResponse{InternalJSONResponse: api.InternalJSONResponse(api.Error(errJSON("internal", "logout could not be completed server-side; the session is still active")))}, nil
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

func clientIPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(ctxClientIP).(string)
	return ip
}

func sessionRawFrom(ctx context.Context) string {
	raw, _ := ctx.Value(ctxSessionRaw).(string)
	return raw
}
