// Package adminapi 是 staff 的后台 HTTP 适配器：handler，以及后台 server 的会话、权限、
// 操作日志中间件。
package adminapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"

	"app.local/internal/kit/logctx"
	staff "app.local/internal/modules/staff/domain"
	"app.local/internal/platform/httpserver"
)

// SessionCookie 是后台会话 cookie。__Host- 前缀要求 Secure、Path=/、不带 Domain，
// 浏览器据此保证它只属于这个主机、只走 HTTPS（localhost 例外）。
const SessionCookie = "__Host-admin_session"

const prefix = httpserver.AdminAPIPrefix

// mustChangeAllowed 是"必须先改密码"时仍然放行的操作。
var mustChangeAllowed = map[string]bool{
	"PUT " + prefix + "/me/password":  true,
	"POST " + prefix + "/auth/logout": true,
	"GET " + prefix + "/me":           true,
}

// NewGuard 返回后台 /admin/v1 分组上的鉴权中间件，按顺序做：
//
//  1. 查"请求方法 + 路由模板 → x-permission"映射：路径不存在返回 404，查不到映射拒绝；
//  2. public 操作直接放行（只有登录）；
//  3. 会话校验（cookie），失败 401；
//  4. 必须改密时只放行 mustChangeAllowed；
//  5. 权限检查：authenticated 放行，superadmin 只放行超级管理员，权限码查库（超级管理员短路）；
//  6. 为写操作记一条操作日志：会话校验通过之后的结果都记，包括改密拦截、权限不足这类被拒绝
//     的尝试；未登录的请求不知道是谁，不记（由限流兜底）。
//
// Echo 先匹配路由再执行分组中间件，所以这里的 c.Path() 是路由模板。
func NewGuard(svc *staff.Service, ops []staff.Operation) echo.MiddlewareFunc {
	perms := make(map[string]string, len(ops))
	routes := map[string]bool{}
	for _, op := range ops {
		perms[op.Method+" "+op.Route] = op.Permission
		routes[op.Route] = true
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			route := c.Path()
			// 带中间件的分组会注册兜底路由（RouteNotFound）：路径不存在，或者路径存在但方法不对，
			// 都匹配到它（后者被它盖住了 Echo 默认的 405）。
			if route == prefix || route == prefix+"/*" {
				for r := range routes {
					if matchRoute(r, c.Request().URL.Path) {
						return echo.ErrMethodNotAllowed
					}
				}
				return echo.ErrNotFound
			}
			key := c.Request().Method + " " + route
			perm, ok := perms[key]
			if !ok {
				return staff.ErrPermissionDenied
			}
			if perm == staff.PermissionPublic {
				return next(c)
			}

			cookie, err := c.Cookie(SessionCookie)
			if err != nil {
				return staff.ErrSessionInvalid
			}
			p, err := svc.Authenticate(c.Request().Context(), cookie.Value)
			if err != nil {
				return err
			}
			httpserver.SetStaffSession(c, httpserver.StaffSession{
				StaffID: p.StaffID, SessionID: p.SessionID, SuperAdmin: p.SuperAdmin, MustChangePassword: p.MustChangePassword,
			})

			err = func() error {
				if p.MustChangePassword && !mustChangeAllowed[key] {
					return staff.ErrPasswordChangeRequired
				}
				// SetStaffSession 换了请求的 ctx（加上 staff_id），要重新取。
				if err := svc.Authorize(c.Request().Context(), p, perm); err != nil {
					return err
				}
				return next(c)
			}()
			if m := c.Request().Method; m != http.MethodGet && m != http.MethodHead {
				audit(c, svc, p.StaffID, key, err)
			}
			return err
		}
	}
}

// audit 记一条写操作日志：员工、请求方法 + 路由模板、路径参数、状态码、IP、request_id。
// 不记录请求体（可能含密码）。写失败只记应用日志，不影响已完成的操作。
func audit(c *echo.Context, svc *staff.Service, staffID, action string, handlerErr error) {
	status := http.StatusOK
	if handlerErr != nil {
		status = httpserver.StatusOf(handlerErr)
	} else if r, err := echo.UnwrapResponse(c.Response()); err == nil && r.Status != 0 {
		status = r.Status
	}
	var params []string
	for _, v := range c.PathValues() {
		params = append(params, v.Value)
	}
	target := strings.Join(params, ",")
	ctx := c.Request().Context()
	err := svc.RecordAudit(ctx, &staff.AuditEntry{
		StaffID:   staffID,
		Action:    action,
		Target:    target,
		Result:    strconv.Itoa(status),
		IP:        c.RealIP(),
		RequestID: logctx.RequestID(ctx),
	})
	if err != nil {
		c.Logger().ErrorContext(ctx, "staff: record audit failed", "action", action, "error", err)
	}
}

// matchRoute 判断 path 是否匹配 Echo 路由模板（:name 段匹配任意非空段）。
func matchRoute(template, path string) bool {
	ts, ps := strings.Split(template, "/"), strings.Split(path, "/")
	if len(ts) != len(ps) {
		return false
	}
	for i, t := range ts {
		if strings.HasPrefix(t, ":") {
			if ps[i] == "" {
				return false
			}
		} else if t != ps[i] {
			return false
		}
	}
	return true
}
