package server

import (
	"context"
	"errors"
	"runtime"
	"time"
	"unicode/utf8"

	"github.com/web-casa/supacove/backend/internal/api"
	"github.com/web-casa/supacove/backend/internal/auth"
	"github.com/web-casa/supacove/backend/internal/i18n"
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
		return api.GetHealthDetails401JSONResponse{Code: "unauthenticated", Message: i18n.T(ctx, "login required", "需要登录")}, nil
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
		return api.PostAuthBootstrap400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "token, username and password are required", "token、用户名和密码为必填项")}, nil
	}
	if err := auth.ValidateUsername(body.Username); err != nil {
		return api.PostAuthBootstrap400JSONResponse{Code: "invalid_request", Message: authMsg(ctx, err)}, nil
	}
	if err := auth.ValidatePassword(body.Password); err != nil {
		return api.PostAuthBootstrap400JSONResponse{Code: "invalid_request", Message: authMsg(ctx, err)}, nil
	}

	// Bootstrap creates the admin AND the first session in one transaction;
	// a failure leaves neither (review P1-02).
	user, sessionRaw, err := a.srv.auth.Bootstrap(ctx, body.Token, body.Username, body.Password)
	switch {
	case errors.Is(err, auth.ErrAlreadyInitialized):
		return api.PostAuthBootstrap409JSONResponse{Code: "already_initialized", Message: i18n.T(ctx, "an admin user already exists; use the CLI reset-password command locally", "管理员账号已存在；请在本地使用 CLI 的 reset-password 命令")}, nil
	case errors.Is(err, auth.ErrInvalidToken):
		return api.PostAuthBootstrap403JSONResponse{Code: "invalid_bootstrap_token", Message: i18n.T(ctx, "bootstrap token is invalid, already used, or expired", "引导令牌无效、已被使用或已过期")}, nil
	case errors.Is(err, auth.ErrKDFBusy):
		return api.PostAuthBootstrap429JSONResponse{RateLimitedJSONResponse: api.RateLimitedJSONResponse(errJSON("rate_limited", i18n.T(ctx, "too many attempts, try again later", "尝试次数过多，请稍后再试")))}, nil
	case err != nil:
		a.srv.log.Error("bootstrap", "err", err)
		return api.PostAuthBootstrap500JSONResponse{InternalJSONResponse: api.InternalJSONResponse(api.Error(errJSON("internal", i18n.T(ctx, "bootstrap failed", "初始化失败"))))}, nil
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
		return api.PostAuthLogin400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "username and password are required", "用户名和密码为必填项")}, nil
	}
	// Runtime field limits mirror the contract in CHARACTER terms (review
	// P0-02/P1-09 remainder): both bounds are enforced before the KDF.
	if n := utf8.RuneCountInString(body.Username); n < 3 || n > 64 {
		return api.PostAuthLogin400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "username must be 3-64 characters", "用户名长度需为 3–64 个字符")}, nil
	}
	if n := utf8.RuneCountInString(body.Password); n < 12 || n > 128 {
		return api.PostAuthLogin400JSONResponse{Code: "invalid_request", Message: i18n.T(ctx, "password must be 12-128 characters", "密码长度需为 12–128 个字符")}, nil
	}

	// Rate limiting already happened in the guard before decoding; issue the
	// session through the generation-checked store method (review P1-03).
	user, sessionRaw, err := a.srv.auth.IssueSession(ctx, body.Username, body.Password, auth.SessionTTL())
	switch {
	case errors.Is(err, auth.ErrBadCredentials):
		// Deliberately vague: no username oracle. Failures are debited by the
		// guard from the response status.
		return api.PostAuthLogin401JSONResponse{Code: "invalid_credentials", Message: i18n.T(ctx, "invalid username or password", "用户名或密码错误")}, nil
	case errors.Is(err, auth.ErrKDFBusy):
		return api.PostAuthLogin503JSONResponse{OverloadedJSONResponse: api.OverloadedJSONResponse(api.Error(errJSON("overloaded", i18n.T(ctx, "password hashing busy, try again", "密码哈希繁忙，请稍后再试"))))}, nil
	case err != nil:
		a.srv.log.Error("login", "err", err)
		return api.PostAuthLogin500JSONResponse{InternalJSONResponse: api.InternalJSONResponse(api.Error(errJSON("internal", i18n.T(ctx, "login failed", "登录失败"))))}, nil
	}

	jar := jarFrom(ctx)
	if jar == nil {
		// Session could not be attached: refuse to claim login success.
		a.srv.log.Error("login: cookie jar missing")
		return api.PostAuthLogin500JSONResponse{InternalJSONResponse: api.InternalJSONResponse(api.Error(errJSON("internal", i18n.T(ctx, "login failed", "登录失败"))))}, nil
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
			return api.PostAuthLogout500JSONResponse{InternalJSONResponse: api.InternalJSONResponse(api.Error(errJSON("internal", i18n.T(ctx, "logout could not be completed server-side; the session is still active", "服务端无法完成注销；会话仍然有效"))))}, nil
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
		return api.GetAuthMe401JSONResponse{Code: "unauthenticated", Message: i18n.T(ctx, "login required", "需要登录")}, nil
	}
	return api.GetAuthMe200JSONResponse(api.User{Id: user.ID, Username: user.Username, CreatedAt: user.CreatedAt}), nil
}

// context helpers for values the guard middleware resolves from the raw request
// (strict handlers do not receive *http.Request).

func sessionRawFrom(ctx context.Context) string {
	raw, _ := ctx.Value(ctxSessionRaw).(string)
	return raw
}
