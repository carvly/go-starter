package httpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"app.local/internal/kit/logctx"
)

// 每类主体一个类型、一个键：读错了键的 handler 拿到的是零值和 401，
// 而不是把另一类主体的 ID 当成自己的。
const userSessionKey = "httpserver.user_session"

// UserSession 是已鉴权的前台用户。字段只放鉴权中间件确实拿得到的信息：会话
// 中间件填全部字段，网关信任 header 模式只有 UserID（RequireRecentAuth 因此
// 直接拒绝）。
type UserSession struct {
	UserID    string
	SessionID string
	AuthTime  time.Time
	AMR       []string
}

// SetUserSession 由鉴权中间件在身份校验通过后调用。JWT 和网关信任 header 两种实现
// 都写这同一个键，handler 不需要知道当前挂的是哪一种。
func SetUserSession(c *echo.Context, s UserSession) {
	c.Set(userSessionKey, s)
	setRequestContext(c, logctx.WithUserID(c.Request().Context(), s.UserID))
}

// UserSessionFrom 取出已鉴权的用户，再把需要的字段作为显式参数传给 Service，
// 不把 Echo/JWT 类型带进 domain。
func UserSessionFrom(c *echo.Context) (UserSession, error) {
	s, ok := c.Get(userSessionKey).(UserSession)
	if !ok || s.UserID == "" {
		return UserSession{}, echo.NewHTTPError(http.StatusUnauthorized, "missing authentication")
	}
	return s, nil
}

const staffSessionKey = "httpserver.staff_session"

// StaffSession 是已登录的后台员工，由后台的会话中间件写入。和 UserSession 是两套独立的
// 键和类型：员工 ID 和用户 ID 都是 UUID，共用一个键时类型系统发现不了混用。
type StaffSession struct {
	StaffID   string
	SessionID string
	// SuperAdmin 为 true 时权限检查直接放行。
	SuperAdmin bool
	// MustChangePassword 为 true 时只放行改密码、登出等少数操作。
	MustChangePassword bool
}

func SetStaffSession(c *echo.Context, s StaffSession) {
	c.Set(staffSessionKey, s)
	setRequestContext(c, logctx.WithStaffID(c.Request().Context(), s.StaffID))
}

// setRequestContext 换掉请求的 ctx：两种主体的鉴权都只经上面两个函数把身份写进日志关联字段。
func setRequestContext(c *echo.Context, ctx context.Context) {
	c.SetRequest(c.Request().WithContext(ctx))
}

// StaffSessionFrom 取出已登录的员工；缺失时返回 401。
func StaffSessionFrom(c *echo.Context) (StaffSession, error) {
	s, ok := c.Get(staffSessionKey).(StaffSession)
	if !ok || s.StaffID == "" {
		return StaffSession{}, echo.NewHTTPError(http.StatusUnauthorized, "missing authentication")
	}
	return s, nil
}
