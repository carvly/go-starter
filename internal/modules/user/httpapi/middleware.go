package httpapi

import (
	"context"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v5"

	"app.local/internal/modules/user/domain"
	"app.local/internal/platform/crypto"
	"app.local/internal/platform/httpserver"
)

// SessionAuthenticator 是中间件对 Service 的唯一要求，domain.Service 实现它。
type SessionAuthenticator interface {
	Authenticate(ctx context.Context, sid string) (*domain.Session, error)
}

// NewSessionMiddleware 是默认的前台鉴权（验签后按 sid 查会话）：
// 验签（HS256 + kid 轮换）→ 校验 iss/aud/exp → 按 sid 查会话。会话已吊销、已过期、
// 空闲超时或账号非 active 立即失效（每次请求查会话，不加缓存），返回 401 invalid_token，被新登录顶掉的返回
// 401 session_replaced；查询失败也拒绝，但返回 500：客户端只在 401 时清掉令牌，服务端故障不会把用户登出。
// 部署在已做过身份校验的网关后面时，换成 NewTrustedIdentityMiddleware。
func NewSessionMiddleware(sessions SessionAuthenticator, currentKeyID string, currentKey []byte, additionalKeys map[string]string) echo.MiddlewareFunc {
	keyFunc := crypto.KeyFunc(currentKeyID, currentKey, additionalKeys)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			h := c.Request().Header.Get(echo.HeaderAuthorization)
			tokenString, ok := strings.CutPrefix(h, "Bearer ")
			if !ok || strings.TrimSpace(tokenString) == "" {
				return domain.ErrInvalidToken
			}
			claims := &crypto.AccessClaims{}
			token, err := jwt.ParseWithClaims(tokenString, claims, keyFunc,
				jwt.WithValidMethods([]string{"HS256"}),
				jwt.WithIssuer(crypto.Issuer),
				jwt.WithAudience(crypto.Audience),
				jwt.WithExpirationRequired(),
			)
			if err != nil || !token.Valid || claims.SID == "" {
				return domain.ErrInvalidToken
			}
			sess, err := sessions.Authenticate(c.Request().Context(), claims.SID)
			if err != nil {
				return err
			}
			httpserver.SetUserSession(c, httpserver.UserSession{
				UserID:    sess.UserID,
				SessionID: sess.ID,
				AuthTime:  sess.AuthTime,
				AMR:       sess.AMR,
			})
			return next(c)
		}
	}
}
