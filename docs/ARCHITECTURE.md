# go-starter 架构概览

事实来源：`internal/app/modules.go`、`internal/archtest/boundaries_test.go`、`internal/platform/`、`internal/kit/`

第一次了解这个项目时读这篇：它回答"代码是怎么组织的、依赖往哪个方向走、哪些能力已经
实现"。要改某一类代码时，读 [`AGENTS.md`](../AGENTS.md) 链接的 `docs/agents/` 主题文档；
分层、模块和运行时的示意图在 `docs/diagrams/`（浏览器打开），画法约定见
[`docs/agents/diagrams.md`](agents/diagrams.md)。

## 目标

生产级 Go 后端 starter：把鉴权、缓存、可观测性、部署、多实例 worker 协调这类几乎每个
新后端服务第一周都要回答一次的问题提前决定好，并给出最小可用的实现。判断要不要放进来
有两条标准：是不是几乎每个服务都要回答；放进来之后是不是仍然只需要改几行配置或照抄
一个例子。

## 设计原则

| 原则 | 含义 | 落点 |
|---|---|---|
| 边界清晰 | 一个功能的代码集中在一个模块目录；模块之间单向依赖 | `internal/modules/<m>/`、依赖方向 |
| 约定尽可能机器校验 | 依赖、归属、生成一致性等客观规则由工具检查，报错写明正确做法；语义复用、适度抽象等判断由评审和 Agent 自检覆盖 | `internal/archtest`、`internal/app` 的注册测试、`scripts/check-*.sh` |
| 文档按需可取 | 每次只读一份短索引，按任务链接到主题文档；文档描述现状 | `AGENTS.md`、`docs/agents/`、`scripts/check-docs.sh` |
| 显式优于隐式 | 依赖关系可以靠"跳转到定义"追到底：不用反射、`init()` 注册、`context.Value` 传业务依赖、按字符串运行时查找 | `internal/app/modules.go`、构造函数注入 |
| 能力和启用分开 | starter 把同一类的可插拔能力做全（登录入口、第三方集成等），每个项目按真实业务只启用用到的；启用由配置决定，没配就不启用 | `internal/config` 的启动校验、`docs/agents/deploy-ci.md` 的"配置" |

局部性的价值是检索精度（一个关键词只落在一个模块里，改动影响范围可预测），不是节省上下文。
检查越多，越要防止"让检查通过"代替"让代码正确"，所以规则本身也受保护（见
[`docs/agents/workflow.md`](agents/workflow.md)）。

### 长期演化与项目防腐

这些约束适用于每次模块、组合根、通用工具和规范的设计，不只属于某一份提案。目标不是消除复杂度或
追求机制代码最少，而是把分散、隐式、重复的复杂度改成集中、显式、可测试的复杂度。评价设计时既看
第一次实现，也看模块和升级次数增加后变化落在哪里。

- **边界先行**：顺序是模块边界 → 文件与数据归属 → 公开契约 → 通用工具 → 检查机制。检查只能证明
  边界未被破坏，不能代替边界设计；发现两方需要频繁修改同一文件时，先调整边界或扩展点；
- **事实唯一，声明优先**：归属、模块能力和依赖只有一份声明，工具从中推导。新增模块应只增加声明和
  组合根的固定接线，不要求通用工具增加该模块的专用分支。需要 `if/switch`、专用删除规则或升级例外时，
  先重新审视边界；
- **检查不制造第二份事实**：归属和能力检查从声明与实际代码推导，不用可独立填写、会与代码漂移的
  豁免标志代替事实；
- **只表达真实状态**：流程和状态机覆盖实际存在的阶段，不为假设中的未来需求预建通用框架；
- **共享最小化**：来源文件明确唯一修改方，双方都修改的才共享，派生文件从来源重新生成。

项目防腐包括两层：

1. **文件边界**：上游 starter 与业务项目各有明确的修改范围，starter 升级不能覆盖项目所有的内容；
   项目需要的能力优先经配置和公开扩展点接入，本地修改上游文件只能是登记在案、等待上游吸收的临时措施；
2. **语义边界**：业务 domain 只定义自己的模型和窄端口，跨模块能力由 `internal/app` 适配器连接并翻译
   模型和错误。业务 domain 不依赖对方的 Repository、数据库表、HTTP 生成类型或内部实现；公开事件和
   扩展点是稳定契约，内部重构不应要求业务模块跟着修改。

防腐不等于给所有调用再包一层：模块端口和组合根适配器已经是边界，没有模型或错误语义差异的简单调用
不新增转发层。每次架构改动都要问：同类新模块能否只加声明完成接入、删除和升级；是否新增共享文件、
重复事实或模块专用分支；内部模型和错误是否泄入业务 domain。答案不利于长期演化时先改边界，不先加
补丁式检查。

### Go 模块与数据边界

- 非公开实现放在 `internal/`，但 `internal/` 只限制仓库外导入，模块间边界仍由 `archtest` 保证；
- 模块是围绕业务能力组织的垂直切片。组合根是唯一知道完整模块清单并连接模块的地方，platform 不含
  业务模块特例；
- 接口由消费方按需定义，只包含真正使用的方法。跨模块同步调用使用消费方端口和组合根适配器，不在业务
  包内实例化其他模块的具体实现；
- `httpapi`、`adminapi`、`jobs` 调用本模块 `domain.Service`；`postgres` 实现 domain 定义的 Repository
  端口；`module.go` 显式构造并注入具体实现。domain 不 import 具体 postgres 实现；
- 每张业务表只有一个所属模块，其他模块不得直接读写、Join 或建立跨模块外键。多步写操作有明确的
  `UnitOfWork` 边界，事件与业务写入在同一事务入队；
- 数据库存储结构不得绕过 OpenAPI 直接成为 HTTP 契约。请求和响应以 spec 为准，由生成类型和 handler
  完成边界转换；不强制增加 PO/DO 双层模型，只有存储结构与领域语义确实不同才增加转换；
- 使用显式构造函数、`Deps` 和 `Config` 表达依赖，必需依赖不能藏进可选参数。Functional Options 只适合
  少量真正可选的配置，不用于掩盖依赖过多或规避必要的签名变化；
- 优先组合，谨慎使用结构体嵌入，不因嵌入意外扩大模块的公开方法面。

### React Feature、状态与契约边界

- 业务代码按 `web/admin/src/features/<feature>/` 和对应路由组织，页面、查询、表单和测试随 Feature 内聚；
- Feature 之间不深入对方内部目录引用组件、Hooks 或状态。Feature 确实需要对外提供能力时才定义明确的
  公开出口，不强制每个 Feature 都建立 `index.ts` 桶文件；`routes/` 是页面组合点，可以引用 Feature 的
  页面和路由所需查询，不属于跨 Feature 的业务复用；
- 跨 Feature 复用先判断是否具有稳定的通用语义。“出现三次”是检查重复的信号，不是机械门槛；
  `components/`、`lib/` 等通用目录不得反向 import `features/`；
- 基础 UI 组件只负责展示和交互事件，不直接发起业务 API 请求或读取业务全局状态。Feature 组件可以承担
  页面组合和少量局部交互，不为了形式把所有逻辑都抽成 Hook；
- 数据查询、缓存失效和复杂异步流程集中在 Feature 的查询模块或 Custom Hooks 中。表单校验和局部状态靠近
  使用位置，只有真实复用或复杂度需要时才提取；不按机械行数制造细碎组件；
- 服务端状态由 TanStack Query 管理，URL 表达可导航、可分享的界面状态，局部状态就近使用 `useState`，
  只有跨路由、跨页面且确属客户端会话的状态才考虑全局 Store；
- TypeScript 类型从 OpenAPI 生成，不手工维护重复 DTO。不用 `any` 绕过检查，外部输入优先用 `unknown`
  加运行时校验，类型断言必须能说明依据；
- 兼容性按操作语义判断，不是新增字段一律可选：请求新增必需字段通常破坏兼容；响应新增字段要考虑
  客户端处理；删除、重命名、改变含义、收紧校验、修改权限和错误码都要显式评估。

### AI 持续友好

- 稳定目录、明确命名和唯一事实来源优先于隐式约定，让 Agent 能定位完整修改范围；
- 文档描述当前实现，规划只放在提案中。代码变化后同步更新相关文档和架构图；
- 架构规则尽可能由编译器、架构测试或生成一致性检查执行，报错给出正确修改方向。检查失败时修代码，
  不改期望值、增加豁免或跳过检查来掩盖问题；
- 归属和能力检查从声明及实际代码推导，不维护另一份容易漂移的手工豁免清单；
- 每次改动同时考虑接入、测试、删除和升级，而不只考虑功能能否运行；
- 不为假想需求预先建立插件系统、通用事件总线或流程框架。防腐不等于增加转发层，没有模型、状态或
  错误语义差异时不增加无意义包装。

### Agent 编写代码前自检

1. **归属**：代码是否有唯一所属模块？是否把业务特例写进了 platform 或通用工具？
2. **Go 依赖**：是否跨模块直接 import？接口是否由消费方定义？domain 是否接触具体存储实现？
3. **数据边界**：是否直接读写其他模块的表？是否让存储结构绕过 OpenAPI 成为 HTTP 契约？
4. **事务与事件**：多步写入是否有事务边界？事件是否同事务入队并保持契约稳定？
5. **React 边界**：是否深入引用其他 Feature 的私有实现？通用目录是否反向依赖 Feature？
6. **状态位置**：服务端状态是否交给 TanStack Query？局部状态是否被不必要地全局化？
7. **类型安全**：是否使用 `any`、无依据的断言或手工复制 DTO 逃避契约检查？
8. **长期演化**：同类模块能否通过声明接入和删除？是否新增模块专用分支或第二份归属事实？
9. **适度抽象**：是否为了假想需求引入 Options、Hook、公共组件或通用框架？是否多包了一层？
10. **验证与文档**：是否运行覆盖本次变化的检查，并同步更新受影响的文档、生成代码和测试？

## 依赖方向

```
cmd ──> app ──> modules/*/module.go ──> modules/*/{httpapi,postgres,jobs}
                                               │           │
                                               ▼           ▼
                                      modules/*/domain   platform/*
                                               │           │
                                               ▼           ▼
                                                  kit/*（仅标准库）
```

由 `internal/archtest` 从包路径推导并强制，新增/删除模块不需要改任何配置：

1. `modules/*/domain` 只依赖标准库、`kit` 和本模块 `events`；
2. 模块之间只经组合根 `internal/app` 粘合，唯一例外是导入其它模块的 `events`；
3. `platform` 不认识任何模块；
4. `kit` 只依赖标准库；`events` 只依赖标准库；
5. 测试替身包只能在测试里导入；
6. 模块只读写自己在 `postgres/tables.go` 登记的表，模块的表之间没有外键（为以后按模块
   拆库留住边界，拆分前置条件见 [`docs/agents/modules.md`](agents/modules.md)）。

## 目录速查

```
cmd/api, cmd/worker        进程入口：加载配置 → app.RunAPI / app.RunWorker
internal/
  app/                     组合根：infra.go、modules.go、adapters_*.go、api.go、worker.go
  kit/                     只依赖标准库的通用件
    errs/                  带 Kind 的业务错误，Kind 决定 HTTP 状态码
    logctx/                ctx 里的 logger 和 request_id
    uow/                   UnitOfWork 契约（uowtest 是测试替身）
    lifecycle/             进程内组件的统一启停
  platform/                与业务无关的基础设施
    postgres/              连接池、cursor 编解码（pgtest：集成测试用的容器化 Postgres）
    dbtx/                  UnitOfWork 实现：Resolve/ResolveTx、嵌套 SAVEPOINT
    queue/                 river client、事务内入队的 Inserter、worker 组件
    cache/                 RedisCache[T] / NoopCache[T]
    redis/                 Redis 客户端与探活
    ratelimit/             Echo 限流的计数存储（Redis GCRA / 内存）
    crypto/                Argon2id、JWT 签发与校验
    oauth/                 第三方登录：浏览器 OAuth 流程与原生 code 换身份（目前只有微信）
    httpserver/            Echo 构造（前台、后台两个 server）、中间件链、错误渲染、健康检查、API 文档
    logger/                slog 构造
    healthcheck/           二进制自带的探活子命令
    cli/                   api 子命令的查找与执行（命令由模块提供）
  modules/                 业务模块，每个目录自带 domain/postgres/httpapi[/events/jobs]、
                           migrations + module.go
    auth/                  前台用户：登录（微信四个入口）、会话、重新验证、注销（发布
                           user_deleted 事件），后台的用户管理，提供前台鉴权中间件
    staff/                 后台员工：账号、会话、角色权限、操作日志，提供后台鉴权
  archtest/                依赖方向的架构测试
  config/                  配置加载
api/openapi.yaml           前台接口唯一真相源，operation 用 tag 归属到模块
api/admin.yaml             后台接口唯一真相源，每个操作带 x-permission
web/admin/                 后台前端（独立的 pnpm 项目），构建产物经 go:embed 打进 api 二进制
scripts/                   check-docs.sh、check-versions.sh、check-module-removal.sh
tools/gen/                 生成器（开发工具，不进运行时）：新资源的各层代码、前端的 meta.gen.ts
deploy/                    Dockerfile、docker-compose.yml
```

## 一个请求和一个事件怎么走

前台请求都经过 `httpserver` 的全局中间件链、按 tag 生成的模块路由和模块 Service；
需要异步处理时，业务写入与 river 入队在同一个事务提交，worker 消费方按事件里的 request_id
重建日志上下文。当前项目的具体模块清单与下单链路见 `docs/PROJECT.md`。

## 技术选型

Go 1.27、Echo v5、pgx/v5 + Postgres、golang-migrate、riverqueue/river、oapi-codegen
（spec-first，按 tag 分模块生成）、go-playground/validator、go-redis、slog、Prometheus、
testcontainers-go。显式构造函数注入，不用 wire/fx。

几处取舍：

- 数据库直接用 pgx 的原生接口（`pgxpool`、`pgx.Tx`），不用 ORM：反射映射和按约定触发的 hook 在编译期
  看不出来，ORM 自带的事务 API 也接不进"Repository 经 `Resolve` 取连接"的机制
  （见 [`docs/agents/transactions.md`](agents/transactions.md)）；
- 队列用 river，不手写 outbox：事务内入队、指数退避重试、唯一性、定时任务和多实例调度都是现成的，手写
  就要自己保证领取、租约、死信的正确性；
- 接口 spec-first（oapi-codegen）：spec 是唯一真相源，路由和 spec 的一致性由测试检查，不从代码注释生成
  spec；
- 配置以环境变量为主，`CONFIG_FILE` 可选 YAML 覆盖非敏感默认值，不用 Viper：不需要多格式、远程配置中心；
- 显式构造函数注入，不用 wire/fx：依赖关系靠"跳转到定义"就能追到底。

## 已实现 / 规划中

| 能力 | 状态 |
|---|---|
| 模块化结构、架构测试、注册完整性测试、删除演练 | 已实现 |
| 前台会话鉴权中间件（JWT 验签 + 按 sid 查会话）/ 网关信任 header、Argon2id（后台员工密码） | 已实现 |
| 前台登录与会话：微信四个入口、服务端会话、refresh 轮换、重新验证、绑定/解绑、注销 | 已实现；手机号、邮箱、其它第三方登录规划中（[提案 0004](proposals/0004-多方式登录.md)） |
| 商品缓存（Redis / Noop） | 已实现；singleflight 防击穿未实现，有热点时再加 |
| 全局限流（Redis 共享 / 内存） | 已实现 |
| river 队列、事务内入队、幂等消费、永久失败收口 | 已实现 |
| 后台接入面（独立 server、`api/admin.yaml`、`adminapi/` 约定） | 已实现 |
| 后台员工：账号、服务端会话、登录退避、角色与权限码、操作日志、CLI 创建管理员 | 已实现（`staff` 模块） |
| 后台前端：登录、强制改密、员工、角色、操作日志、个人设置，嵌入二进制 | 已实现（`web/admin/`）；TOTP、文件上传、任务面板等按需扩展见 [提案 0003](proposals/0003-后台按需扩展.md) |
| 定时任务、模块子命令 | 已实现（模块的 `PeriodicJobs()` / `Commands()`），staff 是第一个使用者 |
| 集成测试（Repository 契约、dbtx、入队） | 已实现，需要 Docker |
| 日志、request_id 跨进程传播、Prometheus 指标、健康检查 | 已实现；OpenTelemetry tracing 未接入 |
| Docker 镜像、compose、CI、提交钩子、dependabot | 已实现；镜像漏洞扫描（trivy）未接入 |
| 手写 outbox | 不实现，river 已覆盖（见"技术选型"） |
| 外部消息队列 relay、跨服务 HTTP 调用封装、熔断 | 不预先实现，出现真实需求时再加 |

## 明确不放进来的

具体云厂商 SDK、LLM provider、通用 RBAC 框架（Casbin 这类）、多租户、前台前端。它们是
"需要时自己加"的扩展点，不是每个服务都要回答的问题。后台管理是例外：几乎每个业务服务都
需要一个运营后台，所以提供后台接入面、员工账号与权限码、后台前端。前台登录也内置了微信登录，
但只用 `net/http` 调微信接口，不引入 SDK。
