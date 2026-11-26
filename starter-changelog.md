# starter 更新日志

starter 自身的发版说明，升级工具从目标版本读取这份文件的"升级说明"（格式见 `docs/agents/upgrade.md`）。
根目录的 `CHANGELOG.md` 留给业务项目记录自己的变更，starter 不提供、不覆盖。

## v0.4.0 — 2026-11-27

前台登录：客户端请求头约定、可插拔的登录会话策略，以及网关鉴权（OpenResty 调会话校验端点换 ES256 身份断言），
取代只做了一半的网关信任 header 模式。没有线上数据，所有改动直接到目标形态，不提供兼容层。

### 破坏性变更

- 前台鉴权失败统一返回 401 `invalid_token`，被登录策略顶掉的会话返回 401 `session_replaced`；鉴权、绑定、重新验证
  查库失败返回 500，不让客户端登出。错误定义移到 `kit/authn`，user 的 `domain` 保留别名。
- 删除网关信任 header 模式：去掉 `USER_TRUSTED_IDENTITY_HEADER` 和错误码 `session_unavailable`，† 操作在两种
  鉴权方式下都可用（`httpserver.NewRequireRecentAuth` 按会话的 `ReauthUntil` 判断）。
- 原生登录：`NativeLoginRequest` 去掉 `client` 字段；App 入口必须带 `X-Client-Platform`（`android`、`ios`、
  `harmonyos`），请求体必须带 `device_class`，缺失时返回 400 `client_platform_invalid`、`device_class_invalid`。
  `Session.client` 的取值随之改变。
- user 模块的建表迁移有改动：`user_sessions` 新增客户端版本、宿主系统、设备形态、是否被顶掉等列，`client` 和
  `users.signup_client` 的取值改变。
- `JWT_SIGNING_KEY` 和 `JWT_ADDITIONAL_KEYS` 里的每把密钥至少 32 字节，没有 kid 的 access token 一律拒绝；
  `docker-compose.yml` 的开发默认密钥随之加长。
- sloglint 的 forbidden-keys 新增 `client_platform`、`client_os`、`client_version`、`installation_id`：这些字段由
  `httpserver` 写入日志上下文，不能手写。

### 新增

- 客户端请求头 `X-Client-Platform`、`X-Client-OS`、`X-Client-Version`、`X-Installation-Id`：`httpserver` 只检查
  格式，值进日志字段；CORS 放行这四个头。约定见 `docs/agents/http.md` 的"客户端请求头"和"客户端约定"。
- 登录会话策略 `domain.SessionPolicy`（`Prepare` 在事务外读数据，`Decide` 在事务内持用户行锁）；自带 `SlotLimit`，
  由 `USER_SESSION_SLOT_LIMIT` 控制每类设备最多保留的会话数，0 为不启用。前台会话列表和后台用户详情展示客户端信息。
- 网关鉴权 `USER_AUTH_MODE=gateway`：OpenResty 的 `gateway_auth.lua` 子请求 api 内部端口的 `/internal/auth/verify`，
  换成 user 服务签发的 ES256 断言（30 秒）；后端用 `httpserver.NewGatewayIdentityMiddleware` 验证。带了断言但验不过、
  校验不可用返回 503 `auth_unavailable`（`errs.KindUnavailable`）。机制见 `docs/agents/auth.md` 的"网关鉴权"。
- api 内部端口 `HTTP_API_INTERNAL_ADDR`：模块经 `RegisterInternalHTTP` 注册，不进 spec，独立限流和 HTTP 指标。
- 配置 `USER_ASSERTION_SIGNING_KEY`、`USER_ASSERTION_SIGNING_KID`（user 服务）、`GATEWAY_ASSERTION_PUBLIC_KEYS`
  （所有验证方）；指标 `gateway_assertion_key_info`、`user_gateway_auth_results_total`。
- `deploy/baremetal/render.sh --internal-port` 渲染内部端口、`/_auth` 子请求和带鉴权字段的访问日志格式；
  `deploy/baremetal/gatewaytest` 在真实 OpenResty 容器里验证渲染结果和 Lua。

### 升级说明

#### 合并前

- 用过 `USER_TRUSTED_IDENTITY_HEADER` 的环境改为 `USER_AUTH_MODE=gateway`：按 `docs/agents/deploy-ci.md` 生成断言
  密钥，配置 `USER_ASSERTION_SIGNING_KEY`、`USER_ASSERTION_SIGNING_KID`、`GATEWAY_ASSERTION_PUBLIC_KEYS`、
  `HTTP_API_INTERNAL_ADDR`，用 `render.sh --internal-port` 重新生成网关配置。没用过网关模式的不用处理。
- 长度不足 32 字节的 `JWT_SIGNING_KEY`、`JWT_ADDITIONAL_KEYS` 换成 `openssl rand -base64 32` 生成的新密钥
  （已登录的用户需要重新登录）。
- 客户端：App 登录带上 `X-Client-Platform` 和 `device_class`，按 401 的 `invalid_token`、`session_replaced` 和
  503 `auth_unavailable` 处理（`docs/agents/http.md` 的"客户端约定"）。

#### 合并后迁移前

- user 模块的建表迁移有改动，已执行过的本地库删卷重建（`docker compose down -v`）。
- 业务代码改到能通过 lint 和测试：处理 `session_unavailable` 的地方删掉，手写 `client_platform` 等日志字段的地方
  删掉。改完跑 `go build ./...`、`go test ./...`、`make lint` 再续跑。

## v0.3.0 — 2026-11-23

可观测性（tracing、统一的进程身份与 HTTP 指标、日志关联字段），以及裸机部署改为 systemd socket activation 的
零排队发布。没有线上数据，所有改动直接到目标形态，不提供兼容层。

### 破坏性变更

- `trace_id` 取代 `request_id`：日志字段、响应头 `X-Trace-Id`、后台操作日志都用同一个值；任务执行是新的 trace，经
  span link 和日志字段 `link_trace_id` 关联入队方。`events.UserDeleted` 删除 `RequestID` 字段。
- 进程身份统一为 `process.Identity`，日志字段和指标标签是 `service_name`、`service_version`、`service_instance_id`；
  删除 `logger.Process`，`logctx` 的字段键不再导出，改用 `WithRoute`、`WithJob` 等函数写入。
- 去掉 echo-prometheus：HTTP 指标改为 `http_server_request_duration_seconds{server, method, route, status}`，
  每个端口一个 Registry、都带 `service_info`。
- sloglint 启用 `context: all`、static-msg、key-naming-case、attr-only、no-global、forbidden-keys：日志一律调用
  `*Context` 方法、字段写成 `slog.Attr`，不用 slog 的全局函数，不手写关联字段。
- `TracerProvider` 必填（`httpserver.Deps.Tracer`、`queue.NewClient`）。
- `HTTP_API_DOCS_ENABLED` 默认改为 `false`。
- `httpserver.Component(name, e, ln, stop)` 改为接收已绑定的 `net.Listener` 和 `httpserver.Stop`（用
  `httpserver.Listen` 绑定）；`httpserver.NewInternal` 接收 Registry。
- river 的表改由 river 自己的迁移器 `rivermigrate` 执行（迁移来源 `river`，版本记在 `river_migration`），删除导出的
  SQL 文件和 `schema_migrations_platform`；Postgres 16 起都能迁移。`migrate.Source` 新增 `Migrator`，
  `api migrate force` 对 river 不适用。
- `deploy/baremetal/` 换成 socket activation 的写法：`<项目>-api.socket` 由实例 `<项目>-api@1`、`@2` 共享，发布时
  轮流接管，排队的连接不会被拒绝；模板由 `render.sh` 按项目渲染，一台机器可以部署多个项目。

### 新增

- OpenTelemetry tracing：HTTP、SQL（otelpgx）、Redis（redisotel）、队列（otelriver）、微信调用建 span，
  `OTEL_EXPORTER_OTLP_ENDPOINT` 为空时不导出。
- `HTTP_SHUTDOWN_DRAIN` 排空期：响应带 `Connection: close`，`/readyz` 返回 503。
- `HTTP_SOCKET_ACTIVATION`：从 systemd 继承监听 socket，依赖检查通过后发 `READY=1`；共享 socket 时收到停止信号立即
  不再 accept。`INSTANCE_ID` 配置实例标识。
- 错误日志按状态码分级，panic 堆栈放进独立的 `stack` 字段；输出接到 journald 时每行带 syslog 优先级前缀
  （systemd 219 没有 `JOURNAL_STREAM` 时按 stdout 的对端识别）。
- `deploy/baremetal/app-cmd`：以服务的用户和环境执行 `api` 子命令。

### 升级说明

#### 合并前

- 日志检索、告警和看板：`request_id` 改为 `trace_id`，`service`/`version`/`instance` 改为 `service_name`/
  `service_version`/`service_instance_id`，HTTP 指标改为 `http_server_request_duration_seconds`。
- 裸机部署按 `docs/agents/deploy-ci.md` 的"裸机发布"用 `deploy/baremetal/render.sh` 重新生成单元、网关和 Alloy
  配置，替换旧的 `app-*` 单元。

#### 合并后迁移前

- 业务模块的代码改到能通过 lint 和测试：日志调用改用 `*Context` 方法和 `slog.Attr`；生成过的 `httpapi`、
  `adminapi` 测试给 `httpserver.Deps` 传 `Tracer`（参考 starter 模块的同名测试）。改完跑 `go build ./...`、
  `go test ./...`、`make lint` 再续跑。
- river 的迁移来源换了版本表，已执行过旧 `schema_migrations_platform` 的本地库删卷重建（`docker compose down -v`）。

## v0.2.0 — 2026-11-20

架构整理：模块名与职责一致、同一事实只声明一处、第三方登录的通用链路接通。没有线上数据，所有改动直接到目标形态，
不提供兼容层。

### 破坏性变更

- 前台用户模块 `auth` 改名为 `user`：目录 `internal/modules/user/`、包 `user`、组合根 `userM` / `Modules.User` /
  `moduleConfigs.User`；配置段 `user`、环境变量 `AUTH_*` → `USER_*`（`JWT_*`、`WECHAT_*` 不变）；迁移源
  `user`（版本表 `schema_migrations_user`）；定时任务 kind `user_cleanup`；日志前缀 `user:`；前台 spec tag
  `user`。HTTP 路径 `/v1/auth/*` 不变。
- `user.UserDataCleaner` → `user.DataCleaner`，`AddUserDataCleaner` → `AddDataCleaner`。
- 模块 `RegisterJobs(*queue.Workers)`，用 `queue.AddWorker` 注册；删除各模块的 `JobKinds()` 与组合根的
  `jobKindRegistrars`。`api queue cancel-orphans` 需要完整配置。
- `module_configs.go` 改为每个模块一行 `loadInto(&c.<M>, <m>.LoadConfig, src)`，`Redacted` 合并全部模块。
- `oauth.Provider` / `oauth.Native` 接口改为 `Capabilities()` + 参数结构体（`AuthorizeParams`、`ExchangeParams`、
  `NativeCredential`）；`oauth.Entry` 去掉未使用的 `LoginProvider`、`ClientID`；`LoginProviderWeChat` →
  `ProviderWeChat`。
- 原生登录请求的 `code` 改为可选。
- `HTTP_GLOBAL_RATE_LIMIT_PER_SECOND`、`HTTP_REQUEST_TIMEOUT` 必须为正数，否则启动失败。
- `starter-owned.txt` 不再列 starter 模块的前端路径，改由模块 `module.yaml` 的 `web_paths` 并入。
- starter 的发版说明从 `CHANGELOG.md` 改名为 `starter-changelog.md`，根目录 `CHANGELOG.md` 留给业务项目。
- 网关信任 header 模式下，依赖会话的操作（† 操作、绑定、重新验证）返回 403 `session_unavailable`，不再返回
  `recent_auth_required`。
- `return_to` 校验更严：整串不能有反斜杠和控制字符，解析后不能有 scheme 和 host。
- 漂移检查只报"相对基线改过、且和目标版本也不一样"的 starter 文件，提前同步过上游修复的文件不再误报。
- 升级基线只取同时是 HEAD 和目标版本祖先的 tag（项目自己的 tag 不会被当成基线）；基线不是版本号时显示目标版本
  及以前的全部升级说明。
- 删除 `scripts/starter-paths.sh` 和 `tools/starter` 的 `paths`、`own` 子命令；`gen fixture` 存档名改为
  `fixture_<资源>.yaml`。

### 新增

- 第三方登录通用链路：PKCE、浏览器与原生 nonce（`POST /v1/auth/native/{provider}/nonce`）、表单 POST 回调
  （`POST /v1/auth/oauth/{provider}/callback`）、原生请求的 `id_token` / `nonce` / `name`。
- `USER_TRUSTED_IDENTITY_HEADER`：配置后前台鉴权信任网关透传的用户 ID。
- `go run ./tools/gen revert <描述文件>`：按生成前的原样精确回退，`gen apply` 不再要求工作区干净；生成之后共享
  文件又被改过时拒绝回退并列出文件。
- `make actionlint`（lefthook 与 CI）；`scripts/check-docs.sh` 检查 Markdown 链接，禁止已提交文档引用本地提案。
- 后台前端会话契约 `src/lib/session.ts`；oxlint 禁止 Feature 之间深入引用、通用目录引用 Feature。

### 升级说明

#### 合并前

- `starter-removed.yaml` 里有 `auth` 的改成 `user`（否则重放会因为目标版本没有 auth 模块而报错）。
- 部署配置里的 `AUTH_*` 环境变量改名为 `USER_*`。

#### 合并后迁移前

- 业务模块的代码只有合并后才能改到能编译：引用 `internal/modules/auth`、`auth.`、`authM`、`AUTH_*` 的地方改成
  `user`；`RegisterJobs` 改为接收 `*queue.Workers`、用 `queue.AddWorker`，删除 `JobKinds()`。改完跑
  `go build ./...`、`go test ./...` 再续跑。
- 后台前端：`useMe`、`usePermissions`、`useCan`、`useLogout`、`meQuery`、`permissionsQuery` 从
  `@/features/auth/queries` 移到 `@/lib/session`，路由守卫 `page`、`loadMe` 从 `@/features/auth/guard` 移到
  `@/lib/guard`，改 import；Feature 之间只能经对方的公开出口
  `@/features/<名字>`（`index.ts`）引用，深入引用 `@/features/<名字>/<文件>` 会被 oxlint 报出。改完在
  `web/admin` 跑 `pnpm typecheck && pnpm lint`。
- `internal/app/modules.go`、`migrate.go`、`module_configs.go`、`api/openapi.yaml` 是项目所有的文件，冲突处按
  上面的命名改：`auth`/`authM`/`Auth` → `user`/`userM`/`User`，删掉 `jobKindRegistrars`，`module_configs.go`
  照 starter 的新写法每个模块一行；openapi.yaml 只改 `tags: [auth]` 和 tag 定义，保留业务新增的内容。
- 没有线上数据时直接重建本地库：迁移源改名后版本表是新的 `schema_migrations_user`。

#### 部署后

- 所有实例升级后执行 `api queue cancel-orphans`，收口旧 kind `auth_cleanup` 的待处理任务。

## v0.1.0 — 2026-11-20

首个支持业务项目裁剪与升级的版本。

### 升级说明

#### 合并前

- 阅读 `docs/agents/project.md` 和 `docs/agents/upgrade.md`，把业务特有内容移到
  `AGENTS.project.md`、`docs/PROJECT.md` 和 `project/`。
- 如果业务项目改过 starter 所有的文件，先按 `project/starter-patches.md` 登记并确认。

#### 合并后迁移前

- 运行固定入口完成合并、生成和检查；迁移已按模块独立编号，不需要转换旧版本表。

#### 迁移后

- 运行 `go test ./...` 和 `make check-modules` 验证保留的模块。

#### 部署后

- 如果删除了模块，所有实例升级后执行 `api queue cancel-orphans`。
