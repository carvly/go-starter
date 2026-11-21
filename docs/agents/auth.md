# 鉴权

事实来源：`internal/platform/httpserver/identity.go`、`internal/platform/crypto/`、`internal/platform/oauth/`、`internal/modules/user/module.go`、`internal/modules/staff/adminapi/guard.go`

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

- **默认：会话中间件**（`internal/modules/user/httpapi/middleware.go` 的
  `NewSessionMiddleware`）：验签（HS256 + `kid` 轮换，旧密钥放 `JWT_ADDITIONAL_KEYS`）
  → 校验 iss/aud/exp → 按 `sid` 查会话。吊销立即生效，† 操作拿得到 `auth_time`；
- **网关信任 header**（`trusted_identity_middleware.go`）：部署在已经校验过身份的
  网关后面时用。这个模式下 `UserSession` 只有 `UserID`，没有服务端会话：依赖会话的操作（† 操作、绑定、
  重新验证）一律返回 403 `session_unavailable`（fail closed），不返回 `recent_auth_required`，客户端不会
  陷入"去重新验证、再失败"的循环。中间件本身不做任何签名校验，
  前提是客户端无法绕过网关直连服务、网关会清掉客户端带来的同名 header。配错
  不会报错，只会让鉴权悄悄失效。

由配置切换：`USER_TRUSTED_IDENTITY_HEADER` 留空用会话中间件，填上网关透传的 header 名（如
`X-User-ID`）用信任 header 模式，选择逻辑在 `internal/modules/user/module.go` 的 `Middleware()`。两种实现都把
身份写进 `httpserver.SetUserSession`（它同时把 `user_id` 写进日志关联字段），handler 用
`httpserver.UserSessionFrom(c)` 读，不需要跟着改。

## 给操作加鉴权

前台接口：模块的 `Deps` 收一个 `Auth echo.MiddlewareFunc`，由组合根传入
`userM.Middleware()`（这样模块在 `modules.go` 里依赖 user，删除演练会连带处理）；在
`httpapi/routes.go` 的 `OperationMiddlewares` 里按 operationId 挂上，spec 里对应操作去掉
`security: []`。敏感操作（改绑登录方式、注销等，接口文档标 †）在 Auth 之后再挂
`mw.RequireRecentAuth`，由组合根传入 `userM.RequireRecentAuth()`（窗口是
`USER_REAUTH_WINDOW`）：

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

生成器的 `web.auth` 只生成 `{mw.Auth}` 这一种挂法，不挂 `RequireRecentAuth`
（`REPO-AUTH-MISSING` 检查有且只有一个模块提供 `Middleware()`）。

仓库里目前没有其它前台模块挂它：管理类操作（新建、修改、调整业务数据）放在后台接入面，由员工
的会话和权限码保护（见 `internal/modules/staff/docs/staff.md`），不是"登录用户即管理员"。后台操作不需要上面这些，
写 `x-permission` 即可。

## 必须保持的安全性质

- 密码哈希是 Argon2id（`alexedwards/argon2id`，`internal/platform/crypto`），参数从基础设施配置
  （`ARGON2_*`）显式传入，不依赖库默认值，按部署环境内存和登录 QPS 压测调整。现在只有后台
  员工的密码用它，前台加密码登录时复用同一组参数；
- 会话和权限每个请求都查库、不缓存，查询失败必须拒绝（fail closed）：吊销、停用、收回权限立即生效；
- 登录失败对客户端不暴露原因（账号是否存在、第三方的具体错误），细节只进日志；
- 前台和后台各自模块的具体规则见上面两篇模块文档。
