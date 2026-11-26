package httpapi

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus"

	"app.local/internal/kit/authn"
	"app.local/internal/platform/crypto"
	"app.local/internal/platform/httpserver"
)

// VerifyPath 是网关的会话校验端点，挂在 api 的内部端口上（httpserver.InternalPrefix 之下）。它是网关和 user 服务之间
// 的基础设施接口，不是客户端契约，不进 spec；契约见 docs/agents/auth.md 的"网关鉴权"。
const VerifyPath = "/auth/verify"

// 校验结果的取值（user_gateway_auth_results_total 的 result 标签）。两种失效直接用 authn 的错误码：它们也是
// X-Auth-Status 的值，网关身份中间件按同一个 Code 判断，只有一份定义。
var (
	verifyOK       = "ok"
	verifyError    = "error"
	verifyReplaced = authn.ErrSessionReplaced.Code
	verifyInvalid  = authn.ErrInvalidToken.Code
)

// NewVerifyHandler 返回会话校验端点：网关拿客户端的 Authorization 来换身份断言。
//
//   - 会话有效：200，X-Auth-Assertion 是签发的断言；
//   - 令牌或会话失效：200，X-Auth-Status 是 invalid_token / session_replaced（是网关与 user 服务之间的约定，
//     401 留给端点本身出错，两者不混淆）；
//   - 查会话失败、没有配置签名私钥：500，网关返回 503。
//
// signer 为 nil 时（没有配置 USER_ASSERTION_SIGNING_KEY）端点照样注册、一律 500：注册与否不随配置变化，网关侧的
// 结果和没有注册相同（503），错误日志写明缺哪个配置。
func NewVerifyHandler(cfg SessionAuthConfig, signer *crypto.AssertionSigner, reg prometheus.Registerer) (echo.HandlerFunc, error) {
	results := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "user_gateway_auth_results_total",
		Help: "Results of the gateway session verify endpoint.",
	}, []string{"result"})
	if err := reg.Register(results); err != nil {
		return nil, err
	}
	for _, r := range []string{verifyOK, verifyError, verifyReplaced, verifyInvalid} {
		results.WithLabelValues(r)
	}
	auth := newSessionAuth(cfg)
	return func(c *echo.Context) error {
		if signer == nil {
			results.WithLabelValues(verifyError).Inc()
			return errors.New("user: gateway verify: USER_ASSERTION_SIGNING_KEY is not configured")
		}
		s, err := auth.authenticate(c.Request().Context(), c.Request().Header.Get(echo.HeaderAuthorization))
		switch {
		case errors.Is(err, authn.ErrSessionReplaced):
			results.WithLabelValues(verifyReplaced).Inc()
			return verifyStatus(c, verifyReplaced)
		case errors.Is(err, authn.ErrInvalidToken):
			results.WithLabelValues(verifyInvalid).Inc()
			return verifyStatus(c, verifyInvalid)
		case err != nil:
			results.WithLabelValues(verifyError).Inc()
			return err
		}
		token, err := signer.Sign(crypto.Assertion{
			UserID: s.UserID, SessionID: s.SessionID, AuthTime: s.AuthTime, AMR: s.AMR, ReauthUntil: s.ReauthUntil,
		})
		if err != nil {
			results.WithLabelValues(verifyError).Inc()
			return err
		}
		results.WithLabelValues(verifyOK).Inc()
		c.Response().Header().Set(httpserver.HeaderAuthAssertion, token)
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return c.NoContent(http.StatusOK)
	}, nil
}

func verifyStatus(c *echo.Context, code string) error {
	c.Response().Header().Set(httpserver.HeaderAuthStatus, code)
	c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	return c.NoContent(http.StatusOK)
}

// RegisterInternal 挂载 user 的内部接口（api 内部端口的 /internal 之下）。
func RegisterInternal(g *echo.Group, verify echo.HandlerFunc) {
	g.GET(VerifyPath, verify)
}
