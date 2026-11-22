package httpserver

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"
)

// RegisterHealthz 挂载 liveness 探针：恒定 200，不查任何依赖。
func RegisterHealthz(e *echo.Echo) {
	e.GET("/healthz", func(c *echo.Context) error {
		return c.NoContent(http.StatusOK)
	})
}

// RegisterReadyz 挂载 readiness 探针：所在 server 正在排空（见 Component）或任一依赖 Ping 失败时返回 503。
// 只有 api 进程的前台和后台端口挂，worker 没有"要不要把流量路由过来"这个概念。
func RegisterReadyz(e *echo.Echo, pingers []Pinger) {
	e.GET("/readyz", func(c *echo.Context) error {
		ctx := c.Request().Context()
		if isDraining(ctx) {
			return c.JSON(http.StatusServiceUnavailable, ErrorBody{Code: "draining", Message: "the server is shutting down"})
		}
		for _, p := range pingers {
			if err := p.Ping(ctx); err != nil {
				return c.JSON(http.StatusServiceUnavailable, ErrorBody{
					Code:    "not_ready",
					Message: "a dependency is not ready",
				})
			}
		}
		return c.NoContent(http.StatusOK)
	})
}

type drainingKey struct{}

func withDraining(ctx context.Context) context.Context {
	return context.WithValue(ctx, drainingKey{}, true)
}

func isDraining(ctx context.Context) bool {
	v, _ := ctx.Value(drainingKey{}).(bool)
	return v
}
