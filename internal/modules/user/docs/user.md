# user 模块：前台用户

事实来源：`internal/modules/user/`、`internal/platform/oauth/`

改 `internal/modules/user/` 或 `internal/platform/oauth/` 之前读这篇；跨模块的鉴权机制（前台鉴权的两种模式、给操作加
鉴权、安全性质）见 `docs/agents/auth.md`。本文随模块一起删除。

## 职责与规则

`internal/modules/user/` 管前台用户：账号的后台管理、登录（微信四个入口，按需启用）、
会话、重新验证、绑定/解绑、注销，以及供其它模块挂载的前台鉴权中间件。

- **用户的后台管理**：`users` 表和后台的列表、详情、停用 / 启用（`user:read`、
  `user:disable`、`user:enable`）由生成器生成（描述文件存为夹具
  `tools/gen/testdata/fixture_user.yaml`，**已冻结**：登录代码是生成器管理范围之外的
  手写代码，不再对 user 模块重新 `gen apply`，见 `docs/agents/generator.md`）。生成之后
  手写加了用户详情页的登录方式列表（`listUserIdentities`）、会话列表（`listUserSessions`，
  都是 `user:read`）和踢下线（`kickUserSession`，`user:kick`）。账号只能经登录创建，
  后台不能新建或编辑。停用
  （`Service.Disable`）在同一个事务里先改状态、再用另一条语句吊销该用户全部未吊销的会话
  （`user_sessions`），会话校验每次都查库，停用立即生效。吊销不能和改状态写进同一条语句（数据修改的 CTE），
  原因见 `docs/agents/transactions.md` 的例子。
- **登录方式**：`user_identities` 表，`(provider, subject)` 唯一定位，每种 provider
  每个账号最多一个，绑定新的就替换旧的。现在只有微信（`subject` 是 unionid）；
  邮箱/手机号只能由验证码流程创建，第三方声明的邮箱不落库也不成为登录方式。微信各入口的 openid 单独存 `wechat_openids`（支付、模板消息用），
  换绑成另一个 unionid 时先删该账号全部 openid 再写入。
- **登录入口**：网站应用扫码、服务号网页授权走浏览器 OAuth 流程
  （`internal/platform/oauth/` 的 `Provider`：一次性 state + 浏览器绑定 cookie 防
  登录 CSRF，登录多一步兑换）；小程序 `wx.login`、App SDK 走原生接口
  （`Native`）。第三方的 code 换身份在事务之外同步完成；"解析账号"的安全规则
  写在 `domain` 的 `ResolveLogin`/`ResolveBind` 里，所有入口只能经过它。
- **通用链路**：流程按 provider 的能力声明工作，新增 provider 不改流程、表结构和 spec。
  - 浏览器：`Provider.Capabilities()` 返回 `BrowserCapabilities{PKCE, Nonce}`。声明了的，发起时由
    `domain` 的 `StartOAuthFlow` 生成 verifier（43 个字符）和 nonce，随 state 行存进 `oauth_states`
    的 payload；授权链接带 `oauth.S256Challenge(verifier)` 和 nonce；回调一次性取出 state 行，把
    verifier、nonce 和回调的全部参数（`ExchangeParams.Callback`）交给 `Exchange`。重新验证时
    `AuthorizeParams.Reauthenticate` 为真，provider 自行加 `prompt` 之类的参数。provider 专有的回调参数
    （如微信快照页的 `is_snapshotuser`）由 provider 自己处理，流程只把哨兵错误映射成结果码。
  - 回调同时接受 GET 和表单 POST（`response_mode=form_post`），两者走同一个 `handleOAuthCallback`。
  - 原生：`Native.Capabilities()` 返回 `NativeCapabilities{Nonce}`。声明了 nonce 的，客户端先调
    `POST /v1/auth/native/{provider}/nonce`（`oauth_states` 的 `kind=nonce`，绑定 provider，5 分钟），
    再随凭证提交；流程一次性核销后交给 provider 比对 id_token，截获的 id_token 因此无法重放。原生请求
    体的 `code`、`id_token`、`nonce`、`name` 都是可选的，缺了 provider 需要的字段返回
    `oauth.ErrMissingCredential`；所有原生入口都经 `httpapi` 的 `verifyNative`。
  - 校验失败对客户端一律是登录失败；`oauth.ErrNoUnionID`（应用没绑定开放平台）按错误级别记录，便于报警。
- **入口按需启用**：四个入口是组合关系，产品用到哪个就配哪个应用的凭据。
  `WECHAT_<WEB|MP|MINI|APP>_APP_ID` 和 `_SECRET` 成对配置才启用
  （`WeChatAppCredentials.Enabled`），都留空就不启用，只配一半启动失败。`providers.go` 的
  `wechatRegistry` 只登记启用的入口；请求未启用的入口（登录、绑定、重新验证）和未知入口
  一样返回 400。注册表的装配在 `providers.go`。
- **会话**：每次登录建一行 `user_sessions`。access token 是 HS256 的 JWT，header 带
  `kid`，claims 只有 iss/aud/sub/sid/iat/exp，有效期 `USER_ACCESS_TOKEN_TTL`（默认
  15 分钟）；**每个请求按 `sid` 查会话行**（带 `users.status`）：吊销、过期、空闲超时、
  账号停用立即生效，不加缓存；查询失败也拒绝，但返回 500（见下文"必须保持的安全性质"）。
  refresh token 按 `refresh_family` 定位，
  轮换后上一代在 `USER_REFRESH_GRACE_PERIOD`（默认 10 秒）内仍可用；`refresh_family`
  对得上、随机串既不是当前值也不是宽限期内的上一代，而会话仍然有效时，视为泄露、吊销
  整个会话（`domain/session.go` 的 `Refresh`）；客户端必须串行刷新。refresh token 按
  请求 `Origin` 交付：名单内（`USER_COOKIE_DELIVERY_ORIGINS`）只进 httpOnly +
  SameSite=Strict cookie（Path `/v1/auth/session`），名单外（App、小程序）走响应体。
- **两种前台鉴权**（`USER_AUTH_MODE`）：默认按 access token 查会话；网关鉴权时网关调本模块的校验端点
  （`httpapi/verify.go`，经 `RegisterInternalHTTP` 挂在 api 内部端口上）换身份断言。机制见 `docs/agents/auth.md` 的
  "前台鉴权：两种模式，二选一"。
- **会话记录的客户端**：`user_sessions.client` 和 `users.signup_client` 的宿主和形态由**登录入口**决定：浏览器兑换
  固定是 `web`；小程序、小游戏各有自己的 AppID，入口就决定了客户端（现在只有 `wechat-miniprogram`；小游戏没有入口，
  接入时按下文"接入新的登录方式"新增 `wechat-minigame`），不看请求头；微信移动应用在
  Android、iOS、鸿蒙上共用一组 AppID，系统取客户端自报的请求头 `X-Client-Platform`（可以伪造，只用于展示），必须是
  `android`、`ios`、`harmonyos`，
  并且请求体必须带 `device_class`（`phone`、`tablet`），否则返回 400 `client_platform_invalid` /
  `device_class_invalid`（`device_class` 取值不对时 spec 的校验先拦下，返回 `invalid_request`）。这两项在校验凭证（`verifyNative`）之前检查，授权码和 nonce 还没用掉，客户端修正后可以
  直接重试。入口与客户端的对应登记在 `providers.go` 的 `nativeClients`（`TestNativeClients_CoverEveryNativeEntry`
  检查每个原生入口都登记了）。`client_version`、`client_os`（小程序宿主所在的系统）取自请求头，格式不合法就不记，
  只用于展示（"我的会话"、后台会话列表）；和 `user_agent`、`ip` 一样只记登录那一刻，refresh 不更新。安装 ID 不落库：
  一串 UUID 在会话列表里没有展示价值，有按设备管理会话的需求时再加。客户端判断 `device_class`：iOS 用
  `userInterfaceIdiom == .pad`，Android 按屏幕最短边 ≥ 600dp。接入微信小游戏后，只要绑定在同一个开放平台账号下，
  它与小程序、App 是同一个 unionid、同一个账号；抖音是另一套身份体系，接入时是新的 provider，不并进 `wechat`。
- **登录与停用、注销并发**：`ResolveLogin` 以 `Repository.TouchLogin` 的条件 UPDATE（`status = 'active'`）判断
  账号状态，不先读再判断；这条 UPDATE 同时锁住用户行，同一账号的并发登录在登录事务里排队。
- **登录接口的防护**：`/v1/auth/*` 的操作（发起授权、回调、兑换、原生登录、签发原生 nonce、刷新、登出）在
  全局限流之外再按 IP 限流，每秒 5 次（`module.go` 的常量 `authRatePerSecond`，不可配置），配了
  Redis 时多实例共享计数；带请求体的操作只接受 `application/json`，表单等其它类型返回
  415（`httpapi/json_only.go`，挂在哪些操作上见 `httpapi/routes.go`），跨站页面不触发
  预检的表单请求到不了 handler。唯一的例外是表单 POST 回调（`oauthCallbackForm`）：第三方本来就用跨站
  表单回调，防护由一次性 state 和绑定 cookie 承担——绑定 cookie 是 `SameSite=None; Secure`，只在发起
  授权的浏览器里有，跨站表单带不来别人的。
- **必需配置**：`USER_PUBLIC_BASE_URL` 是浏览器 OAuth 流程 redirect_uri 的 origin，必须是
  浏览器会发出的规范 origin（小写 `scheme://host[:port]`，不带默认端口、路径和用户信息），
  `USER_COOKIE_DELIVERY_ORIGINS` 的每一项同样要求，不规范时启动失败。`WECHAT_*` 不是
  必需项，见上面的"入口按需启用"（校验在 `internal/modules/user/config.go` 的 `LoadConfig`）。
- **我的会话、我的登录方式**：`GET /v1/me/sessions`（只列未吊销的）和
  `GET /v1/me/identities` 按 cursor 分页，按创建时间倒序（`PageByUser`）；后台用户详情页
  的两个列表和账号内部逻辑用全量的 `ListByUser`。
- **重新验证**：† 操作要求 `auth_time` 在 `USER_REAUTH_WINDOW`（默认 10 分钟）内，
  由 `RequireRecentAuth` 中间件检查（见 `docs/agents/auth.md` 的"给操作加鉴权"）。现在账号只有微信登录
  方式，重新验证只能走微信（小程序入口静默完成，客户端在注销/换绑前自己弹确认框）。
- **注销**：`DELETE /v1/me`（†）在一个事务里置 `status=deleted`、清空昵称头像、
  吊销全部会话、删除全部登录方式和 openid，并发布 `user_deleted` 事件
  （worker 在 user 的 `jobs/` 里，清理器在组合根通过 `userM.AddDataCleaner(...)` 登记，
  见 `docs/agents/queue.md`）。`users.last_login_at` 在每次登录时更新，`users.signup_client`
  记录建号时的客户端；后台列表可以按最近登录时间排序和筛选。
- **过期行清理**：`user_cleanup` 定时任务（user 的 `PeriodicJobs()`，每小时 +
  启动时一次）删除 `oauth_states` 过期行、`user_sessions` 过期行和吊销超过 7 天的行。
- **换绑是账号所有权的变更**：同一种 provider 换成新的 subject 时，吊销用旧登录方式建立的其它
  会话，当前会话的 `login_identity_id` 改指向新的登录方式（`ResolveBind`）。以后即使改成原地更新
  `subject`，也必须吊销。同一个 unionid 从别的入口进来只 upsert 本入口的 openid；换成另一个
  unionid 才清空全部 openid。
- **解绑**：不能解绑最后一种登录方式（`ErrLastIdentity`）；解绑时吊销用它登录的其它会话。以后加
  密码时，解绑最后一个邮箱或手机号要同时清空密码（密码只能配合邮箱或手机号使用）。
- **重新验证必须属于当前账号**：第三方返回的 `(provider, subject)` 不属于当前账号就拒绝
  （`ErrReauthWrongAccount`）。第三方的重新验证强度弱（已授权过的会自动同意），以后有了密码或
  验证码，第三方重新验证只作为没有其它凭据的账号的兜底。
- **建号时的昵称和头像**：取第三方的昵称，为空时（小程序入口）用"微信用户"；昵称按字符截到 64，
  头像超过 1024 不存；原始值照常进 `profile`。
- **`return_to` 和 `redirect_uri`**：`return_to` 只接受以单个 `/` 开头的站内路径，整串不能有
  反斜杠和控制字符，解析后不能有 scheme 和 host（`ValidReturnTo`；浏览器会去掉制表符、把 `\` 当 `/`，只看前缀
  挡不住 `/<制表符>/evil.com`）；`redirect_uri` 由 `USER_PUBLIC_BASE_URL` 加固定路径拼出，和平台侧登记的值精确
  匹配。
- **为什么每个请求查库还保留 JWT + refresh 两层**：web 的长期凭据只在 httpOnly cookie 里；refresh
  轮换能发现盗用；签名先挡掉伪造的 token，不必为它们查库。泄露检测按 `refresh_family` 定位，因为
  `sid` 在 access token 里可读，按 `sid` 定位就能被用来踢别人下线。
- **客户端串行刷新**：web 用 Web Locks API，App、小程序在进程内做 single-flight。
- **不做的事**：不按邮箱自动合并账号，第三方声明的邮箱永远不成为 email 登录方式（攻击者可以用
  别人的邮箱抢先建号）；不做新设备登录通知；不保存第三方的 token；不做 App 内的网页授权。

## 微信平台要点

这些是微信平台的行为，代码里看不出来，接入和上线前逐条核对：

- **unionid 的前提**：启用的应用（网站应用、服务号、小程序、移动应用）都要绑定在**同一个**开放平台
  账号下，上线前完成；开放平台要先做开发者资质认证（付费）。网站应用和移动应用直接建在这个账号下；
  小程序在微信开发者平台"我的业务 - 开放平台 - 绑定关系"里绑定（不是"小程序 - 绑定关系"）；服务号在
  开放平台"管理中心 - 公众号"里绑定。拿不到 unionid 时登录失败（`oauth.ErrNoUnionID`），不退化成用
  openid。
- **服务号网页授权**：只有已认证的服务号能用；scope 必须是 `snsapi_userinfo`（`snsapi_base` 不返回
  unionid）；授权必须由用户点击触发，否则落进网页快照页，快照页的虚拟账号（`is_snapshotuser=1`）一律
  拒绝。官方文档把这个标记列为换 token 接口（`sns/oauth2/access_token`）的返回字段，userinfo 不一定
  带，所以换完 token、userinfo 返回、回调参数三处都检查；`state` 只能是 `[a-zA-Z0-9]`、不超过 128 字节，所以用 hex；授权链接的参数按官方顺序拼
  （现在 `url.Values.Encode` 按字母排序恰好一致，加参数时要核对）。
- **小程序**：`code2session` 返回 40226 是高风险用户被拦截，按登录失败处理；`session_key` 不下发也不
  保存；`wx.getUserProfile` 已回收，昵称头像由用户用"头像昵称填写能力"提交 `PATCH /v1/me`。
- **移动应用**：未上架的应用每天只能登录 100 次（10060），各平台要分别设为已上架。服务端同一个入口
  支持 Android、iOS、鸿蒙；iOS 上架前按当时的 App Store 审核指南 4.8 核对登录方式的要求。
- **协议不标准**：出错时 HTTP 200 + `errcode`，所以手写实现，不用聚合多家登录的库。
- 部署相关的约束（IP 白名单、回调域名）见 `docs/agents/deploy-ci.md` 的"前台登录的部署约束"。

## 会话策略（按设备类别互踢）

登录时可以按策略顶掉同一账号的旧会话，如"每类设备只能登录一个"。默认不启用：不启用时登录不多查询；启用后登录多两到三次查询（事务前预查登录方式，事务里列有效会话，需要顶掉时再吊销）。
不论是否启用，鉴权遇到失效的会话时都多查一次原始行，用来区分 `session_replaced` 和 `invalid_token`（只有验签通过的令牌
才会走到这一步）。

- **内置策略** `domain.SlotLimit`：配置 `USER_SESSION_SLOT_LIMIT=N`（正整数）启用，同一设备类别最多保留 N 个
  会话，新登录顶掉 `last_used_at` 最早的（平手按 ID）；0 或留空不启用。设备类别（`domain.DeviceSlot`）：App 按
  `device_class` 分 `phone`、`tablet`（不分系统），其余就是客户端本身（`web`、`wechat-miniprogram`）。改配置后
  滚动重启生效；关闭后已经被顶掉的会话保持吊销，不恢复。
- **自定义策略**：实现 `user.SessionPolicy`（如按会员等级限制设备数），写在组合根适配器 `internal/app/adapters_*.go`
  里（模块之间不能互相导入），在 `modules.go` 用独立一行 `userM.SetSessionPolicy(...)` 登记。只能有一个策略：
  同时配置了 `USER_SESSION_SLOT_LIMIT`，或重复登记，启动时 panic。
- **两段式**：`Prepare` 在登录事务之前调用，可以读其它模块（跨模块读不能放进事务，见 `docs/agents/modules.md`）；
  它返回的 `Decide` 在事务里调用，拿不到 ctx 也就拿不到登录事务，约定只用参数和闭包里准备好的数据、不做 IO（调用时持有
  用户行锁）。`Decide` 返回要吊销的
  会话 ID，或返回错误拒绝这次登录。准备时按登录方式预查账号：查不到是首次登录，没有旧会话，不调用策略。
- **执行归 Service**（`domain/login.go` 的 `admitSession`）：`ResolveLogin` 的 `TouchLogin` 已锁住用户行，事务里
  列出的有效会话（`ListActiveByUser`，和 `GetValid` 共用会话的有效条件，账号状态已由 `TouchLogin` 保证）包含先
  提交的那次登录；`Decide` 返回的 ID 必须都在其中，否则整次登录回滚、返回 500（策略的程序错误）；然后一条 SQL 吊销并标记 `replaced`。准备之后登录方式换了账号（并发的首次登录或换绑）
  时返回 `login_failed`，客户端重试即可。
- **被顶掉的设备**：鉴权返回 401 `session_replaced`；refresh 只在出示的令牌确实是这个会话的当前值或上一代时
  返回 `session_replaced`，只知道 family 的人拿到的是 `invalid_token`，探测不出"账号在别处登录了"。被吊销的行
  最多保留 7 天（快到绝对过期的会更早被清理），之后只返回 `invalid_token`。会话被顶掉后账号又被停用，鉴权仍返回
  `session_replaced`，客户端同样是重新登录，登录时才会看到账号已停用。
- **被顶掉时的客户端处理**（令牌处理的完整约定见 `docs/agents/http.md` 的"客户端约定"）：收到 `session_replaced` 清掉令牌，提示"账号已在其它设备登录"，由用户确认后再登录，**不能自动
  重新登录**：小程序常在启动时静默 `wx.login`，同一类别的两台设备会互相顶来顶去。只在本地没有令牌、且上次失效的
  原因不是 `session_replaced` 时静默登录。
- **为什么这样设计**：
  - 不建"设备类别"列：`web`、`wechat-miniprogram` 与 `client` 是同一个事实，按什么分组是策略自己的规则（业务策略可能
    用别的维度），由 `DeviceSlot` 现场计算；
  - 只有策略的吊销写 `replaced`：其它吊销路径（登出、自撤、后台踢、停用、注销、换绑、泄露检测）现在没有消费方，不记
    原因；后台要展示吊销原因时，连同"列表展示已吊销会话"一起设计；
  - 锁的顺序是 users → user_sessions，和停用、注销一致，不会死锁；
- **拒绝新登录**：starter 不自带这种策略，自定义策略的 `Decide` 返回错误即可（错误按 errs 的规则返回给客户端）；
  需要运行时切换或按用户配置时，由自定义策略自己读配置表。
- **信任边界**：`device_class` 是客户端自报的，把第二台手机报成 `tablet` 就能多登一台；互踢限制得住普通用户，
  防不住刻意绕过，严格防账号共享要另做设备证明。Web 和小程序的类别由登录入口决定，不能伪造。

## 接入新的登录方式

表结构、通用链路、"解析账号"、会话、解绑规则都不用改：

1. 在 `internal/platform/oauth/` 实现 `Provider`（浏览器跳转）或 `Native`（原生 SDK）：声明
   `Capabilities()`，在 `AuthorizeURL` 里放进 challenge、nonce 和自己专有的参数（如 `response_mode`），
   在 `Exchange` / `VerifyNative` 里校验（OIDC 的 id_token 签名、iss/aud/exp/nonce 由 provider 校验），
   从 `ExchangeParams.Callback` 读自己专有的回调字段；带用 httptest 模拟对方响应的测试；
2. `internal/modules/user/config.go` 加凭据和校验（按"能力和启用分开"：没配就不启用）；
3. 在 user 的 `providers.go` 的注册表里登记；原生入口同时登记 `nativeClients`：有自己 AppID 的小程序、小游戏
   填客户端（`<生态>-<形态>`，如 `douyin-minigame`），多个系统共用 AppID 的 App 填空；新的客户端取值同时加进
   `domain` 的 `Client*` 常量、`api/openapi.yaml` 的 `Session.client` 和 `api/admin.yaml` 的 `UserSessionClient`（带中文
   文案）。数据库列不限定取值，不需要迁移；
4. 在平台侧配置回调地址（指向 API 域名）；
5. `api/admin.yaml` 的登录方式枚举（已预留 email、phone、github、google、apple）补上显示名，
   `make generate`。

手机号、邮箱、密码登录和 GitHub、Google、Apple 等 provider 还没有实现。

## 业务怎么扩展用户

业务自己的用户资料放在业务模块里，按 `user_id` 关联，不建外键，需要时惰性初始化；
不要修改 user 的表或 Repository。user 提供两个组合根扩展点：

- `Service.GetPublicProfiles(ctx, ids) (map[string]PublicProfile, error)` 批量返回
  `id`、`display_name`、`avatar_url`、`status`，不存在的 ID 不在结果里；业务列表不要逐行查用户；
- `Service.WeChatOpenID(ctx, userID, appid) (string, error)` 查询指定微信应用下的 openid，
  供支付和订阅消息使用；不存在时返回 `domain.ErrNotFound`。

持有用户数据的模块还要同时提供两种幂等清理：

1. 实现 `user.DataCleaner`，全部模块构造完成后在 `internal/app/modules.go`
   用独立一行 `userM.AddDataCleaner(memberM.UserDataCleaner())` 登记，`user_deleted`
   worker 会即时调用；登记行不是构造依赖，删除任一模块时一起删除；
2. 提供每天一次的清扫任务，按本模块表里的 `user_id` 分批，经组合根适配器调用
   `GetPublicProfiles`，删除状态为 `deleted` 或结果里不存在的用户数据。它补偿滚动部署期间
   旧 worker 不认识新清理器、以及清理重试耗尽造成的遗漏；这个查询适配器让业务模块真实依赖
   user，所以删除 user 时该业务模块也要删除。

不推送注册、停用、启用事件：停用已由每个请求的会话查询实时生效，注销是终态。

## 必须保持的安全性质（前台）

- 停用前台用户必须同时吊销会话，并且两者原子完成；注销、踢下线同理；
- **吊销立即生效**：前台鉴权每个请求按 `sid` 查 `user_sessions`，不做 access token 主动
  吊销，也不缓存会话查询结果（缓存的"写后删"窗口会让吊销不成立）；查询失败必须拒绝
  （fail closed），返回 500；
- refresh token 只存随机串部分的摘要；family 对得上而随机串不对、会话仍有效时视为泄露，吊销
  整个会话；按 `refresh_family` 定位，只知道 `sid` 吊销不了别人的会话；
- 浏览器授权流程的一次性 state + 绑定 cookie 防登录 CSRF：没有绑定 cookie、或 cookie 摘要
  和流程行里登记的不一致，直接拒绝；
- 第三方登录失败（unionid 缺失、快照页、40226 等）对客户端统一返回"登录失败"，细节只进日志。
