package httpapi

import (
	"time"

	"github.com/labstack/echo/v5"

	"app.local/internal/modules/auth/domain"
	"app.local/internal/platform/httpserver"
)

// NewRequireRecentAuth 保护 † 操作（绑定/解绑、注销等）：要求会话的 auth_time
// 在 window 之内。网关信任 header 模式拿不到 auth_time，UserSession.AuthTime
// 为零值，这里直接拒绝（fail closed）。成功的重新验证由 /v1/me/reauthenticate
// 更新 auth_time，不重新签发 token。
func NewRequireRecentAuth(window time.Duration) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			s, err := httpserver.UserSessionFrom(c)
			if err != nil {
				return err
			}
			if s.AuthTime.IsZero() || time.Since(s.AuthTime) > window {
				return domain.ErrReauthRequired
			}
			return next(c)
		}
	}
}
