package httpapi

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
)

// NewJSONOnly 限定带请求体的写操作只接受 application/json（见 internal/modules/user/docs/user.md 的"登录接口的防护"，
// 挂在哪些操作上见 routes.go）：跨站页面只能发不触发预检的表单请求，限定 JSON 后它们到不了
// handler。response_mode=form_post 的表单回调（oauthCallbackForm）不挂它，防护由一次性 state + 绑定 cookie 承担。
func NewJSONOnly() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			switch req.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
				if req.ContentLength != 0 && !strings.HasPrefix(req.Header.Get(echo.HeaderContentType), "application/json") {
					return echo.NewHTTPError(http.StatusUnsupportedMediaType, "content type must be application/json")
				}
			}
			return next(c)
		}
	}
}
