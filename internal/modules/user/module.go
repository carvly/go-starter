// Package user 是前台用户模块：账号的后台管理、微信登录、会话和
// 前台鉴权中间件。module.go 是它对组合根暴露的唯一入口。
package user

import (
	"log/slog"
	"time"

	"github.com/labstack/echo/v5"
	echoMiddleware "github.com/labstack/echo/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	goredis "github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"

	"app.local/internal/modules/user/adminapi"
	"app.local/internal/modules/user/domain"
	"app.local/internal/modules/user/httpapi"
	"app.local/internal/modules/user/jobs"
	"app.local/internal/modules/user/postgres"
	"app.local/internal/platform/crypto"
	"app.local/internal/platform/dbtx"
	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/oauth"
	"app.local/internal/platform/queue"
	"app.local/internal/platform/ratelimit"
)

// Deps 是 user 模块的全部依赖，由组合根注入。
type Deps struct {
	DB     *dbtx.Handle
	Config Config
	Queue  *queue.Inserter
	// Redis 为 nil 时限流退回进程内存（额度按实例数放大）。
	Redis  *goredis.Client
	Logger *slog.Logger
	// AssertionVerifier 验证网关转来的身份断言（基础设施配置 GATEWAY_ASSERTION_PUBLIC_KEYS），没配时为 nil。
	// USER_AUTH_MODE=gateway 时必须有，由 LoadConfig 保证。
	AssertionVerifier *crypto.AssertionVerifier
	// Metrics 是进程的 Registry，网关校验端点的结果指标注册进去；为 nil 时不输出。
	Metrics prometheus.Registerer
}

type Module struct {
	Service      *domain.Service
	cfg          Config
	oauth        oauth.Registry
	redis        *goredis.Client
	logger       *slog.Logger
	dataCleaners []DataCleaner
	verifier     *crypto.AssertionVerifier
	signer       *crypto.AssertionSigner
	metrics      prometheus.Registerer
}

func New(d Deps) *Module {
	issuer := crypto.NewTokenIssuer(d.Config.JWTSigningKey, d.Config.JWTKeyID)
	svc := domain.NewService(domain.Deps{
		Users:      postgres.NewRepository(d.DB),
		Identities: postgres.NewIdentityRepository(d.DB),
		OpenIDs:    postgres.NewWeChatOpenIDRepository(d.DB),
		Sessions:   postgres.NewSessionRepository(d.DB, d.Config.SessionIdleTimeout),
		States:     postgres.NewOAuthStateRepository(d.DB),
		Publisher:  publisher{q: d.Queue},
		Issuer:     issuer,
		UoW:        d.DB,
		Logger:     d.Logger,
		Config: domain.Config{
			SessionMaxAge:      d.Config.SessionMaxAge,
			SessionIdleTimeout: d.Config.SessionIdleTimeout,
			AccessTokenTTL:     d.Config.AccessTokenTTL,
			RefreshGracePeriod: d.Config.RefreshGracePeriod,
			ReauthWindow:       d.Config.ReauthWindow,
		},
		SessionPolicy: configuredSessionPolicy(d.Config),
	})
	m := &Module{
		Service:  svc,
		cfg:      d.Config,
		oauth:    wechatRegistry(d.Config.WeChat),
		redis:    d.Redis,
		logger:   d.Logger,
		verifier: d.AssertionVerifier,
		metrics:  d.Metrics,
	}
	if m.metrics == nil {
		m.metrics = prometheus.NewRegistry()
	}
	if d.Config.AssertionSigningKey != "" {
		signer, err := crypto.NewAssertionSigner(d.Config.AssertionSigningKey, d.Config.AssertionSigningKID)
		if err != nil {
			panic("user: assertion signing key passed LoadConfig but does not parse: " + err.Error())
		}
		m.signer = signer
	}
	return m
}

// Middleware 返回其它模块保护前台操作用的鉴权中间件。USER_AUTH_MODE=session（默认）时验签 → 按 sid 查会话，吊销
// 立即生效；gateway 时验证网关转来的身份断言（会话由网关调本模块的校验端点实时查过，见 docs/agents/auth.md）。
func (m *Module) Middleware() echo.MiddlewareFunc {
	if m.cfg.AuthMode == AuthModeGateway {
		return httpserver.NewGatewayIdentityMiddleware(m.verifier)
	}
	return httpapi.NewSessionMiddleware(m.sessionAuthConfig())
}

func (m *Module) sessionAuthConfig() httpapi.SessionAuthConfig {
	return httpapi.SessionAuthConfig{
		Sessions:       m.Service,
		KeyID:          m.cfg.JWTKeyID,
		Key:            []byte(m.cfg.JWTSigningKey),
		AdditionalKeys: m.cfg.JWTAdditionalKeys,
		ReauthWindow:   m.cfg.ReauthWindow,
	}
}

// RequireRecentAuth 返回保护 † 操作（改绑登录方式、注销等）的中间件：最近验证要在 USER_REAUTH_WINDOW 之内，
// 挂在 Middleware() 之后。两种鉴权方式都把截止时间写进 UserSession.ReauthUntil。
func (m *Module) RequireRecentAuth() echo.MiddlewareFunc {
	return httpserver.NewRequireRecentAuth()
}

// RegisterInternalHTTP 挂载 user 在 api 内部端口上的接口：网关的会话校验端点。没配签名私钥时端点照样注册、
// 一律 500（见 httpapi.NewVerifyHandler）。
func (m *Module) RegisterInternalHTTP(g *echo.Group) {
	verify, err := httpapi.NewVerifyHandler(m.sessionAuthConfig(), m.signer, m.metrics)
	if err != nil {
		panic("user: register gateway verify endpoint: " + err.Error())
	}
	httpapi.RegisterInternal(g, verify)
}

// RegisterHTTP 挂载 api/openapi.yaml 中 tag=user 的全部操作。
func (m *Module) RegisterHTTP(g *echo.Group) {
	httpapi.Register(g, httpapi.NewHandler(httpapi.Deps{
		Service:               m.Service,
		OAuth:                 m.oauth,
		NativeClients:         nativeClients,
		PublicBaseURL:         m.cfg.PublicBaseURL,
		CookieDeliveryOrigins: m.cfg.CookieDeliveryOrigin,
	}), httpapi.Middlewares{
		Auth:              m.Middleware(),
		RequireRecentAuth: m.RequireRecentAuth(),
		RateLimit: echoMiddleware.RateLimiterWithConfig(echoMiddleware.RateLimiterConfig{
			Store: ratelimit.NewStore(m.redis, "auth", ratelimit.PerSecond(authRatePerSecond), m.logger),
		}),
	})
}

// authRatePerSecond 是 /v1/auth/* 按 IP 限流的默认值（全局限流之外的一道防线）。
const authRatePerSecond = 5

// RegisterAdminHTTP 挂载 api/admin.yaml 中 tag=users 的全部操作。
func (m *Module) RegisterAdminHTTP(g *echo.Group) {
	adminapi.Register(g, adminapi.NewHandler(m.Service))
}

// RegisterJobs 注册 river worker：过期行清理，以及 user_deleted 的编排 worker
// （消费 user 自己发布的事件，清理其它模块的数据，cleaners 由组合根注入）。
func (m *Module) RegisterJobs(workers *queue.Workers) {
	queue.AddWorker(workers, jobs.NewCleanupWorker(m.Service))
	queue.AddWorker(workers, jobs.NewUserDeletedWorker(m.dataCleaners))
}

// PeriodicJobs 每小时清理一次 oauth_states 和 user_sessions 的过期行，
// 进程启动时先跑一次。
func (m *Module) PeriodicJobs() []queue.Periodic {
	return []queue.Periodic{{
		Schedule: river.PeriodicInterval(time.Hour),
		Args:     jobs.CleanupArgs{},
		Opts:     &river.PeriodicJobOpts{RunOnStart: true},
	}}
}
