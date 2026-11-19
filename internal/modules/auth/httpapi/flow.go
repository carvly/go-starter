package httpapi

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/labstack/echo/v5"

	"app.local/internal/kit/logctx"
	"app.local/internal/modules/auth/domain"
	"app.local/internal/modules/auth/httpapi/oapi"
	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/oauth"
)

func logFailedVerification(c *echo.Context, provider string, err error) {
	logctx.From(c.Request().Context()).WarnContext(c.Request().Context(),
		"auth: third-party credential verification failed", "provider", provider, "error", err)
}

// 回调跳回前端的固定路径；result 取值 ok 或错误码。
const completePath = "/auth/complete"

func completeURL(intent domain.OAuthFlowIntent, result string) string {
	q := url.Values{}
	q.Set("intent", string(intent))
	q.Set("result", result)
	return completePath + "?" + q.Encode()
}

// handleOAuthCallback 是浏览器授权回调的编排（见 docs/agents/auth.md 的"登录入口"）：
// 一次性取 state → 校验绑定 cookie → 按 intent 分发。登录 intent 在这里完成
// code 换身份，把结果写成一次性兑换（60 秒）再跳回前端；绑定/重新验证 intent
// 在这里直接完成。任何失败都跳回前端 /auth/complete?result=<错误码>，只有
// state 本身无效时才直接 400（无处可跳）。
func (h *Handler) handleOAuthCallback(c *echo.Context, provider string, params oapi.OauthCallbackParams) error {
	ctx := c.Request().Context()
	st, err := h.service.TakeOAuthState(ctx, params.State)
	if err != nil {
		// state 无效或已过期：可能是伪造/重放的回调，也可能是用户停留太久，
		// 无处可安全跳转，直接 400。
		return echo.NewHTTPError(http.StatusBadRequest, "invalid or expired oauth state")
	}
	prefix := domain.ExchangeCodePrefix(params.State)
	if err := h.checkBindingCookie(c, prefix, st.BindingHash); err != nil {
		return err
	}
	if st.Provider != provider {
		return echo.NewHTTPError(http.StatusBadRequest, "provider mismatch")
	}
	// 服务号网页快照页的虚拟账号一律拒绝。
	if params.IsSnapshotuser != nil && *params.IsSnapshotuser == "1" {
		return c.Redirect(http.StatusFound, completeURL(st.Intent, "snapshot_user"))
	}

	entry, ok := h.oauth.Browser(provider)
	if !ok {
		return errUnknownProvider(provider)
	}

	if st.Intent == domain.IntentLogin {
		return h.completeLoginCallback(c, entry, st, params)
	}

	// 绑定 / 重新验证：在回调里直接完成，绑定 cookie 随之清除。
	defer clearBindingCookie(c, prefix)
	if params.Code == nil || *params.Code == "" {
		return c.Redirect(http.StatusFound, completeURL(st.Intent, "login_failed"))
	}
	idn, err := entry.Provider.Exchange(ctx, *params.Code, "", "")
	if err != nil {
		logFailedVerification(c, provider, err)
		return c.Redirect(http.StatusFound, completeURL(st.Intent, "login_failed"))
	}
	switch st.Intent {
	case domain.IntentBind:
		if _, err := h.service.BindIdentity(ctx, st.SID, toExternalIdentity(idn)); err != nil {
			return c.Redirect(http.StatusFound, completeURL(st.Intent, resultCodeOf(err)))
		}
	case domain.IntentReauth:
		if err := h.service.Reauthenticate(ctx, st.SID, toExternalIdentity(idn)); err != nil {
			return c.Redirect(http.StatusFound, completeURL(st.Intent, resultCodeOf(err)))
		}
	}
	return c.Redirect(http.StatusFound, completeURL(st.Intent, "ok"))
}

// completeLoginCallback 是登录回调：code 换身份后把结果写成一次性兑换，
// 绑定 cookie 留到兑换时清除（跳转到前端的是跨站响应，在这里种 SameSite=Strict
// 的 cookie 行为不一致，换成兑换码 + 同站请求交付）。
func (h *Handler) completeLoginCallback(c *echo.Context, entry oauth.Entry, st *domain.OAuthState, params oapi.OauthCallbackParams) error {
	if params.Code == nil || *params.Code == "" {
		return c.Redirect(http.StatusFound, completeURL(domain.IntentLogin, "login_failed"))
	}
	idn, err := entry.Provider.Exchange(c.Request().Context(), *params.Code, "", "")
	if err != nil {
		logFailedVerification(c, st.Provider, err)
		return c.Redirect(http.StatusFound, completeURL(domain.IntentLogin, "login_failed"))
	}
	code := domain.NewExchangeCode(params.State)
	if err := h.service.StoreOAuthResult(c.Request().Context(), st, ptrIdentity(toExternalIdentity(idn)), code); err != nil {
		return err
	}
	q := url.Values{}
	q.Set("intent", string(domain.IntentLogin))
	q.Set("code", code)
	return c.Redirect(http.StatusFound, completePath+"?"+q.Encode())
}

// startBoundFlow 是绑定/重新验证的发起（POST /v1/me/...）：当前会话做 sid、
// auth_time 由 RequireRecentAuth（†，仅绑定）或会话本身（重新验证）保证。
func (h *Handler) startBoundFlow(c *echo.Context, provider string, intent domain.OAuthFlowIntent) error {
	entry, ok := h.oauth.Browser(provider)
	if !ok {
		return errUnknownProvider(provider)
	}
	s, err := httpserver.UserSessionFrom(c)
	if err != nil {
		return err
	}
	binding := newBindingValue()
	state, err := h.service.StartOAuthFlow(c.Request().Context(), provider, intent, s.SessionID, "", hashCookieValue(binding))
	if err != nil {
		return err
	}
	setBindingCookie(c, domain.ExchangeCodePrefix(state), binding, domain.OAuthStateTTL)
	return c.JSON(http.StatusOK, oapi.OAuthStartResponse{
		AuthorizeUrl: entry.Provider.AuthorizeURL(oauth.AuthorizeParams{
			State:       state,
			RedirectURI: h.redirectURI(provider),
		}),
	})
}

// resultCodeOf 把绑定/重新验证的失败映射成回调跳转 result 的取值。
func resultCodeOf(err error) string {
	switch {
	case errors.Is(err, domain.ErrAlreadyBound):
		return "identity_bound"
	case errors.Is(err, domain.ErrAccountUnavailable):
		return "account_unavailable"
	case errors.Is(err, domain.ErrReauthWrongAccount):
		return "reauth_wrong_account"
	default:
		return "error"
	}
}

func ptrIdentity(v domain.ExternalIdentity) *domain.ExternalIdentity { return &v }
