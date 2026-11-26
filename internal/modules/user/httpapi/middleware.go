package httpapi

import (
	"context"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v5"

	"app.local/internal/kit/authn"
	"app.local/internal/modules/user/domain"
	"app.local/internal/platform/crypto"
	"app.local/internal/platform/httpserver"
)

// SessionAuthenticator 是中间件对 Service 的唯一要求，domain.Service 实现它。
type SessionAuthenticator interface {
	Authenticate(ctx context.Context, sid string) (*domain.Session, error)
}

// SessionAuthConfig 是按 access token 校验会话需要的全部配置：会话中间件和网关的校验端点共用。
type SessionAuthConfig struct {
	Sessions       SessionAuthenticator
	KeyID          string
	Key            []byte
	AdditionalKeys map[string]string
	// ReauthWindow 是重新验证的有效窗口：UserSession.ReauthUntil = AuthTime + ReauthWindow。
	ReauthWindow time.Duration
}

// sessionAuth 是"验签 → 校验 iss/aud/exp → 按 sid 查会话"这一段：会话中间件和校验端点调用同一个实现，两种鉴权模式
// 对同一个令牌的判断不会不一致。
type sessionAuth struct {
	sessions     SessionAuthenticator
	keyFunc      jwt.Keyfunc
	reauthWindow time.Duration
}

func newSessionAuth(cfg SessionAuthConfig) sessionAuth {
	return sessionAuth{
		sessions:     cfg.Sessions,
		keyFunc:      crypto.KeyFunc(cfg.KeyID, cfg.Key, cfg.AdditionalKeys),
		reauthWindow: cfg.ReauthWindow,
	}
}

// authenticate 校验 Authorization 头。令牌问题返回 authn.ErrInvalidToken，会话被顶掉返回 authn.ErrSessionReplaced，
// 查询失败原样返回（500）。
func (a sessionAuth) authenticate(ctx context.Context, authorization string) (httpserver.UserSession, error) {
	tokenString, ok := strings.CutPrefix(authorization, "Bearer ")
	if !ok || strings.TrimSpace(tokenString) == "" {
		return httpserver.UserSession{}, authn.ErrInvalidToken
	}
	claims := &crypto.AccessClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, a.keyFunc,
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(crypto.Issuer),
		jwt.WithAudience(crypto.Audience),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !token.Valid || claims.SID == "" {
		return httpserver.UserSession{}, authn.ErrInvalidToken
	}
	sess, err := a.sessions.Authenticate(ctx, claims.SID)
	if err != nil {
		return httpserver.UserSession{}, err
	}
	s := httpserver.UserSession{UserID: sess.UserID, SessionID: sess.ID, AuthTime: sess.AuthTime, AMR: sess.AMR}
	if !sess.AuthTime.IsZero() {
		s.ReauthUntil = sess.AuthTime.Add(a.reauthWindow)
	}
	return s, nil
}

// NewSessionMiddleware 是默认的前台鉴权（验签后按 sid 查会话）：会话已吊销、已过期、空闲超时或账号非 active 立即失效
// （每次请求查会话，不加缓存），返回 401 invalid_token，被新登录顶掉的返回 401 session_replaced；查询失败也拒绝，但返回
// 500：客户端只在 401 时清掉令牌，服务端故障不会把用户登出。网关鉴权模式下换成 httpserver.NewGatewayIdentityMiddleware。
func NewSessionMiddleware(cfg SessionAuthConfig) echo.MiddlewareFunc {
	auth := newSessionAuth(cfg)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			s, err := auth.authenticate(c.Request().Context(), c.Request().Header.Get(echo.HeaderAuthorization))
			if err != nil {
				return err
			}
			httpserver.SetUserSession(c, s)
			return next(c)
		}
	}
}
