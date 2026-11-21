# 可观测性：日志、request_id、指标、健康检查

事实来源：`internal/platform/logger/`、`internal/kit/logctx/`、`internal/platform/buildinfo/`、`internal/platform/queue/logfields.go`、`internal/platform/httpserver/identity.go`、`internal/platform/httpserver/health.go`、`internal/platform/healthcheck/`、`internal/platform/httpserver/server.go`

## 日志

### 输出与传播

- `log/slog` JSON 格式，写 stdout，构造在 `internal/platform/logger`。进程不写日志文件、不做轮转，
  收集交给运行环境（容器运行时、journald、日志采集器）；多个实例不要共写同一个文件；
- 关联字段存在 ctx 里（`internal/kit/logctx`），**只存值**；进程的根 logger（`logger.New`）的 Handler 在输出
  每条记录时从 ctx 取出写入。所以不论用哪个 logger（`logctx.From`、`slog.Default()`、Echo 的
  `c.Logger()`、river 的 logger、第三方库经 `slog.New(h)` 重建的），只要调用带 ctx 的方法，字段都在。
  调用方已经显式写了同名字段时以调用方为准，不输出重复的键；
- 接收外部 logger 的平台组件（HTTP server、river client）都经 `logger.Ensure` 取用：不是 `logger.New` 构造的
  logger 会被包一层，传错 logger 也不丢字段。`slog.SetDefault` 在 `cmd/*/main.go` 里设为根 logger；
- `context.Value` 只放横切信息（这里的 logger 和关联字段，以及 `internal/platform/dbtx` 的事务句柄、
  `internal/kit/uow` 的提交后回调），不传业务依赖。

### 每条日志都带的字段

| 字段 | 含义 | 写入方 |
|---|---|---|
| `service`、`version`、`instance` | 哪个进程（`api`/`worker`）、哪个构建、哪个实例 | 根 logger 的静态字段（`logger.Process`）；`version` 见下，`instance` 是配置 `INSTANCE_ID`，默认主机名 |
| `request_id` | 串起网关 access log、api、worker 的同一次请求 | HTTP：RequestID 中间件（请求头 `X-Request-Id`，网关传入即沿用）；任务：队列中间件 |
| `method`、`route` | 属于哪个接口；`route` 是路由模板，没匹配上路由时不输出 | RequestID 中间件 |
| `user_id` / `staff_id` | 前台用户 / 后台员工 | `httpserver.SetUserSession` / `SetStaffSession`：两种鉴权都只经它们写入身份 |
| `job_kind`、`job_id`、`attempt` | 哪个任务、第几次执行；`job_kind` 与 river 自己日志里的键相同 | 队列执行中间件 |

这些字段由入口写入，**业务代码不手写**（`logctx` 的 `Key*` 常量是全部键名）。没有请求的路径（启动、
定时任务的调度循环）只有静态字段。

- `version`：构建时用 `-ldflags "-X app.local/internal/platform/buildinfo.version=..."` 注入
  （`deploy/Dockerfile` 的 `VERSION` 构建参数，镜像构建不传时是 `dev`）；没注入时取 Go 嵌入的 VCS 提交前 12 位
  （本地在仓库里 `go build` 时有，有未提交改动加 `-dirty`），都没有时是 `dev`；
- 同一台机器上跑同一服务的多个实例时，必须分别设置 `INSTANCE_ID`，否则 `instance` 相同；
- 跨进程传播：入队时队列中间件把 `logctx.RequestID(ctx)` 写进任务 metadata 的 `request_id`，执行时从
  metadata 读回；事件不声明 request_id 字段；
- 不用 `slog` 的 `WithGroup`：分组之后，关联字段会落进分组里，按顶层字段检索不到。
- **脱敏**：不把密码、token、`Authorization` header 整体写进日志，只记 user_id、长度
  这类不构成凭据的信息；
- 启动时打印一份配置快照：`config.Redacted()` 是白名单，只列出其中写明的字段。新增配置项
  要出现在启动日志里，就在 `Redacted()` 里手动加一行；密钥类字段写 `"***"`（或
  `redactedIfSet`），或者干脆不加。

### 记什么、不记什么

日志的职能是**全量记录离散事件**，并且能按请求、按人、按对象找到它；量、速率、耗时分布是指标的职能。

- 记：错误（带完整错误链）、安全事件（凭据重放、权限拒绝等）、状态变化、降级与兜底、批处理和定时任务的结果；
- 不记：耗时（耗时分布看指标，单个请求的耗时看网关 access log），正常路径的每一步、每条 SQL、每个请求的访问记录；
- 日志不采样、不丢弃：排查错误和安全事件必须只靠日志就能完成，所以错误的完整信息写在日志里；
- 事件专属的内容放字段：涉及的实体 ID（`<实体>_id`）、计数、结果，便于按对象检索。

### 写法

- 取 logger 用 `logctx.From(ctx)`，调用带 ctx 的方法（`InfoContext`、`WarnContext`、`ErrorContext`）：关联字段只在 ctx 里，
  不带 ctx 的 `Info`/`Error` 会丢掉它们。golangci-lint 的 `sloglint`（`context: all`）强制这一点，启动、关停这类
  没有请求的路径也传进程的 ctx；
- `msg` 是固定的英文短语，形如 `"<模块或包>: <发生了什么>"`（`"user: user_deleted processed"`、
  `"queue: ..."`），**变量一律放字段，不拼进 msg**：同一类事件的 msg 完全相同，才能按 msg 聚合、
  计数和精确检索；
- 字段名用 snake_case，同一个含义全项目只用一个名字：上表的字段由入口写入，不手写；事件里常用的有
  `error`（直接传 error 值）、`panic`（recover 到的值）、`stack`（panic 所在 goroutine 的堆栈，单独成字段，不拼进
  `error`）、实体 ID（`<实体>_id`）；新增常用字段时先搜一遍已有叫法；
- 一个错误只记一次：能返回就返回，由处理它的那一层记录。HTTP 请求返回的错误由错误处理器统一记录
  （`"http request failed"`，带 `status`、`code`、`error`，业务错误另带 `error_kind`，panic 另带 `stack`；级别按状态码，
  见下面"级别"），handler 和 Service 不再重复记；
  worker 返回的错误和 panic 由队列执行中间件用任务 ctx 记录（带 `request_id`、`attempt`：river 自己的
  `Job errored`/`Job panicked` 日志带不上它们）——还会重试记 Warn（`"queue: job failed, will retry"`），
  最后一次记 Error（`"queue: job failed, no retries left"`），`river.JobCancel` 记 Warn，`river.JobSnooze` 不记；
  river 另把错误写进任务的 `errors` 列并按策略重试（查 `river_job` 表），worker 不再重复记；只有吞掉错误继续执行（降级、尽力而为的副作用）时才在原地记 Warn；
- 不在每个请求上打 Info：应用不记访问日志，请求量、耗时、状态码分布看指标，单个请求看网关的访问日志
  （按 `request_id` 关联）。

### 级别

| 级别 | 用于 | 例子 |
|---|---|---|
| Error | 需要人处理：数据可能不一致、依赖不可用、panic、未预期的 500 | after-commit 钩子 panic、关闭依赖失败 |
| Warn | 异常但已自动处理或降级；安全相关事件 | refresh token 重放被吊销会话、可选依赖未配置而降级 |
| Info | 低频的状态变化和批处理结果 | 启动、监听端口、定时清理的删除条数 |
| Debug | 排查细节，生产默认不开（`LOG_LEVEL`） | — |

HTTP 错误处理器按状态码定级别（`internal/platform/httpserver/errors.go` 的 `levelOf`）：5xx 记 Error；401、403、429
可能是攻击迹象，记 Warn；其余 4xx 是客户端的问题，记 Info。Error 级别因此只剩服务端故障，可以直接告警。

### 排查

日志是一行一个 JSON 对象，用 jq 过滤。在 systemd 下运行时由 journald 收集，`-u` 支持通配，
多个实例按时间合并输出：

```bash
journalctl -u '<服务名>@*' -f                                     # 跟踪全部实例
journalctl -u '<服务名>@*' -o cat --since -1h | jq -c 'select(.request_id=="<id>")'
journalctl -u '<服务名>@*' -p err --since -1h -o cat            # 只看 Error（-p warning 含 Warn）
journalctl -u '<服务名>@*' -g '"user_id":"<id>"'                   # -g 按正则匹配日志内容
journalctl -u '<服务名>@*' -o json | jq -r '[._SYSTEMD_UNIT, .MESSAGE] | @tsv'   # 带上来自哪个实例
```

`-p` 能用，是因为 `logger.New` 检测到输出就是 systemd 接到 journald 的那条流（`JOURNAL_STREAM` 与 stdout 的
设备号:inode 相同）时，在每行前加 syslog 优先级前缀（Error `<3>`、Warn `<4>`、Info `<6>`、Debug `<7>`），journald 据此
设置优先级并去掉前缀，`-o cat` 看到的仍是纯 JSON。终端、容器和被重定向的输出不加前缀。

## 指标

- api 进程：`echoprometheus` 中间件 + `GET /metrics`，用独立的 Registry；后台 server
  （配置了 `HTTP_ADMIN_ADDR` 时）在自己的端口上另有 `/metrics`，subsystem 是
  `echo_starter_kit_admin`；
- worker 进程：内部端口（`HTTP_INTERNAL_ADDR`，默认 `:9090`）上的 `/metrics`；
- 告警规则不在这个仓库里，由使用方在自己的监控平台配置。

### 路径一律用路由模板

- HTTP 指标的 `url` 标签是路由模板（`c.Path()`，如 `/v1/me`），按模板聚合：
  `sum(rate(<subsystem>_requests_total{method="GET", url="/v1/me"}[5m]))`。没匹配上路由的
  404 共用一个标签值（`DoNotUseRequestPathFor404`），扫描器的乱扫路径不会各占一条时间序列；
  `server_test.go` 的 `TestServer_MetricsURLLabelIsRouteTemplate` 固定了这一点；
- 自定义指标同样不把 ID、查询参数、用户输入放进标签。"某个具体对象被访问了多少次"查日志，不进指标。

## 健康检查

- `/healthz`：liveness，恒定 200，不查依赖。api、worker 和后台 server 都有；
- `/readyz`：readiness，逐个 Ping 依赖（数据库，配置了 Redis 时还有 Redis），任一失败
  返回 503。只有 api 的前台端口有；worker 没有"要不要把流量路由过来"这个概念，后台
  server 和前台在同一个进程里，不单独挂；
- 依赖列表在 `internal/app/infra.go` 的 `Pingers`；
- distroless 镜像里没有 curl，Dockerfile 的 HEALTHCHECK 用二进制自己的 `healthcheck`
  子命令（`internal/platform/healthcheck`）。纯 docker/compose 判定 unhealthy 后不会
  自动重启容器。
