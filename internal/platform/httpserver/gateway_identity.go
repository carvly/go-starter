package httpserver

import (
	"errors"
	"time"

	"github.com/labstack/echo/v5"

	"app.local/internal/kit/authn"
	"app.local/internal/platform/crypto"
)

// 网关鉴权的两个请求头：网关调 user 服务的校验端点后，把结果放进其中一个转给后端（docs/agents/auth.md 的
// "网关鉴权"）。网关先清掉客户端带来的同名头；客户端直连时伪造的断言验不过签名。
const (
	// HeaderAuthAssertion 是 user 服务签发的身份断言（ES256 JWT）。
	HeaderAuthAssertion = "X-Auth-Assertion"
	// HeaderAuthStatus 是会话校验没通过的原因（invalid_token、session_replaced），不签名：伪造它只能让自己被拒绝。
	HeaderAuthStatus = "X-Auth-Status"
)

// NewGatewayIdentityMiddleware 是网关鉴权模式下的前台鉴权：验证网关转来的身份断言。拆出去、没有 user 模块代码的
// 服务也用它，所以放在 platform，只认识断言格式，不认识任何模块。
//
//   - 断言有效：写入 UserSession（字段齐全，† 操作可用）；
//   - 没有断言：按 X-Auth-Status 返回 401 session_replaced，其余 401 invalid_token；
//   - 带了断言但验不过（kid 不认识、签名不对、过期、格式不符）：503 auth_unavailable。网关先清掉客户端带来的断言，
//     经网关到达的断言一定是 user 服务签的，验不过只能是服务端的问题（密钥轮换漏配、公钥贴错、时钟偏差）。返回 401
//     会让客户端刷新令牌、再失败就清令牌，刷新换不掉服务端的问题，用户会被登出；绕过网关直连的伪造者拿到 401 还是
//     503 都一样被拒绝。
func NewGatewayIdentityMiddleware(v *crypto.AssertionVerifier) echo.MiddlewareFunc {
	if v == nil {
		panic("httpserver: NewGatewayIdentityMiddleware: verifier is nil")
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			h := c.Request().Header
			token := h.Get(HeaderAuthAssertion)
			if token == "" {
				if h.Get(HeaderAuthStatus) == authn.ErrSessionReplaced.Code {
					return authn.ErrSessionReplaced
				}
				return authn.ErrInvalidToken
			}
			a, err := v.Verify(token)
			if err != nil {
				return errors.Join(authn.ErrAuthUnavailable, err)
			}
			SetUserSession(c, UserSession{
				UserID: a.UserID, SessionID: a.SessionID, AuthTime: a.AuthTime, AMR: a.AMR, ReauthUntil: a.ReauthUntil,
			})
			return next(c)
		}
	}
}

// NewRequireRecentAuth 保护 † 操作（绑定/解绑、注销等）：要求最近验证过身份（UserSession.ReauthUntil 未过）。
// 挂在前台鉴权之后；两种鉴权都填 ReauthUntil，行为相同。
func NewRequireRecentAuth() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			s, err := UserSessionFrom(c)
			if err != nil {
				return err
			}
			if s.ReauthUntil.IsZero() || !time.Now().Before(s.ReauthUntil) {
				return authn.ErrReauthRequired
			}
			return next(c)
		}
	}
}
