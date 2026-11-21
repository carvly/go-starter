// Package httpserver 构造对外 HTTP 服务：通用中间件链、错误渲染、健康检查、
// API 文档和指标。它不认识任何业务模块，业务路由由组合根经 Deps.Routes 挂载。
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"

	"app.local/api"
	"app.local/internal/config"
	"app.local/internal/kit/lifecycle"
	"app.local/internal/kit/logctx"
	"app.local/internal/platform/logger"
	"app.local/internal/platform/metrics"
	"app.local/internal/platform/ratelimit"
)

// APIPrefix 是全部业务路由的前缀，与 api/openapi.yaml 的 servers.url 一致。
const APIPrefix = "/v1"

type Deps struct {
	Logger  *slog.Logger
	HTTP    config.HTTPConfig
	CORS    config.CORSConfig
	Pingers []Pinger

	// RateLimitStore 是全局限流的计数存储；为 nil 时用进程内存（多实例下额度会成倍放大）。
	RateLimitStore middleware.RateLimiterStore

	// Routes 由各模块的 RegisterHTTP 组成，挂在 APIPrefix 下。
	Routes []func(g *echo.Group)

	// Metrics 是前台端口的 Registry，HTTP 指标注册进去，由本端口的 /metrics 输出；为 nil 时用一个空的。
	Metrics *prometheus.Registry
}

func healthEndpointSkipper(c *echo.Context) bool {
	switch c.Path() {
	case "/healthz", "/readyz", "/metrics":
		return true
	default:
		return false
	}
}

// New 按固定顺序注册中间件：指标 → Recover → RequestID → BodyLimit → CORS → Secure →
// 全局限流 → Timeout；鉴权是操作级中间件，由各模块挂载。
func New(deps Deps) (*echo.Echo, error) {
	// CORS 必须排在鉴权/限流前面，否则预检请求会被拦掉。
	corsMw, err := (middleware.CORSConfig{
		Skipper:      healthEndpointSkipper,
		AllowOrigins: deps.CORS.AllowOrigins,
		AllowHeaders: []string{echo.HeaderAuthorization, echo.HeaderContentType},
		// refresh token 按 Origin 交付名单经 cookie 交付（SameSite=Strict），
		// 跨域请求必须允许携带凭据；AllowOrigins 保持精确匹配，不用 "*"。
		AllowCredentials: true,
	}).ToMiddleware()
	if err != nil {
		return nil, fmt.Errorf("configure server: CORS: %w", err)
	}

	e, registry, err := newServer(common{
		Logger:         deps.Logger,
		HTTP:           deps.HTTP,
		RateLimitStore: deps.RateLimitStore,
		CORS:           corsMw,
		Name:           "api",
		Metrics:        deps.Metrics,
	})
	if err != nil {
		return nil, err
	}

	v1 := e.Group(APIPrefix)
	for _, register := range deps.Routes {
		register(v1)
	}
	RegisterHealthz(e)
	RegisterReadyz(e, deps.Pingers)
	if err := registerAPIDocs(e, deps.HTTP.APIDocsEnabled, "/openapi.yaml", api.OpenAPISpec); err != nil {
		return nil, fmt.Errorf("configure server: API docs: %w", err)
	}
	e.GET("/metrics", echo.WrapHandler(metrics.Handler(registry)))

	return e, nil
}

// common 是前台和后台 server 共用的中间件链的配置。
type common struct {
	Logger         *slog.Logger
	HTTP           config.HTTPConfig
	RateLimitStore middleware.RateLimiterStore
	// CORS 为 nil 时不挂（后台同源部署，不开 CORS）。
	CORS echo.MiddlewareFunc
	// CSP 为空时不发 Content-Security-Policy。
	CSP string
	// Name 是指标的 server 标签：api、admin。
	Name    string
	Metrics *prometheus.Registry
	// SkipRateLimit 额外跳过全局限流的请求（健康检查和指标总是跳过）。
	SkipRateLimit func(c *echo.Context) bool
}

// newServer 构造 Echo 并注册共用的中间件链：指标 → Recover → RequestID → BodyLimit →
// [CORS] → Secure → 全局限流 → Timeout。返回的 Registry 供 /metrics 使用。
func newServer(d common) (*echo.Echo, *prometheus.Registry, error) {
	// 调用方传入的 logger 不一定来自 logger.New，包一层才能保证请求日志带上关联字段。
	log := logger.Ensure(d.Logger)
	e := echo.New()
	e.Logger = log
	e.Validator = newRequestValidator()
	e.HTTPErrorHandler = newHTTPErrorHandler()

	// IPExtractor 必须匹配真实部署拓扑，否则按 IP 的限流可以被伪造 header 绕过。
	if d.HTTP.TrustedProxy {
		e.IPExtractor = echo.ExtractIPFromXFFHeader(echo.TrustLoopback(true), echo.TrustPrivateNet(true))
	} else {
		e.IPExtractor = echo.ExtractIPDirect()
	}

	registry := orEmpty(d.Metrics)
	metricsMw, err := newMetricsMiddleware(registry, d.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("configure server: metrics: %w", err)
	}
	e.Use(metricsMw)

	// 只取 panic 所在 goroutine 的堆栈：默认取全部 goroutine 并截断到 4KB，出问题的那段常被挤掉。
	e.Use(middleware.RecoverWithConfig(middleware.RecoverConfig{DisableStackAll: true, StackSize: 16 << 10}))

	// 请求的关联字段在这里写进 ctx：Echo 先匹配路由再执行 Use 链，c.Path() 已是路由模板。
	e.Use(middleware.RequestIDWithConfig(middleware.RequestIDConfig{
		RequestIDHandler: func(c *echo.Context, requestID string) {
			ctx := logctx.WithRoute(logctx.WithRequest(c.Request().Context(), log, requestID), methodName(c.Request().Method), c.Path())
			c.SetRequest(c.Request().WithContext(ctx))
			c.SetLogger(logctx.From(ctx))
		},
	}))

	e.Use(middleware.BodyLimit(d.HTTP.BodyLimitBytes))

	if d.CORS != nil {
		e.Use(d.CORS)
	}

	secureMw, err := (middleware.SecureConfig{
		XSSProtection:         "1; mode=block",
		ContentTypeNosniff:    "nosniff",
		XFrameOptions:         "SAMEORIGIN",
		HSTSMaxAge:            int(d.HTTP.HSTSMaxAge.Seconds()),
		ContentSecurityPolicy: d.CSP,
	}).ToMiddleware()
	if err != nil {
		return nil, nil, fmt.Errorf("configure server: Secure: %w", err)
	}
	e.Use(secureMw)

	if d.HTTP.GlobalRateLimitPerSecond <= 0 || d.HTTP.RequestTimeout <= 0 {
		return nil, nil, errors.New("configure server: GlobalRateLimitPerSecond and RequestTimeout must be > 0 (validated by config)")
	}
	store := d.RateLimitStore
	if store == nil {
		store = ratelimit.NewStore(nil, "global", ratelimit.PerSecond(d.HTTP.GlobalRateLimitPerSecond), log)
	}
	rateLimitMw, err := (middleware.RateLimiterConfig{
		Skipper: func(c *echo.Context) bool {
			return healthEndpointSkipper(c) || (d.SkipRateLimit != nil && d.SkipRateLimit(c))
		},
		Store: store,
	}).ToMiddleware()
	if err != nil {
		return nil, nil, fmt.Errorf("configure server: RateLimiter: %w", err)
	}
	e.Use(rateLimitMw)

	// Timeout 只让客户端提前拿到超时响应；释放服务端资源要靠 ctx 一路透传。
	timeoutMw, err := (middleware.ContextTimeoutConfig{
		Skipper: healthEndpointSkipper,
		Timeout: d.HTTP.RequestTimeout,
	}).ToMiddleware()
	if err != nil {
		return nil, nil, fmt.Errorf("configure server: ContextTimeout: %w", err)
	}
	e.Use(timeoutMw)

	return e, registry, nil
}

// NewInternal 构造 worker 用的内部 server：只有 /healthz 和 /metrics，
// 绑在不对外发布的内部端口上。registry 为 nil 时 /metrics 输出空内容。
func NewInternal(log *slog.Logger, registry *prometheus.Registry) *echo.Echo {
	e := echo.New()
	e.Logger = logger.Ensure(log)
	RegisterHealthz(e)
	e.GET("/metrics", echo.WrapHandler(metrics.Handler(orEmpty(registry))))
	return e
}

// orEmpty 让三种 server 对 nil Registry 的处理一致：用一个空的。
func orEmpty(reg *prometheus.Registry) *prometheus.Registry {
	if reg == nil {
		return prometheus.NewRegistry()
	}
	return reg
}

// Component 把一个 *echo.Echo 包成 lifecycle.Component。ctx 取消时 Echo 自己
// 走优雅关闭（最多 15s），所以 Stop 不需要做事。
func Component(name string, e *echo.Echo, addr string) lifecycle.Component {
	return &serverComponent{name: name, e: e, addr: addr}
}

type serverComponent struct {
	name string
	e    *echo.Echo
	addr string
}

func (s *serverComponent) Name() string { return s.name }

func (s *serverComponent) Start(ctx context.Context) error {
	sc := echo.StartConfig{
		Address:         s.addr,
		HideBanner:      true,
		HidePort:        true,
		GracefulTimeout: 15 * time.Second,
		OnShutdownError: func(err error) {
			s.e.Logger.ErrorContext(ctx, "graceful shutdown error", slog.String("server", s.name), slog.Any("error", err))
		},
		BeforeServeFunc: func(srv *http.Server) error {
			// 连接级读写超时防慢速连接，与 handler 级的 ContextTimeout 互补。
			srv.ReadTimeout = 30 * time.Second
			srv.WriteTimeout = 30 * time.Second
			return nil
		},
	}
	s.e.Logger.InfoContext(ctx, "http server listening", slog.String("server", s.name), slog.String("addr", s.addr))
	return sc.Start(ctx, s.e)
}

func (s *serverComponent) Stop(context.Context) error { return nil }
