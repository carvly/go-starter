# 可观测性：日志、request_id、指标、健康检查

事实来源：`internal/platform/logger/`、`internal/kit/logctx/`、`internal/platform/httpserver/health.go`、`internal/platform/healthcheck/`、`internal/platform/httpserver/server.go`

## 日志

- `log/slog` JSON 格式，构造在 `internal/platform/logger`；
- RequestID 中间件为每个请求生成或透传 request_id，把带 `request_id` 字段的 logger
  放进 ctx（`internal/kit/logctx`）。业务代码用 `logctx.From(ctx)` 取 logger，不手动加
  request_id 字段。`context.Value` 只放横切信息（这里的 logger/request_id，以及
  `internal/platform/dbtx` 的事务句柄、`internal/kit/uow` 的提交后回调），不传业务依赖；
- 跨进程传播：发布事件时把 `logctx.RequestID(ctx)` 写进事件，worker 消费时重建带
  request_id 的 logger，同一个请求的 api 日志和 worker 日志可以串起来；
- **脱敏**：不把密码、token、`Authorization` header 整体写进日志，只记 user_id、长度
  这类不构成凭据的信息；
- 启动时打印一份配置快照：`config.Redacted()` 是白名单，只列出其中写明的字段。新增配置项
  要出现在启动日志里，就在 `Redacted()` 里手动加一行；密钥类字段写 `"***"`（或
  `redactedIfSet`），或者干脆不加。

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
