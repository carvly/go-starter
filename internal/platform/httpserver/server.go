// Package httpserver 构造对外 HTTP 服务：通用中间件链、错误渲染、健康检查、
// API 文档和指标。它不认识任何业务模块，业务路由由组合根经 Deps.Routes 挂载。
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"

	"app.local/api"
	"app.local/internal/config"
	"app.local/internal/kit/lifecycle"
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
	// Tracer 是进程的 TracerProvider（tracing.New），必填；不导出时也要传，trace_id 由它生成。测试传 tracing.Disabled()。
	Tracer trace.TracerProvider
}

func healthEndpointSkipper(c *echo.Context) bool {
	switch c.Path() {
	case "/healthz", "/readyz", "/metrics":
		return true
	default:
		return false
	}
}

// corsPreflightMaxAge 是浏览器缓存预检结果的秒数。改了 CORS 白名单后，最多要等这么久才在已打开的页面上生效。
const corsPreflightMaxAge = 600

// New 按固定顺序注册中间件：请求入口（span、trace_id、日志字段）→ 指标 → Recover → BodyLimit → CORS →
// Secure → 全局限流 → Timeout；鉴权是操作级中间件，由各模块挂载。
func New(deps Deps) (*echo.Echo, error) {
	// CORS 必须排在鉴权/限流前面，否则预检请求会被拦掉。
	corsMw, err := (middleware.CORSConfig{
		Skipper:      healthEndpointSkipper,
		AllowOrigins: deps.CORS.AllowOrigins,
		AllowHeaders: []string{
			echo.HeaderAuthorization, echo.HeaderContentType,
			HeaderClientPlatform, HeaderClientOS, HeaderClientVersion, HeaderInstallationID,
		},
		// 跨域的前端要读到 X-Trace-Id，出错时才能展示给用户、报给客服。
		ExposeHeaders: []string{HeaderTraceID},
		// 客户端每个请求都带 X-Client-* 自定义头，跨域时都要预检；不设时浏览器只缓存预检结果约 5 秒。
		MaxAge: corsPreflightMaxAge,
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
		Tracer:         deps.Tracer,
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
	Tracer  trace.TracerProvider
	// SkipRateLimit 额外跳过全局限流的请求（健康检查和指标总是跳过）。
	SkipRateLimit func(c *echo.Context) bool
}

// newServer 构造 Echo 并注册共用的中间件链：请求入口 → 指标 → Recover → BodyLimit →
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

	if d.Tracer == nil {
		return nil, nil, errors.New("configure server: Tracer is required")
	}
	// 请求入口在最外层：指标的 exemplar 和之后的一切日志都要用到它建的 span 和绑定的 logger。
	e.Use(newRequestMiddleware(d.Tracer, d.HTTP.TrustedProxy, log))

	registry := orEmpty(d.Metrics)
	metricsMw, err := newMetricsMiddleware(registry, d.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("configure server: metrics: %w", err)
	}
	e.Use(metricsMw)

	// 只取 panic 所在 goroutine 的堆栈：默认取全部 goroutine 并截断到 4KB，出问题的那段常被挤掉。
	e.Use(middleware.RecoverWithConfig(middleware.RecoverConfig{DisableStackAll: true, StackSize: 16 << 10}))

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

// InternalPrefix 是内部接口的路径前缀：只挂在内部端口上，前台端口没有这些路由。
const InternalPrefix = "/internal"

// InternalDeps 是 api 内部端口的依赖。
type InternalDeps struct {
	Logger *slog.Logger
	HTTP   config.HTTPConfig
	// RateLimitStore 是内部端口按 IP 限流的计数存储，与前台的分开计数（一次带令牌的业务请求在两边各计一次，不会扣两次
	// 同一份额度）；为 nil 时用进程内存。
	RateLimitStore middleware.RateLimiterStore
	// Routes 由各模块的 RegisterInternalHTTP 组成，挂在 InternalPrefix 下。
	Routes []func(g *echo.Group)
	// Metrics 是这个端口的 HTTP 指标注册进去的 Registry。内部端口不输出 /metrics：组合根传进程的 Registry，
	// 指标经前台端口输出，用 server="internal" 标签区分。
	Metrics *prometheus.Registry
	Tracer  trace.TracerProvider
}

// NewInternalAPI 构造 api 的内部端口：网关等基础设施调用的接口（如会话校验），不面向客户端、不进 spec。
// 中间件链与前台相同（请求日志、指标、trace、Recover、BodyLimit、按 IP 的限流、Timeout），只是不挂 CORS（浏览器不会
// 访问）。限流不能省：网关在业务请求到达前台限流之前就调会话校验，不限的话拿一个有效令牌刷请求，每个请求都会先查一次
// 库。IP 取自网关转来的 X-Forwarded-For（HTTP_TRUSTED_PROXY），与前台按同一个客户端计数、各用各的额度。
func NewInternalAPI(deps InternalDeps) (*echo.Echo, error) {
	e, _, err := newServer(common{
		Logger:         deps.Logger,
		HTTP:           deps.HTTP,
		RateLimitStore: deps.RateLimitStore,
		Name:           "internal",
		Metrics:        deps.Metrics,
		Tracer:         deps.Tracer,
	})
	if err != nil {
		return nil, err
	}
	g := e.Group(InternalPrefix)
	for _, register := range deps.Routes {
		register(g)
	}
	RegisterHealthz(e)
	return e, nil
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

// Listen 绑定 addr，供自己绑定端口的部署使用（容器、本地开发、worker 的内部端口）。
// socket activation 部署的端口由 systemd 绑定，listener 来自 systemd.Listeners。
func Listen(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("httpserver: listen %s: %w", addr, err)
	}
	return ln, nil
}

const (
	// gracefulTimeout 是关闭 listener 之后等连接做完的上限（含 Shutdown 兜底）。
	gracefulTimeout = 15 * time.Second
	// serveReturnTimeout 是关闭 listener 之后等 Serve 返回的上限：Serve 返回后不会再有新连接被接管。
	serveReturnTimeout = time.Second
	// freshConnTimeout 是等已 accept 的连接发来第一个请求的上限：共享 socket 交接时队列里的连接在毫秒内就会发出请求，
	// 超过它还不发的（本机的 TCP 探测之类）不再等，交给 Shutdown。
	freshConnTimeout = time.Second
)

// Stop 是 Component 收到停止信号后的行为。
type Stop struct {
	// Drain 是排空期：处理已有连接上的请求、每个响应带 Connection: close，空闲连接不主动关闭，留给网关按自己的
	// keepalive_timeout 关掉，这样网关不会在服务端关闭空闲连接的瞬间复用它发出请求（非幂等请求不会被重试，会直接失败）。
	// 同组组件出错（lifecycle.Failed）时不排空。
	Drain time.Duration
	// SharedListener 为 true 表示 ln 与其它进程共享（systemd socket activation）：收到停止信号立即关闭本进程这份 fd、
	// 不再 accept，队列里的新连接由共享这个 socket 的其它实例接走（socket 仍由 systemd 和它们持有，不会被 RST）；
	// 排空期只服务已建立的连接，连接都关闭后提前结束。为 false 时 ln 归本进程独有，一关闭新连接就会被拒绝，所以排空期
	// 内照常 accept，留给网关根据 /readyz 的 503 摘掉实例，排空结束才关闭。
	SharedListener bool
}

// Component 把一个 *echo.Echo 包成 lifecycle.Component，在 ln 上服务。ctx 取消后：
//  1. 每个响应带 Connection: close（连接回完当前请求即关闭，不再复用），/readyz 返回 503；
//  2. 按 stop 排空、关闭 ln（只是本进程这份 fd），等 Serve 返回；
//  3. 等到没有处于 StateNew（已 accept、还没读完第一个请求，最多等 freshConnTimeout）和 StateActive（正在处理）的连接；
//  4. 最后 http.Server.Shutdown 关掉剩下的空闲连接，第 3、4 步合计最多 gracefulTimeout。
//
// 不能一开始就调 Shutdown：它开始之后读完请求的连接会被直接关闭而不响应（net/http 的 conn.serve），已被 accept 的
// 连接拿到 EOF。第 4 步关闭的空闲连接若恰好被客户端复用仍会失败：网关的空闲连接在排空期内已由网关自己关掉
// （keepalive_timeout < 排空期），剩下的只会是能容忍断开的客户端（如指标抓取）。不用
// http.Server.SetKeepAlivesEnabled(false)：它会立刻关闭所有空闲连接，正是排空要避免的竞态。Stop 不需要做事。
func Component(name string, e *echo.Echo, ln net.Listener, stop Stop) lifecycle.Component {
	return &serverComponent{name: name, e: e, ln: ln, stop: stop, conns: map[net.Conn]http.ConnState{}}
}

type serverComponent struct {
	name     string
	e        *echo.Echo
	ln       net.Listener
	stop     Stop
	draining atomic.Bool

	mu    sync.Mutex
	conns map[net.Conn]http.ConnState
}

func (s *serverComponent) Name() string { return s.name }

func (s *serverComponent) Start(ctx context.Context) error {
	log := s.e.Logger.With(slog.String("server", s.name))
	srv := &http.Server{
		Handler: http.HandlerFunc(s.serveHTTP),
		// 连接级读写超时防慢速连接，与 handler 级的 ContextTimeout 互补。
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		ErrorLog:     slog.NewLogLogger(log.Handler(), slog.LevelError),
		ConnState:    s.trackConn,
	}
	var serveErr error
	served := make(chan struct{}) // Serve 返回时关闭
	log.InfoContext(ctx, "http server listening", slog.String("addr", s.ln.Addr().String()))
	go func() {
		serveErr = srv.Serve(s.ln)
		close(served)
	}()

	select {
	case <-served:
		return fmt.Errorf("serve: %w", serveErr)
	case <-ctx.Done():
	}
	s.draining.Store(true)
	closeListener := func() {
		_ = s.ln.Close() // Serve 随之返回 accept 错误，是预期的
		select {
		case <-served:
		case <-time.After(serveReturnTimeout):
			log.WarnContext(ctx, "http server: Serve did not return after closing the listener")
		}
	}
	if s.stop.SharedListener {
		closeListener()
	}
	if s.stop.Drain > 0 && !lifecycle.Failed(ctx) {
		log.InfoContext(ctx, "http server draining", slog.Duration("drain", s.stop.Drain), slog.Bool("shared_listener", s.stop.SharedListener))
		s.waitDrain()
	}
	if !s.stop.SharedListener {
		closeListener()
	}
	deadline := time.Now().Add(gracefulTimeout)
	s.waitBusyConns(deadline)
	shutdownCtx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	// listener 已由 closeListener 关闭；Serve 还没返回时 Shutdown 会再关一次，返回的 net.ErrClosed 不是关停失败。
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, net.ErrClosed) {
		log.ErrorContext(ctx, "graceful shutdown error", slog.Any("error", err))
		_ = srv.Close()
	}
	return nil
}

// waitDrain 等满排空期；共享 listener 时已不再 accept，连接都关闭后就不必再等。
func (s *serverComponent) waitDrain() {
	end := time.Now().Add(s.stop.Drain)
	for time.Now().Before(end) {
		if s.stop.SharedListener && s.connCount() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *serverComponent) trackConn(c net.Conn, st http.ConnState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st == http.StateClosed || st == http.StateHijacked {
		delete(s.conns, c)
		return
	}
	s.conns[c] = st
}

// waitBusyConns 等到没有 StateActive 的连接、且没有 StateNew 的连接（最多等 freshConnTimeout），或到 deadline。
func (s *serverComponent) waitBusyConns(deadline time.Time) {
	freshDeadline := time.Now().Add(freshConnTimeout)
	for time.Now().Before(deadline) {
		fresh, active := s.busyCounts()
		if active == 0 && (fresh == 0 || !time.Now().Before(freshDeadline)) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *serverComponent) busyCounts() (fresh, active int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.conns {
		switch st {
		case http.StateNew:
			fresh++
		case http.StateActive:
			active++
		}
	}
	return fresh, active
}

func (s *serverComponent) connCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// serveHTTP 在收到停止信号后给每个响应加 Connection: close，并在请求 ctx 里标记排空，由 RegisterReadyz 挂的 /readyz
// 返回 503，网关或编排器据此摘掉实例。排空状态只属于这个 server 组件，不需要另外接线。
func (s *serverComponent) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if s.draining.Load() {
		w.Header().Set("Connection", "close")
		r = r.WithContext(withDraining(r.Context()))
	}
	s.e.ServeHTTP(w, r)
}

func (s *serverComponent) Stop(context.Context) error { return nil }
