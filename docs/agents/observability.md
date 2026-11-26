# 可观测性：命名约定、日志、指标、tracing、健康检查

事实来源：`internal/platform/tracing/`、`internal/platform/httpserver/trace.go`、`internal/platform/oauth/wechat.go`、`internal/platform/process/`、`internal/platform/metrics/`、`internal/platform/httpserver/metrics.go`、`internal/platform/logger/`、`internal/kit/logctx/`、`internal/platform/buildinfo/`、`internal/platform/queue/logfields.go`、`internal/platform/httpserver/identity.go`、`internal/platform/httpserver/health.go`、`internal/platform/healthcheck/`、`internal/platform/httpserver/server.go`、`internal/app/listeners.go`

## 命名约定：日志、指标用同一套名字

日志进 Loki、指标进 Prometheus 之后，靠**同名同值**的字段在 Grafana 里互相跳转、互相过滤。规则：

1. **进程身份只有一个来源**：`process.Identity`（`process.New(服务名, INSTANCE_ID)`，版本取 `buildinfo.Version`），
   由组合根的 `newProcess`（`internal/app/infra.go`）构造，同时构造根 logger，挂在 `Infra.Identity`。日志的静态字段（`Identity.LogAttrs`）和指标 `service_info` 的标签（`Identity.Labels`）
   都从它取，不在别处另写服务名；
2. **身份字段用 OpenTelemetry 资源属性的名字，点换成下划线**：`service_name`、`service_version`、`service_instance_id`
   （`process` 包里不导出的常量）。Loki 接收 OTLP 时用的就是这组名字，Grafana 默认按 `service_name` 关联；
   **不用 `instance`**：Prometheus 的 `instance` 标签是抓取地址 `host:port`，含义不同；
3. **同一含义在日志字段和指标标签里同名**：

   | 含义 | 日志字段 | 指标标签 |
   |---|---|---|
   | 服务、版本、实例 | `service_name`、`service_version`、`service_instance_id` | `service_info` 的同名标签 |
   | 请求方法、路由模板 | `method`、`route` | `method`、`route` |
   | 响应状态码 | `status`（错误日志） | `status` |

   新增字段或标签前先查这张表和日志的"每条日志都带的字段"，已有的含义用已有的名字；
4. **环境不由应用输出**：`APP_ENV` 只出现在启动日志的配置快照里，日志和指标都不带环境字段。一套采集端只采一个环境，
   环境标签在采集端加（Alloy 的 `loki.write`/`prometheus.remote_write` 的 `external_labels`），应用不重复写；
5. **标签只放低基数的值**：Loki 的标签和 Prometheus 的标签都不放 ID、路径、用户输入（见下面"指标"）。

## 日志

### 输出与传播

- `log/slog` JSON 格式，写 stdout，构造在 `internal/platform/logger`。进程不写日志文件、不做轮转，
  收集交给运行环境（容器运行时、journald、日志采集器）；多个实例不要共写同一个文件；
- 关联字段存在 ctx 里（`internal/kit/logctx`），**只存值**；进程的根 logger（`logger.New`）的 Handler 在输出
  每条记录时从 ctx 取出写入。所以不论用哪个 logger（`logctx.From`、`slog.Default()`、Echo 的
  `c.Logger()`、river 的 logger、第三方库经 `slog.New(h)` 重建的），只要调用带 ctx 的方法，字段都在。
  调用方已经显式写了同名字段时以调用方为准，不输出重复的键；
- 接收外部 logger 的平台组件（HTTP server、river client）都经 `logger.Ensure` 取用：不是 `logger.New` 构造的
  logger 会被包一层，传错 logger 也不丢字段。`slog.SetDefault` 由 `newProcess` 设为根 logger；
- `context.Value` 只放横切信息（这里的 logger 和关联字段，以及 `internal/platform/dbtx` 的事务句柄、
  `internal/kit/uow` 的提交后回调、`internal/platform/httpserver` 的排空标记），不传业务依赖。

### 每条日志都带的字段

| 字段 | 含义 | 写入方 |
|---|---|---|
| `service_name`、`service_version`、`service_instance_id` | 哪个进程（`api`/`worker`）、哪个构建、哪个实例 | 根 logger 的静态字段（`process.Identity` 的 `LogAttrs`，见上面"命名约定"）；版本见下，实例是配置 `INSTANCE_ID`，默认主机名 |
| `trace_id`、`span_id` | 全项目唯一的请求标识，不另设其它名字的 ID：网关 access log、应用日志、trace、响应头 `X-Trace-Id`、后台操作日志同一个值；`span_id` 定位到 trace 里的哪一步。ctx 里没有 span（启动、关停）时不输出 | 根 logger 的 Handler 从 ctx 里的 span 取（见下面"tracing"），不经 `logctx` |
| `link_trace_id` | 任务入队时所在 trace 的 `trace_id`：任务执行是一条新的 trace，经 span link 关联；trace 会被采样丢掉，日志不会，只有它能把任务和入队方连起来。请求里入队的是请求的 `trace_id`；worker 里入队的是上一个任务这次执行的 `trace_id`（顺着它的 `link_trace_id` 追到请求）；定时任务和请求之外入队的指向只有入队 span 的 trace，查不到其它日志 | 队列执行中间件，从任务 metadata 的 `traceparent` 解析 |
| `method`、`route` | 属于哪个接口；`route` 是路由模板，没匹配上路由时不输出；`method` 的非标准方法归成 `_OTHER`，与指标标签同值 | 请求入口中间件（`internal/platform/httpserver/trace.go`） |
| `user_id` / `staff_id` | 前台用户 / 后台员工 | `httpserver.SetUserSession` / `SetStaffSession`：两种鉴权都只经它们写入身份。没有登录态、但处理的是某个用户的路径（刷新令牌、注销任务）用 `logctx.WithUserID` 写进 ctx |
| `job_kind`、`job_id`、`attempt` | 哪个任务、第几次执行；`job_kind` 与 river 自己日志里的键相同 | 队列执行中间件；孤儿任务告警只带 `job_kind` |
| `client_platform`、`client_os`、`client_version`、`installation_id` | 客户端自报的端、宿主系统、版本和安装 ID，按"某版本的 Android"筛选日志用；格式不合法或没带时不输出，后台请求一般没有。客户端自报、可伪造，只用于排查 | 请求入口中间件（`httpserver.ParseClientInfo`，约定见 [http.md](http.md) 的"客户端请求头"） |

这些字段由入口经 `logctx` 的 `With*` 函数（`WithRoute`、`WithUserID`、`WithStaffID`、`WithJob`、`WithJobKind`、
`WithLinkTraceID`、`WithClient`）写入 ctx（`trace_id`、`span_id` 由根 Handler 从 span 取），**业务代码不手写**。键名常量在 `logctx`、`process` 里都不导出，其它包引用不到；slog 自己的
`time`、`level`、`msg`、`source` 同样不能当字段名。手写只能写字面量，sloglint 的 `forbidden-keys` 检查字面量
（`.golangci.yml`；它不解析别的包的常量，所以键名不能导出），只放行定义和写入它们的 `internal/kit/logctx`、
`internal/platform/logger`、`internal/platform/process`。来自 ctx 的字段与调用方显式写的同名时根 Handler 不重复输出，
但静态字段和 slog 自己的键会在同一行 JSON 里重复，多数解析器只保留后一个。没有请求的路径（启动、
定时任务的调度循环）只有静态字段。

- `service_version`：构建时用 `-ldflags "-X app.local/internal/platform/buildinfo.version=..."` 注入
  （`deploy/Dockerfile` 的 `VERSION` 构建参数，镜像构建不传时是 `dev`）；没注入时取 Go 嵌入的 VCS 提交前 12 位
  （本地在仓库里 `go build` 时有，有未提交改动加 `-dirty`），都没有时是 `dev`；
- 同一台机器上跑同一服务的多个实例时，必须分别设置 `INSTANCE_ID`，否则 `service_instance_id` 相同；
- 跨进程传播：入队时 otelriver 把当前 span 的 `traceparent` 写进任务 metadata，执行时以 span link 指向它，
  日志的 `link_trace_id` 由此而来；事件不声明 trace 字段；
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
  没有请求的路径也传进程的 ctx。不用 `slog.InfoContext` 这类全局函数（`no-global`），logger 从 ctx 或依赖注入取；
- 字段一律写成 `slog.Attr`（`slog.String`、`slog.Int64`、`slog.Any("error", err)`…），不写键值对（`attr-only`）：
  键值对少写一个值时编译能过，运行时才变成 `!BADKEY`；
- `msg` 是固定的英文短语，形如 `"<模块或包>: <发生了什么>"`（`"user: user_deleted processed"`、
  `"queue: ..."`），**变量一律放字段，不拼进 msg**：同一类事件的 msg 完全相同，才能按 msg 聚合、
  计数和精确检索（`sloglint` 的 `static-msg` 检查）；
- 字段名用 snake_case（`sloglint` 的 `key-naming-case` 检查），同一个含义全项目只用一个名字：上表的字段由入口写入，不手写；事件里常用的有
  `error`（`slog.Any("error", err)`）、`panic`（recover 到的值）、`stack`（panic 所在 goroutine 的堆栈，单独成字段，不拼进
  `error`）、实体 ID（`<实体>_id`）；新增常用字段时先搜一遍已有叫法；
- 一个错误只记一次：能返回就返回，由处理它的那一层记录。HTTP 请求返回的错误由错误处理器统一记录
  （`"http request failed"`，带 `status`、`code`、`error`，业务错误另带 `error_kind`，panic 另带 `stack`；级别按状态码，
  见下面"级别"），handler 和 Service 不再重复记；
  worker 返回的错误和 panic 由队列执行中间件用任务 ctx 记录（带 `trace_id`、`link_trace_id`、`attempt`：river 自己的
  `Job errored`/`Job panicked` 日志带不上它们）——还会重试记 Warn（`"queue: job failed, will retry"`），
  最后一次记 Error（`"queue: job failed, no retries left"`），`river.JobCancel` 记 Warn，`river.JobSnooze` 不记；
  river 另把错误写进任务的 `errors` 列并按策略重试（查 `river_job` 表），worker 不再重复记；只有吞掉错误继续执行（降级、尽力而为的副作用）时才在原地记 Warn；
- 不在每个请求上打 Info：应用不记访问日志，请求量、耗时、状态码分布看指标，单个请求看网关的访问日志
  （按 `trace_id` 关联：网关生成的 `$trace_id` 经 `X-Trace-Id` 传给应用，就是这次请求的 `trace_id`）。

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

日志是一行一个 JSON 对象，用 jq 过滤。在 systemd 下运行时由 journald 收集。裸机部署的单元都设了 `SyslogIdentifier=`
（`<项目>-api-1`、`<项目>-api-2`、`<项目>-worker`、`<项目>-migrate`，以项目 myshop 为例），按它查：`-t` 可以重复，多个实例按时间合并输出。
systemd 219（CentOS 7）上启动后立即退出的进程，日志没有 `_SYSTEMD_UNIT`，`journalctl -u` 查不到，启动失败的原因只能用 `-t` 看：

```bash
journalctl -t myshop-api-1 -t myshop-api-2 -f                                  # 跟踪全部实例
journalctl -t myshop-api-1 -t myshop-api-2 -o cat --since -1h | jq -c 'select(.trace_id=="<id>" or .link_trace_id=="<id>")'
journalctl -t myshop-api-1 -t myshop-api-2 -p err --since -1h -o cat           # 只看 Error（-p warning 含 Warn）
journalctl -t myshop-api-1 -t myshop-api-2 -g '"user_id":"<id>"'               # -g 按正则匹配日志内容
journalctl -t myshop-api-1 -t myshop-api-2 -o json | jq -r '[.SYSLOG_IDENTIFIER, .MESSAGE] | @tsv'   # 带上来自哪个实例
```

`-p` 能用，是因为 `logger.New` 检测到输出就是 systemd 接到 journald 的那条流时，在每行前加 syslog 优先级前缀（Error `<3>`、
Warn `<4>`、Info `<6>`、Debug `<7>`），journald 据此设置优先级并去掉前缀，`-o cat` 看到的仍是纯 JSON。检测方法：systemd 231 起
看 `JOURNAL_STREAM` 是否与 stdout 的设备号:inode 相同；更早的 systemd（219）不设置它，改看 stdout 是否是连到
`/run/systemd/journal/stdout` 的 unix socket。终端、容器和被重定向的输出不加前缀。

## 指标

- **每个端口一个 Registry，内容不重叠**：同一进程的多个端口各自被抓取，内容重复会让按服务求和算重。
  进程的 Registry（`metrics.NewRuntimeRegistry`，由 `NewInfra` 构造，挂在 `Infra.Metrics`）带 Go 运行时、进程指标、
  `service_info` 和业务自定义指标，由 api 的前台端口、worker 的内部端口（`HTTP_INTERNAL_ADDR`，默认 `:9090`）输出；
  api 的后台端口用自己的 Registry（`metrics.NewRegistry`），只有 `service_info` 和后台的 HTTP 指标；api 的内部端口（网关
  鉴权的会话校验，`HTTP_API_INTERNAL_ADDR`）不输出 `/metrics`，它的 HTTP 指标注册进 `Infra.Metrics`（`server="internal"`），
  经前台端口输出；
- **socket activation 时端口由轮流上线的实例共享**（见 [deploy-ci.md](deploy-ci.md) 的"裸机发布"）：`/metrics` 仍在前台、后台端口，
  抓到的是当时在线的实例。1、2 是同一个服务的前后两任，是一条连续的序列；交接的几秒里可能交替抓到新旧进程，新进程的计数器
  从 0 开始，按一次重置处理；
- **两种 Registry 的归属不同**：`Infra.Metrics` 属于进程，每个进程**恰好由一个端口**输出（组合根 `internal/app` 决定是哪个）；
  端口自己的 Registry 只装这个端口的 HTTP 指标。由此：
  - 后台模块的自定义指标也注册进 `Infra.Metrics`，在 api 的**前台端口**输出，不在后台端口；
  - 后台独立部署（单独的 api 实例只承接后台流量，网关不往它的前台端口转发）时，进程仍会启动前台 server，运行时指标和
    自定义指标仍在前台端口：这些实例的前台端口也要抓取，否则看不到它们的运行时和业务指标；
  - 以后若让 api 进程能只跑后台 server（不启动前台），组合根要把 `Infra.Metrics` 改由后台端口输出（`adminServerDeps`
    的 `Metrics` 用 `in.Metrics`，不再 `metrics.NewRegistry`），保持"恰好一个端口"；反过来不能让两个端口都输出它，
    否则按服务求和会算重。`internal/app/api_test.go` 的 `TestServerDeps_ProcessRegistryOnExactlyOnePort` 固定了这一点；
- `service_info` 恒为 1，标签是进程身份，每个端口都有：join 按 `job`/`instance` 匹配，缺了它这个端口的指标就过滤不到；
- `/metrics` 由 `metrics.Handler` 输出，客户端在 `Accept` 里要求时用 OpenMetrics 格式（exemplar 只在这个格式里出现）；
- 告警规则不在这个仓库里，由使用方在自己的监控平台配置；
- 按服务过滤时 join `service_info`：Prometheus 的 `job`/`instance` 来自抓取配置，应用不重复输出：
  `sum by (service_name) (rate(http_server_request_duration_seconds_count[5m]) * on (job, instance) group_left (service_name) service_info)`。

### HTTP 指标

`internal/platform/httpserver/metrics.go` 的中间件，一个直方图：

```
http_server_request_duration_seconds{server, method, route, status}
```

- `_count` 就是请求数，错误率按 `status` 算，不另设计数器；`server` 是 `api`、`admin` 或 `internal`；
- 中间件紧贴在请求入口之内（入口建好 span 之后）：被限流（429）、请求体超限（413）、panic（500）的请求都计入。handler 返回的错误由它交给
  `HTTPErrorHandler` 渲染，耗时包含写出错误响应的时间，`status` 是实际写出的状态码；Recover 原样再抛的
  `http.ErrAbortHandler` 按 500 计入后继续抛；`/healthz`、`/readyz`、`/metrics` 不计入；
- `method` 只取标准方法，其余归为 `_OTHER`：方法名来自客户端，不能任意增加标签值。归一化在入口（`methodName`），
  日志的 `method` 字段同值；
- 包装了响应又没实现 `Unwrap` 时取不到实际写出的状态码，`status` 按错误推算（`StatusOf`），不会把错误请求记成 200；
- span 被采样（配置了导出）时，观测值带 `trace_id` exemplar，Grafana 能从指标跳到这条 trace；不导出时不带，
  否则 exemplar 指向不存在的 trace。导出时 trace 的去留由 Collector 尾部采样决定：慢的和出错的全保留，高延迟桶的
  exemplar 都能跳转，低延迟桶的可能指向已丢弃的 trace。exemplar 只在 OpenMetrics 格式里输出，抓取端要协商这个格式。

### 路径一律用路由模板

- `route` 是路由模板（`c.Path()`，如 `/v1/widgets/:id`），按模板聚合：
  `sum(rate(http_server_request_duration_seconds_count{method="GET", route="/v1/me"}[5m]))`。没匹配上路由的
  请求 `route` 为空，扫描器的乱扫路径不会各占一条时间序列；
  `server_test.go` 的 `TestServer_MetricsRouteLabelIsRouteTemplate` 固定了这一点；
- 网关鉴权的两个指标（[auth.md](auth.md) 的"网关鉴权"）：`user_gateway_auth_results_total{result}` 是校验端点的结果分布
  （`ok`、`invalid_token`、`session_replaced`、`error`；成功和两种失效都是 HTTP 200，HTTP 指标分不开）；
  `gateway_assertion_key_info{kid}` 是本进程已加载的断言公钥，轮换断言密钥时用它确认所有实例都有新 `kid`。网关侧的
  "校验不可用"到不了 user 服务，看网关访问日志的 `auth` 字段（`anonymous`、`ok`、`invalid_token`、`session_replaced`、
  `rate_limited`、`unavailable`，开了网关鉴权的项目的日志格式才有）；
- 自定义指标同样不把 ID、查询参数、用户输入放进标签，注册进 `Infra.Metrics`（api 进程的在前台端口输出，后台模块的也一样，见上面"两种 Registry 的归属不同"）。"某个具体对象被访问了多少次"查日志，不进指标。

## tracing

`internal/platform/tracing` 构造进程的 TracerProvider，组合根在 `NewInfra` 里装配（挂在 `Infra.Tracer`，不设为 otel 的
全局 provider：不经组合根构造的出站客户端从 ctx 里的 span 取 provider，见微信调用），`Infra.Close` 在所有组件停下之后最后关停（最多 3 秒，导出不完的丢弃）。

- **trace_id 始终存在**：始终是 SDK provider。`OTEL_EXPORTER_OTLP_ENDPOINT` 为空时 `NeverSample`、不挂 exporter，不建立
  任何连接，但 SDK 先生成 ID 再做采样决定，日志照样有 `trace_id`；设置后全量导出（`AlwaysSample`），导出量由 Collector
  的尾部采样控制。其余 `OTEL_EXPORTER_OTLP_*`（headers、超时）由 exporter 直接读环境变量；
- resource 只来自 `process.Identity`（`service.name`、`service.version`、`service.instance.id`），与日志、指标同一来源，
  不读 `OTEL_SERVICE_NAME`、`OTEL_RESOURCE_ATTRIBUTES`；
- **trace_id 的来源**（请求入口中间件 `internal/platform/httpserver/trace.go`）：只有 `HTTP_TRUSTED_PROXY=true`（在网关后）
  时才接受上游的 `traceparent`（以后拆出的内部服务之间），其次是 `X-Trace-Id`（网关的 `$trace_id`，由 nginx 内置的 `$request_id` 经 `map` 改名，必须是 32 位十六进制
  且非全零，否则忽略），都没有时随机生成。网关给的 ID 经自定义 IDGenerator 直接成为根 span 的 `trace_id`，根 span 没有
  伪造的父 span。直连时一律新生成：客户端不能指定 trace_id，把自己的请求混进别人的日志；
- 客户端（浏览器、App）带来的 `traceparent` 一律不作为父 span（网关在 `snippets/proxy.conf` 里清掉它）。接入客户端遥测
  时，服务端仍新开 trace，用 span link 指向客户端的 span（OTel 对不可信入口的建议，和 river 任务关联入队方的做法相同）；
- 网关因连接错误把请求重试到另一个实例（`proxy_next_upstream`）时，两个实例收到同一个 `$trace_id`，同一条 trace 下
  会有两个根 span、`instance` 不同：这是同一个网关请求的两次尝试，不是执行了两遍。`trace_id` 的含义是"网关请求"，
  网关 access log 只有一行，`upstream` 字段列出两个实例；
- 开了网关鉴权时，同一个 trace 下还有一个路由为 `/internal/auth/verify` 的根 span：那是网关为这个请求做的会话校验
  （gateway_auth.lua 把 `$trace_id` 显式传给子请求，子请求自己的 `$request_id` 与父请求不同），不是重试。按路由区分；
- 响应头 `X-Trace-Id` 写回当前 `trace_id`，前台 CORS 在 `ExposeHeaders` 里放行它，跨域的前端出错时能展示给用户、报给客服；
- 需要持久化这个 ID 的适配器（后台操作日志）用 `httpserver.TraceID(ctx)` 取值，模块不 import otel；

插桩只在边界，domain 不建 span：

| 边界 | 做法 |
|---|---|
| 入站 HTTP | 请求入口中间件（Echo 的 `Use` 链最外层）。Echo 先匹配路由再执行 `Use` 链，span 名直接是 `"{method} {路由模板}"`，没匹配上路由时只用 method；客户端请求头（`X-Client-*`、`X-Installation-Id`）只进日志字段，不写 span 属性（见下文"span 属性的取舍"）；`/healthz`、`/readyz`、`/metrics` 不建 span（与全局限流同一个 `healthEndpointSkipper`）。5xx 和 panic 标 Error，4xx 不标。被限流（429）和没匹配路由（404）的请求也建 span：日志和 `X-Trace-Id` 要用它的 trace_id，且未标错的 span 会被尾部采样按比例丢弃；洪泛流量由网关限流挡住，不靠应用少建 span |
| PostgreSQL | `otelpgx` 设到连接池的 `Tracer`（`postgres.PoolConfig.Tracer`）：span 名是 SQL 的第一个词，完整语句在属性里，不记参数值 |
| Redis | `redisotel.InstrumentTracing`（`internal/platform/redis`），不记命令参数（键里有 ID，值可能是业务数据） |
| 微信等外部 HTTP | 自己建 client span，只记方法、域名、路径、状态码（`internal/platform/oauth/wechat.go`）；非 2xx 和 `errcode`≠0 都标错（微信的业务错误 HTTP 状态恒为 200），provider 取自 ctx 里的 span（`trace.SpanFromContext(ctx).TracerProvider()`）。不用 `otelhttp.NewTransport`：它把完整 URL 记进 `url.full` 且不能配置脱敏，微信的查询参数里有 `appid`、`secret`（长期有效的应用凭据）和用户的 `access_token`。也不向外部服务传播 `traceparent` |
| river | `otelriver` 排在队列中间件的最外层（`EnableTracePropagation`）：入队时把 `traceparent` 写进任务 metadata，执行时建 work span（名为 `river.work/<kind>`）并以 span link 指向入队的请求。任务执行是一条新的 trace：任务可能几小时后才执行，父子关系会让 trace 横跨很久，尾部采样也等不了 |

- span 名只用由服务端决定、取值有限的值（路由模板、归一化后的方法、状态码、任务类型）：span 名用来聚合，取值无限会让
  按名统计失去意义；
- span 属性的取舍看敏感性和在哪里检索，不看取值多少：
  - 不放凭据和可能带凭据的原始内容（原始路径、查询参数、请求头、请求体字段），理由同下表微信那一行；
  - 不放用来定位某个人、某台设备、某个实体的标识（用户 ID、安装 ID、订单号）：trace 经尾部采样大部分被丢弃，在 trace
    后端按标识搜索找不全；日志不采样，这些标识写进日志字段，按日志筛选再经 `trace_id` 跳到 trace。敏感标识也因此只存在
    日志一处；
  - 采样策略要用的低敏感字段（如客户端的端和版本，用于"新版本灰度期全量保留"）可以进 span 属性，前提是同一个改动里
    在 `deploy/baremetal/alloy/host.alloy` 的尾部采样加上用它的策略；没有策略用它就不加；
- OTel 的 `app.*`、`device.*`、`session.*` 语义约定（已从核心约定迁到客户端注册表
  [semantic-conventions-client-side](https://github.com/open-telemetry/semantic-conventions-client-side)，仍是 development）
  描述的是客户端自己发出的遥测（客户端进程的资源和 span），不用来在服务端 span 上描述请求方。`session.id` 指客户端的
  使用会话，不是登录会话（`user_sessions`），登录会话 ID 不用这个名字；
- 错误的完整信息只写在日志里，span 只标状态，经 `trace_id` 关联；不把业务事件写成 span event。插桩库（otelpgx、redisotel、
  otelriver）会把 `err.Error()` 写进状态描述和 `exception` 事件，`internal/platform/tracing` 的 provider 在交给 exporter
  之前统一去掉（`errorTextRemover`），插桩处不用各自处理；
- otelpgx、otelriver 不传 MeterProvider 时退回 otel 全局 provider，两处都显式传 noop（`newPgxTracer`、`queue.NewClient`），
  指标只走 Prometheus；
- Collector 的尾部采样：出错的全保留、慢的全保留、其余按比例。一条 trace 只出自一个进程，每台机器本机的 Alloy 就能看全，
  不需要按 trace_id 负载均衡；示例见 `deploy/baremetal/alloy/host.alloy`（全机一份），各项目的抓取和日志读取在
  `deploy/baremetal/alloy/project.alloy` 渲染出的片段里，打 `project` 标签区分同机的多个项目（`service_name` 固定是 api/worker）；
- 端到端链路（真实的 Collector 尾部采样、Tempo 存储、Grafana 从日志跳到 trace）还没有在目标环境验证过，测试只覆盖到
  交给 exporter 之前。

## 健康检查

- `/healthz`：liveness，恒定 200，不查依赖。api、worker 和后台 server 都有；
- `/readyz`：readiness，逐个 Ping 依赖（数据库，配置了 Redis 时还有 Redis），任一失败
  返回 503。所在 server 处于排空期（`HTTP_SHUTDOWN_DRAIN`，见 [deploy-ci.md](deploy-ci.md)）时直接返回 503
  （`code: draining`）：`httpserver.Component` 在请求 ctx 里标记排空，由 `RegisterReadyz` 判断。api 的前台和后台端口
  都挂（编排器、本机排查按端口探测）；worker 没有"要不要把流量路由过来"这个概念，不挂；
- socket activation 时两个实例共享端口，外部探测 `/readyz` 分不清实例，就绪改由进程判断：发 `READY=1` 之前用同一组 `Pinger`
  确认依赖可用（`internal/app/listeners.go` 的 `waitDependencies`，失败每秒重试，直到 systemd 的 `TimeoutStartSec` 终止进程）。
  连接池是惰性建连的，不检查的话库连不上也会发出 READY。非 socket 模式不做这一步，阻塞在 Serve 之前会让 `/healthz` 无响应；
- 依赖列表在 `internal/app/infra.go` 的 `Pingers`；
- distroless 镜像里没有 curl，Dockerfile 的 HEALTHCHECK 用二进制自己的 `healthcheck`
  子命令（`internal/platform/healthcheck`）。纯 docker/compose 判定 unhealthy 后不会
  自动重启容器。
