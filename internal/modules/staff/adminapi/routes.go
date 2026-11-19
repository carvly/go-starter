package adminapi

import (
	"github.com/labstack/echo/v5"

	"app.local/internal/modules/staff/adminapi/oapi"
)

// Register 挂载 api/admin.yaml 中 tag=staff 的全部操作。鉴权由后台 server 分组上的
// Guard 统一做，这里没有操作级中间件。
func Register(g *echo.Group, h *Handler) {
	oapi.RegisterHandlers(g, h)
}
