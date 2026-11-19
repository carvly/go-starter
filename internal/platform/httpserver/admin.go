package httpserver

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"slices"
	"strings"

	echoprometheus "github.com/labstack/echo-prometheus"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"app.local/api"
	"app.local/internal/config"
)

// AdminAPIPrefix 是全部后台路由的前缀，与 api/admin.yaml 的 servers.url 一致。
const AdminAPIPrefix = "/admin/v1"

// adminCSP 用于后台前端：脚本只来自同源构建产物；UI 库运行时注入样式，
// style-src 需要 'unsafe-inline'；frame-ancestors 'none' 防点击劫持。
const adminCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
	"font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

type AdminDeps struct {
	Logger *slog.Logger
	HTTP   config.HTTPConfig
	// Origin 是后台前端的 origin（如 https://admin.example.com）。会话用 cookie，
	// 写请求的 Origin 不等于它时拒绝，防跨站请求伪造。
	Origin string

	// RateLimitStore 为 nil 时用进程内存，额度与前台的全局限流相同但分开计数。
	RateLimitStore middleware.RateLimiterStore

	// Routes 由各模块的 RegisterAdminHTTP 组成，挂在 AdminAPIPrefix 下。
	Routes []func(g *echo.Group)
	// Auth 是后台的会话与权限检查，由提供员工账号的模块实现、组合根注入，排在 Origin 校验
	// 之后、按顺序执行。有后台路由时不能为空：单独一个字段而不是混在 Middlewares 里，才能
	// 确认"鉴权确实接上了"，而不只是"挂了某个中间件"。
	Auth []echo.MiddlewareFunc
	// Middlewares 排在 Auth 之后（如操作日志）。Echo 先匹配路由再执行分组中间件，
	// 中间件里 c.Path() 是路由模板。
	Middlewares []echo.MiddlewareFunc

	// Static 是后台前端的构建产物（index.html + assets/）；为 nil 时不托管前端。
	Static fs.FS
}

// NewAdmin 构造后台 server：与前台共用中间件链，但不开 CORS（同源部署），
// 文档页只暴露 admin.yaml，指标用单独的 subsystem。它监听独立地址，
// 公网入口上根本没有 /admin/v1。
func NewAdmin(deps AdminDeps) (*echo.Echo, error) {
	// 失败即关闭：有后台路由却没有鉴权，说明提供员工账号的模块没接上，这时启动会把后台
	// 接口裸露出来，宁可启动失败。
	if len(deps.Routes) > 0 && len(deps.Auth) == 0 {
		return nil, errors.New("configure admin server: admin routes registered without Auth (session/permission) middleware")
	}
	e, registry, err := newServer(common{
		Logger:           deps.Logger,
		HTTP:             deps.HTTP,
		RateLimitStore:   deps.RateLimitStore,
		CSP:              adminCSP,
		MetricsSubsystem: "echo_starter_kit_admin",
		// 前端的静态文件来自内存里的构建产物，冷启动打开页面就要并发加载几十个，计入按 IP 的
		// 全局限流会让页面加载一半；限流只管接口。
		SkipRateLimit: func(c *echo.Context) bool { return deps.Static != nil && c.Path() == spaRoute },
	})
	if err != nil {
		return nil, err
	}

	mws := slices.Concat([]echo.MiddlewareFunc{sameOrigin(deps.Origin)}, deps.Auth, deps.Middlewares)
	g := e.Group(AdminAPIPrefix, mws...)
	for _, register := range deps.Routes {
		register(g)
	}
	RegisterHealthz(e)
	if err := registerAPIDocs(e, deps.HTTP.APIDocsEnabled, "/admin.yaml", api.AdminSpec); err != nil {
		return nil, fmt.Errorf("configure admin server: API docs: %w", err)
	}
	e.GET("/metrics", echoprometheus.NewHandlerWithConfig(echoprometheus.HandlerConfig{Gatherer: registry}))
	if deps.Static != nil {
		registerSPA(e, deps.Static)
	}
	return e, nil
}

// sameOrigin 拒绝跨站的写请求。浏览器会给跨站请求带上 Origin 和
// Sec-Fetch-Site，非浏览器客户端（curl、脚本）不带，也不会携带受害者的 cookie。
func sameOrigin(origin string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			switch req.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				return next(c)
			}
			if site := req.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				return echo.NewHTTPError(http.StatusForbidden, "cross-site request rejected")
			}
			if o := req.Header.Get(echo.HeaderOrigin); o != "" && o != origin {
				return echo.NewHTTPError(http.StatusForbidden, "cross-origin request rejected")
			}
			return next(c)
		}
	}
}

// spaRoute 是前端静态文件和前端路由回退共用的路由模板。
const spaRoute = "/*"

// registerSPA 托管单页应用：存在的文件原样返回，其余路径（前端路由）回退到 index.html。assets/ 下是带内容哈希的文件，长期缓存；其余（包括 index.html）每次校验，
// 发版后浏览器能拿到新的入口。
func registerSPA(e *echo.Echo, fsys fs.FS) {
	h := func(c *echo.Context) error {
		p := strings.TrimPrefix(path.Clean("/"+c.Param("*")), "/")
		if p == "admin/v1" || strings.HasPrefix(p, "admin/v1/") {
			return echo.ErrNotFound
		}
		if p == "" {
			p = "index.html"
		}
		if st, err := fs.Stat(fsys, p); err != nil || st.IsDir() {
			// assets/ 下缺失的文件（如旧版本的 js）返回 404，不能拿 index.html 冒充；
			// 其余路径是前端路由，可能带点（/staff/john.doe），回退到 index.html。
			if strings.HasPrefix(p, "assets/") {
				return echo.ErrNotFound
			}
			p = "index.html"
		}
		if strings.HasPrefix(p, "assets/") {
			c.Response().Header().Set(echo.HeaderCacheControl, "public, max-age=31536000, immutable")
		} else {
			c.Response().Header().Set(echo.HeaderCacheControl, "no-cache")
		}
		return c.FileFS(p, fsys)
	}
	e.GET(spaRoute, h)
	e.HEAD(spaRoute, h)
}
