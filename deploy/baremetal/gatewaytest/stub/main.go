// Command stub 是网关鉴权集成测试（deploy/baremetal/gatewaytest）放进 OpenResty 容器里的后端：
//
//   - 内部端口（:8082）：会话校验端点的桩，按令牌返回约定里的每一种结果（Go 端真实的校验端点另有单元测试）；
//   - 前台端口（:8080）：真实的 httpserver（网关后部署：信任 X-Trace-Id）和真实的 NewGatewayIdentityMiddleware。
//
// 它验证的是 OpenResty 配置和 gateway_auth.lua 与 Go 端之间的约定，不是会话逻辑本身。
package main

import (
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/labstack/echo/v5"

	"app.local/internal/config"
	"app.local/internal/platform/crypto"
	"app.local/internal/platform/httpserver"
	"app.local/internal/platform/tracing"
)

// verifyCall 是校验端点收到的一次请求，经 /v1/debug/verify-calls 给测试看。
type verifyCall struct {
	Header http.Header `json:"header"`
	At     time.Time   `json:"at"`
}

func main() {
	signer, err := crypto.NewAssertionSigner(os.Getenv("STUB_SIGNING_KEY"), "k1")
	if err != nil {
		log.Fatal(err)
	}
	verifier, err := crypto.NewAssertionVerifier(map[string]string{"k1": os.Getenv("STUB_PUBLIC_KEY")})
	if err != nil {
		log.Fatal(err)
	}
	var (
		mu    sync.Mutex
		calls []verifyCall
	)

	internal := http.NewServeMux()
	internal.HandleFunc("GET /internal/auth/verify", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, verifyCall{Header: r.Header.Clone(), At: time.Now()})
		mu.Unlock()
		switch r.Header.Get("Authorization") {
		case "Bearer valid":
			token, err := signer.Sign(crypto.Assertion{UserID: "u-1", SessionID: "s-1", AuthTime: time.Now(), ReauthUntil: time.Now().Add(time.Minute)})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set(httpserver.HeaderAuthAssertion, token)
		case "Bearer replaced":
			w.Header().Set(httpserver.HeaderAuthStatus, "session_replaced")
		case "Bearer limited":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"code":"http_error","message":"rate limit exceeded"}`))
			return
		case "Bearer error":
			http.Error(w, "db down", http.StatusInternalServerError)
			return
		case "Bearer down":
			// 模拟 user 服务连接中断：nginx 拿到 502，网关应返回 503。
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
		default:
			w.Header().Set(httpserver.HeaderAuthStatus, "invalid_token")
		}
		w.WriteHeader(http.StatusOK)
	})

	auth := httpserver.NewGatewayIdentityMiddleware(verifier)
	front, err := httpserver.New(httpserver.Deps{
		Logger: slog.New(slog.NewJSONHandler(os.Stderr, nil)),
		HTTP: config.HTTPConfig{
			BodyLimitBytes: 4 << 20, RequestTimeout: 60 * time.Second, GlobalRateLimitPerSecond: 1000, TrustedProxy: true,
		},
		CORS:   config.CORSConfig{AllowOrigins: []string{"https://app.test"}},
		Tracer: tracing.Disabled(),
		Routes: []func(*echo.Group){func(g *echo.Group) {
			g.GET("/whoami", func(c *echo.Context) error {
				s, _ := httpserver.UserSessionFrom(c)
				return c.JSON(http.StatusOK, map[string]any{"user_id": s.UserID, "session_id": s.SessionID})
			}, auth)
			g.POST("/upload", func(c *echo.Context) error {
				n, err := io.Copy(io.Discard, c.Request().Body)
				if err != nil {
					return err
				}
				return c.JSON(http.StatusOK, map[string]any{"bytes": n})
			}, auth)
			g.GET("/public", func(c *echo.Context) error {
				return c.JSON(http.StatusOK, map[string]any{
					"assertion": c.Request().Header.Get(httpserver.HeaderAuthAssertion),
					"status":    c.Request().Header.Get(httpserver.HeaderAuthStatus),
				})
			})
			g.GET("/debug/verify-calls", func(c *echo.Context) error {
				mu.Lock()
				defer mu.Unlock()
				return c.JSON(http.StatusOK, calls)
			})
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	go func() { log.Fatal(http.ListenAndServe(":8082", internal)) }()
	log.Println("stub ready")
	log.Fatal(http.ListenAndServe(":8080", front))
}
