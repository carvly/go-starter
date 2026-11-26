# 鉴权

事实来源：`internal/platform/httpserver/identity.go`、`internal/platform/httpserver/gateway_identity.go`、`internal/platform/crypto/`、`internal/platform/oauth/`、`internal/kit/authn/`、`internal/modules/user/module.go`、`internal/modules/user/httpapi/`、`internal/modules/staff/adminapi/guard.go`、`deploy/baremetal/openresty/`

给接口加鉴权、改身份类型或密码与令牌的实现之前读这篇。各模块自己的规则跟模块放在一起，随模块一起删除：

- 前台用户（登录入口、会话、第三方登录链路、微信平台要点、业务怎么扩展用户）：
  [`internal/modules/user/docs/user.md`](../../internal/modules/user/docs/user.md)；
- 后台员工（账号、会话、登录退避、权限码、操作日志、应急恢复）：
  [`internal/modules/staff/docs/staff.md`](../../internal/modules/staff/docs/staff.md)。

## 两套身份体系

前台用户（user 模块：第三方登录、JWT + refresh，面向 web、App、小程序）和后台员工（staff 模块：用户名 + 密码、
服务端会话 cookie，只有同源的后台 web）是两套独立的体系。

为什么后台不复用前台的登录体系：后台可能只有一两个人使用，不能依赖邮件、短信、企业 IdP 或第三方登录
（邮箱被盗就等于后台被盗）；后台只有一个同源的 web 客户端，用服务端会话 cookie 即可，不需要为 App
和小程序设计的 JWT + refresh。

请求上下文里的身份按主体分类型（`internal/platform/httpserver/identity.go`：前台用户 `UserSession`，后台员工
`StaffSession`），一个 handler 读错了类型只会得到 401，不会把另一类主体的 ID 当成用户 ID。

## 前台鉴权：两种模式，二选一

由 `USER_AUTH_MODE` 切换，选择逻辑在 `internal/modules/user/module.go` 的 `Middleware()`。两种实现都把完整身份
（`UserID`、`SessionID`、`AuthTime`、`AMR`、`ReauthUntil`）写进 `httpserver.SetUserSession`（它同时把 `user_id` 写进
日志关联字段），handler 用 `httpserver.UserSessionFrom(c)` 读，不需要知道当前是哪一种。

- **`session`（默认）：会话中间件**（`internal/modules/user/httpapi/middleware.go` 的 `NewSessionMiddleware`）：
  验签（HS256 + `kid` 轮换，旧密钥放 `JWT_ADDITIONAL_KEYS`，轮换步骤见 [deploy-ci.md](deploy-ci.md) 的"配置"）
  → 校验 iss/aud/exp → 按 `sid` 查会话。吊销立即生效。没带令牌、验签失败、会话失效返回 401 `invalid_token`，会话被
  登录策略顶掉（内置策略按设备类别）返回 401 `session_replaced`（见 user.md 的"会话策略"）；查会话的数据库查询失败也拒绝，但返回
  500：客户端只在 401 时清令牌（处理顺序见 [http.md](http.md) 的"客户端约定"），服务端故障不会把用户登出；
- **`gateway`：网关鉴权**（见下一节）：OpenResty 调 user 服务的校验端点实时查会话，换成签名的身份断言转给后端；后端用
  `internal/platform/httpserver/gateway_identity.go` 的 `NewGatewayIdentityMiddleware` 验证断言。按业务模块拆出独立部署
  的服务、或网关后面有其它语言的后端时才需要；单体部署用 `session`。

前台鉴权的错误（`invalid_token`、`session_replaced`、`recent_auth_required`、`auth_unavailable`）定义在
`internal/kit/authn`：会话中间件（user 模块）和网关身份中间件（platform）返回同一个值，user 的 `domain/errors.go`
引用它们。

## 网关鉴权

```
客户端 ──Authorization──> OpenResty（location /v1/，access_by_lua：openresty/lua/gateway_auth.lua）
  清掉客户端带来的 X-Auth-*；没带令牌 → 匿名放行（不发子请求）
  读完请求体 → 子请求 /_auth ──> api 内部端口 GET /internal/auth/verify（只转发 Authorization、Host、X-Trace-Id、X-Forwarded-For）
    200 + X-Auth-Assertion → 转给后端
    200 + X-Auth-Status    → 转给后端（公开接口照常处理，需要登录的操作返回对应的 401）
    429（校验端点限流）    → 原样回给客户端
    其它（5xx、连不上）    → 网关直接 503 auth_unavailable
后端：NewGatewayIdentityMiddleware 验证断言
```

**会话是否有效只由 `domain.Service.Authenticate` 判断**：会话中间件和校验端点共用 `httpapi/middleware.go` 的同一段代码，
网关不持有任何密钥（为什么不在网关里自己校验，见 [ARCHITECTURE.md](../ARCHITECTURE.md) 的"技术选型"）。

**为什么用 Lua 而不是 nginx 的 `auth_request`**：网关不可用时要返回和应用一致的 JSON 错误体（`auth_unavailable`），
`auth_request` 的子请求失败只能得到 nginx 的错误页；断言的有效期要从请求体读完之后算起（Lua 先 `ngx.req.read_body()`，
否则弱网慢上传会让断言在途过期），`auth_request` 在读请求体之前就发子请求；把结果转给上游要在 location 里写
`proxy_set_header`，会让 server 级的 `snippets/proxy.conf` 全部失效，Lua 用 `ngx.req.set_header` 直接改请求头。

**身份断言**（`internal/platform/crypto/assertion.go`）：ES256（P-256）JWT，header 带 `kid`，`aud` 为
`gateway-assertion`（与 access token 不同，两种令牌不能互相冒用），30 秒有效，每个请求重新签发（不是缓存）。claims
带 `sub`、`sid`、`auth_time`、`amr` 和 `reauth_until`（`auth_time + USER_REAUTH_WINDOW`，由签发方算好，验证方不需要
知道窗口长度）。用 ES256：只有 user 服务持有私钥，验证方拿到的公钥不能签名，拆出去的服务被攻破也伪造不了断言。

- 要签名而不是信任明文头：明文头的安全性完全依赖"客户端连不到后端""每个 location 都清头"，任何一个配错就是任意冒充；
- 不复用 `JWT_*`：持有 `JWT_SIGNING_KEY` 就能签发登录令牌，验证方不该有这个能力；
- 不绑定具体请求（不签 method、path）：断言只在网关到后端的内网里出现，绑定要网关参与签名，收益不抵复杂度。代价是在内网
  截获的断言 30 秒内可以重放，只能冒充同一个人，靠内网隔离；
- 验证允许签发方与验证方 5 秒的时钟偏差，超过时按服务端问题返回 503；
- 开销：每个带令牌的请求多一次网关到 user 服务的内网 HTTP（keepalive 复用连接）和一次 ES256 签名；查库次数与会话中间件
  相同（每个请求一次）。单体部署不开这个模式，没有额外开销。

**校验端点的契约**（`internal/modules/user/httpapi/verify.go`，挂在 api 的内部端口上，不进 spec：它是网关和 user 服务
之间的基础设施接口，不是客户端契约，和健康检查同类；Go 端单元测试逐行覆盖下表，Lua 与 Go 之间的约定由
`deploy/baremetal/gatewaytest` 在真实 OpenResty 里验证）：

| 情况 | 响应 |
|---|---|
| 会话有效 | 200，`X-Auth-Assertion: <断言>`，`Cache-Control: no-store` |
| 没带令牌、令牌无效、会话失效 | 200，`X-Auth-Status: invalid_token` |
| 会话被登录策略顶掉 | 200，`X-Auth-Status: session_replaced` |
| 同一个客户端 IP 超过限流额度 | 429（网关原样回给客户端）。内部端口按客户端 IP 限流，额度同 `HTTP_GLOBAL_RATE_LIMIT_PER_SECOND`、与前台分开计数；IP 取自网关转来的 `X-Forwarded-For`，前提是 `HTTP_TRUSTED_PROXY=true`，否则所有客户端共用网关这一个 IP 的额度 |
| 查会话失败、没有配置签名私钥 | 500（网关返回 503） |

**后端中间件的结果**：

| 请求 | 结果 |
|---|---|
| 断言有效 | 放行，写入完整身份 |
| 没有断言，`X-Auth-Status: session_replaced` | 401 `session_replaced` |
| 没有断言，其它 | 401 `invalid_token` |
| 带了断言但验不过（`kid` 不认识、签名不对、过期、格式不符） | 503 `auth_unavailable` |

网关先清掉客户端带来的断言，经网关到达的断言一定是 user 服务签的，验不过只能是服务端的问题（密钥轮换漏配、公钥贴错、
时钟偏差）：返回 401 会让客户端刷新令牌、再失败就清令牌，把用户登出。错误日志里区分两类原因（`crypto.ErrAssertionInvalid`
多半是公钥贴错或伪造，`ErrAssertionUnavailable` 多半是轮换漏配或时钟偏差）。`X-Auth-Status` 不签名：伪造它只能让
自己被拒绝。客户端直连后端时自带的断言验不过签名，所以"客户端连不到后端""每个 location 都清头"只是纵深防御，配错
不会变成任意冒充；但能直连的人可以制造 503 和错误日志，后端端口（前台、内部端口）都只能让网关连到。

**配置**：

| 配置 | 归属 | 说明 |
|---|---|---|
| `USER_AUTH_MODE` | user 模块 | `session` 或 `gateway`；`gateway` 时必须配 `GATEWAY_ASSERTION_PUBLIC_KEYS` |
| `USER_ASSERTION_SIGNING_KEY`、`USER_ASSERTION_SIGNING_KID` | user 模块（只在 user 服务上） | 签名私钥（PKCS#8 DER 的 base64）和 `kid`，成对配置；P-256，且公钥清单里必须有它对应的公钥，否则启动失败 |
| `GATEWAY_ASSERTION_PUBLIC_KEYS` | 基础设施（`internal/config`，只读环境变量） | `kid:公钥` 列表（SPKI DER 的 base64），所有验证方用同一份；拆出去、没有 user 模块的服务也能加载 |
| `HTTP_API_INTERNAL_ADDR` | 基础设施 | api 内部端口的地址，为空时不启动 |

签发方不把"由私钥推出的公钥"自动加进自己的验证集合：那样签发方自己验得过，清单里漏了这把公钥的其它服务却全部失败。
所以 user 模块加载配置时读同一个 `GATEWAY_ASSERTION_PUBLIC_KEYS`，检查清单里有自己正在用的公钥。部署、切换顺序和
密钥轮换见 [deploy-ci.md](deploy-ci.md) 的"网关鉴权"。

**排查**：校验结果、已加载的公钥和网关访问日志的 `auth` 字段见 [observability.md](observability.md) 的"指标"。

**拆出去的服务**：没有 user 模块的代码，在自己的组合根里用 `httpserver.NewGatewayIdentityMiddleware(Infra 的验证器)`
和 `httpserver.NewRequireRecentAuth()` 接线；生成器"恰好一个模块提供 `Middleware()`"的规则在拆分时随各服务的组合根
一起重新设计（见 [modules.md](modules.md) 的"拆成独立部署的服务：前置条件"）。

## 给操作加鉴权

前台接口：模块的 `Deps` 收一个 `Auth echo.MiddlewareFunc`，由组合根传入
`userM.Middleware()`（这样模块在 `modules.go` 里依赖 user，删除演练会连带处理）；在
`httpapi/routes.go` 的 `OperationMiddlewares` 里按 operationId 挂上，spec 里对应操作去掉
`security: []`。敏感操作（改绑登录方式、注销等，接口文档标 †）在 Auth 之后再挂
`mw.RequireRecentAuth`，由组合根传入 `userM.RequireRecentAuth()`（`httpserver.NewRequireRecentAuth`：比较
`UserSession.ReauthUntil`，窗口是 `USER_REAUTH_WINDOW`）：

```go
// internal/modules/<m>/httpapi/routes.go
func Register(g *echo.Group, h *Handler, mw Middlewares) {
	oapi.RegisterHandlersWithOptions(g, h, oapi.RegisterHandlersOptions{
		OperationMiddlewares: map[string][]echo.MiddlewareFunc{
			"createThing":     {mw.Auth},
			"dangerousThing":  {mw.Auth, mw.RequireRecentAuth},
		},
	})
}
```

生成器的 `web.auth` 只生成 `{mw.Auth}` 这一种挂法，† 操作的 `RequireRecentAuth` 要手动加。生成时
`REPO-AUTH-MISSING` 检查恰好有一个模块提供 `Middleware()`。

仓库里目前没有其它前台模块挂它：管理类操作（新建、修改、调整业务数据）放在后台接入面，由员工
的会话和权限码保护（见 `internal/modules/staff/docs/staff.md`），不是"登录用户即管理员"。后台操作不需要上面这些，
写 `x-permission` 即可。

## 必须保持的安全性质

- 密码哈希是 Argon2id（`alexedwards/argon2id`，`internal/platform/crypto`），参数从基础设施配置
  （`ARGON2_*`）显式传入，不依赖库默认值，按部署环境内存和登录 QPS 压测调整。现在只有后台
  员工的密码用它，前台加密码登录时复用同一组参数；
- 会话和权限每个请求都查库、不缓存，查询失败必须拒绝（fail closed）：吊销、停用、收回权限立即生效。前台的查询失败
  返回 500 而不是 401，拒绝不变，只是不让客户端误以为登录失效；
- 登录失败对客户端不暴露原因（账号是否存在、第三方的具体错误），细节只进日志；
- 前台和后台各自模块的具体规则见上面两篇模块文档。
