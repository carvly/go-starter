# 配置、部署与 CI

事实来源：`internal/config/`、`internal/platform/systemd/`、`internal/app/listeners.go`、`deploy/`、`configs/`、`.github/`、`Makefile`、`go.mod`、`scripts/check-versions.sh`、`scripts/outdated.sh`、`scripts/upgrade.sh`、`scripts/lib-deps.sh`

改 `internal/config/`、`deploy/`、`configs/`、`Makefile`、`.github/` 之前读这篇。
改受保护的检查文件（`Makefile`、CI 工作流、`scripts/check-*.sh` 等，完整清单只在
[workflow.md](workflow.md) 的“规则变更的标记”）属于规则变更，先看那一节。

## 配置

- 环境变量优先，`CONFIG_FILE` 指向的可选 YAML 覆盖非敏感默认值。`internal/config`
  只加载基础设施配置和共享 Argon2 参数；模块在自己的包里定义 `Config` / `LoadConfig`，
  经 `config.Source.Section` 认领同名顶层段，再用 `Source.Env` 覆盖环境变量。组合根加载全部
  模块后调用 `Source.Finish()`，未认领的顶层段启动失败；
- 必需字段缺失、`CONFIG_FILE` 读不了、值格式错误（比如 duration 没写单位）都在启动时
  直接报错退出，不静默用零值或旧值；`CONFIG_FILE` 严格加载（`yaml.WithKnownFields()`、
  `yaml.WithUniqueKeys()`，只允许单个文档），拼错的键、重复键、多个文档、空文件、
  `yaml:"-"` 的密钥字段写进文件都会报错；
- 可插拔能力（如微信各登录入口）的配置是可选项，按项启用（见 ARCHITECTURE.md 的
  "能力和启用分开"）：启动校验只拦"配错"（一个入口的凭据只配一半），不拦"没配"，也不要求
  "至少启用一个"，启用哪些由项目的真实业务决定。只有所有部署都离不开的配置才是必需项；
  需要填占位值才能启动，说明必需项定错了；
- secrets 不进 git：JWT 签名密钥、轮换用的旧密钥和微信各应用的 AppID / Secret 只能来自环境变量
  （字段是 `yaml:"-"`）；
  数据库 DSN 和 Redis 密码技术上也能写进 `CONFIG_FILE`，但含密码的配置文件不能提交，
  生产环境用环境变量注入。`configs/.env.example` 只放占位符，本地用 `.env`（godotenv
  加载，不提交）；
- `APP_ENV` 只出现在启动日志的配置快照里，不允许拿它做代码分支；日志和指标的环境标签由采集端加（见
  [observability.md](observability.md) 的"命名约定"）。多环境靠同一套 key 注入不同的值；
- `INSTANCE_ID` 是日志和 `service_info` 指标的 `service_instance_id`，默认取主机名；同一台机器上跑同一服务的多个实例时必须分别设置
  （见 [observability.md](observability.md) 的"每条日志都带的字段"）；
- Redis 可选：配置了 `REDIS_ADDR` 时缓存（模块按需接入 `internal/platform/cache`）和限流计数用 Redis，
  否则缓存退回 Noop、限流按进程内存计数（启动时会 Warn 一次）。

## 进程与部署

- `cmd/api`、`cmd/worker` 只做"加载配置 + healthcheck 子命令 + 调 `app.RunAPI` /
  `app.RunWorker`"，不知道任何业务模块；`api migrate` 在全量配置校验前分派，只读数据库配置；
  `api queue cancel-orphans` 需要完整配置（它构造模块、从实际注册的 worker 得出任务类型）；其余带参数调用交给 `app.RunCommand`，
  执行模块提供的子命令（`api help` 列出全部）。容器里用
  `docker compose run --rm api <模块> <动作> [参数]` 运行（镜像的 `ENTRYPOINT` 是
  `/app/api`），需要和 api 相同的环境变量。退出码：0 成功，1 执行出错，2 用法错误；
- 进程内的长期组件由 `internal/kit/lifecycle` 统一启停：任一组件出错或提前退出，
  其余组件按逆序停止，进程以非零退出码结束；
- `deploy/Dockerfile` 多阶段构建，最终镜像是 distroless nonroot（不用 root 跑是安全
  基线），`CGO_ENABLED=0`；Node 阶段构建后台前端，产物经 go:embed 打进 api 二进制，worker
  镜像不经过这个阶段（见 [admin.md](admin.md) 的“构建与部署”）。根目录的 `.dockerignore`
  排除本地的 `web/admin/node_modules`、`dist`。构建参数 `VERSION`（如 `--build-arg VERSION=v1.2.3`）
  经 `-ldflags` 注入日志和 `service_info` 的 `service_version`。镜像里的构建不带 VCS 信息，不传时是 `dev`，
  发布构建必须传；
- **网关只转发业务路径**。`/healthz`、`/readyz`、`/metrics` 不需要登录，经网关转发会暴露版本号、主机名、运行时信息
  和依赖是否可用；探活、发布脚本、指标采集在内网直接访问端口。前台按白名单只转发 `/v1/`，其余返回 404；后台前端是
  单页应用，`/` 必须转发；后台端口上 `/admin/v1` 之外的根路径只有前端页面、`/healthz`、`/readyz`、`/metrics`、`/docs`、
  `/admin.yaml`，网关按精确匹配拦掉 `/healthz`、`/readyz`、`/metrics`，生产环境再拦文档。后台端口新增根路径时同步这里和网关配置
  （`TestAdmin_RoutesOutsideAPIPrefix` 会提醒）。网关不按来源 IP 限制：`/admin/v1` 靠员工会话和逐操作
  授权保护（见 [staff.md](../../internal/modules/staff/docs/staff.md)），前端页面只是构建产物；
- **文档按环境开关**（`HTTP_API_DOCS_ENABLED`，默认 `false`）：
  - 测试环境打开并**公开转发**（前台 `/docs`、`/openapi.yaml`，后台 `/docs`、`/admin.yaml`），不加认证。前端开发直接打开
    `/docs`，AI 直接取 spec 原文；内容就是测试环境当前部署的版本。spec 不是安全边界，见 [http.md](http.md) 的"API 文档"；
  - 生产环境关闭，网关也不转发这几条路径（前台白名单本来就不含，后台按精确匹配拦掉）；上线的版本先在测试环境跑过，
    看测试环境的文档；
- `deploy/docker-compose.yml`：postgres → `migrate`（执行 `api migrate up`，只需 `DB_DSN`）→
  api / worker，长期运行的 service 都声明了资源上限。api/worker 不会自己建表；
- api 进程可以同时跑后台 server：配置 `HTTP_ADMIN_ADDR`（为空不启动）和
  `HTTP_ADMIN_ORIGIN`（后台前端的 origin，配置了地址时必填）。前台和后台端口都只绑
  localhost 或内网，由网关对外；它和前台共用数据库连接池，连接数上限按两者合计规划。后台必须用独立域名
  （如 `admin.example.com`，前台是 `api.example.com`），后台前端页面和 `/admin/v1` 都在这个域名下、同源：
  cookie 不按端口和路径隔离，和前台同一个主机名时，后台会话 cookie 也会被发到前台 server。
  后台端口的 `/metrics` 只有后台的 HTTP 指标，进程的运行时和业务指标在前台端口：后台独立部署时前台端口也要抓取，
  见 [observability.md](observability.md) 的"两种 Registry 的归属不同"。
  首个超级管理员
  用 `docker compose run --rm api staff create -username root` 创建（见 `internal/modules/staff/docs/staff.md`），
  会话和日志参数是 `STAFF_*` 环境变量。前台用户的登录会话和微信凭据是 `USER_*` /
  `WECHAT_*` 环境变量（见 `internal/modules/user/docs/user.md` 和 `configs/.env.example`）。其中
  `USER_PUBLIC_BASE_URL` 是必需项，缺了 api 和 worker 都起不来；`WECHAT_*` 四个入口按产品
  需要组合，每个入口的 `_APP_ID` / `_SECRET` 成对配置才启用，只配一半启动失败；其余 `USER_*`
  时长有默认值，`USER_COOKIE_DELIVERY_ORIGINS`、`USER_TRUSTED_IDENTITY_HEADER`（网关信任 header 模式，见
  auth.md 的"前台鉴权"）、`USER_SESSION_SLOT_LIMIT`（按设备类别互踢，默认不启用，见 user.md 的"会话策略"）可选。`HTTP_GLOBAL_RATE_LIMIT_PER_SECOND`、`HTTP_REQUEST_TIMEOUT`、
  `HTTP_BODY_LIMIT_BYTES` 必须为正数，否则启动失败；默认值只在 `internal/config` 一处。
  `USER_PUBLIC_BASE_URL` 和 `USER_COOKIE_DELIVERY_ORIGINS` 里的每一项都必须是规范的 origin
  （小写 `scheme://host[:port]`，不带默认端口和路径），否则启动失败。compose 在 `x-user-env` 里给了开发默认值（微信各入口默认不启用，
  要用的入口在 compose 文件同级的 `.env` 里配上凭据），新增必需配置时同样要在 compose 里补默认值。compose 里
  后台监听 8081（页面和接口都在这个端口，
  只发布到宿主机的 127.0.0.1），`HTTP_ADMIN_ORIGIN` 默认 `http://localhost:8081`；
- 用 Podman 跑 compose 时（`podman compose` 调用 podman-compose）有两处和 Docker 不同：
  `up -d --build` 会重建镜像，但**不替换正在运行的容器**，要加 `--force-recreate`
  （`podman compose -f deploy/docker-compose.yml up -d --build --force-recreate --no-deps api`），否则跑的
  还是旧镜像；在终端里执行 `compose run` 会分配 TTY，输出的行尾带 `\r`，脚本解析输出（如
  `staff create` 打印的临时密码）前先 `tr -d '\r'`；
- api 只监听 HTTP，TLS 由上游反代/LB 终止；HSTS 由终止 TLS 的那一层加：网关加（推荐，网关自己返回的 404 也带）时
  `HTTP_HSTS_MAX_AGE` 保持 0，不重复发；网关不加时才在应用里打开；
- 排空：`HTTP_SHUTDOWN_DRAIN`（默认 0）是收到停止信号后继续服务的时间，期间每个响应带 `Connection: close`、`/readyz`
  返回 503，空闲连接留给网关按自己的 `keepalive_timeout` 关掉。网关后的部署必须设，且 ≥ 网关 upstream 的
  `keepalive_timeout` + 1s：否则服务端关闭空闲连接的瞬间，网关可能复用它发出请求，非幂等请求不会被重试，直接失败；
- 关停顺序（`httpserver.Component`，行为由 `httpserver.Stop` 决定）：收到停止信号起每个响应带 `Connection: close`；自己绑定
  的端口排空期内照常 accept（一关闭新连接就被拒绝，要等网关按 `/readyz` 的 503 摘掉实例），排空结束才关闭 listener；
  与其它进程共享的 socket（`Stop.SharedListener`，socket activation）立即关闭本进程这份 fd、不再 accept，新连接由接班的实例
  接走，排空期只服务已建立的连接、连接都关闭后提前结束。之后等 `Serve` 返回（≤1s），再等没有"正在处理"的连接、"已 accept
  还没发来第一个请求"的连接最多等 1s，最后 `http.Server.Shutdown` 关掉剩下的空闲连接，后两步合计 ≤15s。不能一开始就
  `Shutdown`：它开始后读完请求的连接会被直接关闭而不响应（网关收到 EOF，502）。api 的关停上限是排空期 + 20s，再加 tracing
  关停的 3s，进程管理器的停止超时要大于总和；同组组件出错（如后台端口监听失败）导致的停止不排空，立即关闭
  （`lifecycle.Failed`）；worker 的内部端口没有网关转来的流量，不排空；
- tracing：`OTEL_EXPORTER_OTLP_ENDPOINT` 为空时不导出、不建立连接（trace_id 照样有），设置后全量导出，由 Collector
  做尾部采样（见 [observability.md](observability.md) 的"tracing"）；
- Postgres 备份/PITR 是基础设施职责，不在应用代码里，但部署时必须确认已经覆盖。

### 前台登录的部署约束

- **web 前端和 API 必须同站**（同一个可注册域名），推荐反代成同源：refresh cookie 是
  SameSite=Strict，浏览器授权流程的绑定 cookie 是 SameSite=None，跨站时会被浏览器的第三方 cookie
  限制拦掉；
- **两张来源名单分开配**：`CORS_ALLOW_ORIGINS` 列 web 和混合 App 的 origin；
  `USER_COOKIE_DELIVERY_ORIGINS` 只列 web（名单内的请求 refresh token 只进 cookie）；
- **微信 API 的 IP 白名单**：各类微信应用开启 API IP 白名单后，服务端的出口 IP 要逐个登记，否则
  报 40164；部署时固定出口 IP（NAT）；
- **平台侧的回调配置都指向 API 域名**：服务号"网页授权域名"填全域名、不带协议，域名需要备案并在根
  目录放校验文件；开放平台网站应用填授权回调域；网站应用的"业务域名"校验文件和授权回调域是两回事；
- **网关的职责**：网关只做 TLS 终止、同源反代、`X-Forwarded-For`（配合 `HTTP_TRUSTED_PROXY=true`）、
  粗粒度限流，转发前删掉客户端带来的 `X-User-ID`。不在网关里做登录和 JWT 校验：那样吊销要等 token
  过期才生效，依赖会话的操作（† 操作、绑定、重新验证）也不可用，只会返回 `session_unavailable`（见 [auth.md](auth.md) 的"前台鉴权"）。

### 裸机发布

单台应用机（裸机/VM），systemd 管理进程，OpenResty 同机反代。适用于 CentOS 7（内核 3.10、systemd 219）及以上
（CentOS Stream 9/10 实测通过），所有单元和脚本只用 systemd 219 具备的功能。发布、`rollout.sh --restart`、api 进程崩溃时，
经网关的请求不失败（崩溃那一刻在途的请求除外）。同一台机器可以部署多个项目。

`deploy/baremetal/` 里的文件分两类。**每个项目一份**的是带占位符的模板，由 `deploy/baremetal/render.sh` 按项目名、端口、域名
生成（不启用后台时省略 `--admin-port`、`--admin-domain`，后台相关的行全部去掉）：

```bash
deploy/baremetal/render.sh --name myshop --api-port 8080 --admin-port 8081 --worker-port 9090 \
    --api-domain api.myshop.com --admin-domain admin.myshop.com \
    [--root /opt/myshop] [--conf-dir /etc/myshop]                    # 生成到 ./rendered-myshop，并打印安装命令
```

| 模板 | 生成 | 作用 |
|---|---|---|
| `systemd/api.socket` | `<项目>-api.socket` | systemd 绑定并持有前台、后台端口 |
| `systemd/api@.service` | `<项目>-api@.service` | api 实例（`%i` 为 `1`、`2`），`Sockets=` 继承同一个 socket |
| `systemd/worker.service` | `<项目>-worker.service` | worker，单实例 |
| `systemd/migrate@.service` | `<项目>-migrate@.service` | 迁移（`%i` 为版本号），一次性服务，环境与 api 相同 |
| `openresty/project.conf`、`openresty/admin.conf` | `<项目>.conf` | 网关：单地址 upstream、只转发业务路径；后台是独立域名 |
| `alloy/project.alloy` | `<项目>.alloy` | 抓本项目 3 个端口的指标、按 `SYSLOG_IDENTIFIER` 读 journald，打 `project` 标签 |
| `rollout.sh` | `/opt/<项目>/bin/rollout.sh` | 发布、回滚、零排队重启 |
| `app-cmd` | `/opt/<项目>/bin/app-cmd` | 以服务的用户和环境执行子命令 |
| `app.env.example` | `/etc/<项目>/app.env` | 配置骨架，地址与 socket 一致 |

**全机一份**、不随项目渲染：`openresty/http.conf`（`$trace_id` 的 map、`trace_json` 日志格式）、`openresty/snippets/proxy.conf`
（各项目 server 块共用的转发设置）、`alloy/host.alloy`（remote_write、Loki 写入、日志 JSON 解析、OTLP 接收与尾部采样；Alloy
以目录方式加载，各项目的片段引用它的组件）、`sysctl/90-somaxconn.conf`。

部署目录由 `--root` 指定（默认 `/opt/<项目>`，下有 `releases/<版本>/`、`current`、`bin/`），配置目录由 `--conf-dir` 指定（默认
`/etc/<项目>`，放 `app.env`），下文以默认值举例。项目名派生其余名字：运行用户、单元名、锁
`/run/<项目>-rollout.lock`、日志标识 `<项目>-api-1`、`<项目>-api-2`、`<项目>-worker`、`<项目>-migrate`、网关 upstream 名、Alloy 组件名。
端口和域名由参数给出，同机的项目各用一组。`service_name` 固定是 `api`/`worker`，多个项目共用观测后端时靠指标和日志的
`project` 标签与 `INSTANCE_ID`（`<主机>-<项目>-api-<n>`）区分。

`/etc/<项目>/app.env` 放全部配置（属主 `root:<项目>`、权限 640）：业务配置（`DB_DSN`、`REDIS_ADDR`、密钥、`CORS_ALLOW_ORIGINS`
和 `USER_COOKIE_DELIVERY_ORIGINS` 列 web 前端、`OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318`），以及渲染时填好的
`HTTP_ADDR`、`HTTP_ADMIN_ADDR`、`HTTP_ADMIN_ORIGIN`、`USER_PUBLIC_BASE_URL`。格式：每行 `KEY=VALUE`，`#` 开头为注释，
值不加引号、不含反斜杠、行首行尾没有空白、不续行：这个范围内 systemd 的 `EnvironmentFile=` 和 `app-cmd` 读到的值相同
（`&`、`$`、`;` 原样保留），超出范围的行 `app-cmd` 直接报错。**单元里用 `Environment=` 设定的键不能写进 app.env**
（`APP_ENV`、`HTTP_TRUSTED_PROXY`、`HTTP_SOCKET_ACTIVATION`、`HTTP_SHUTDOWN_DRAIN`、`INSTANCE_ID`、`HTTP_INTERNAL_ADDR`）：
`EnvironmentFile=` 的值会覆盖 `Environment=`，与书写顺序无关，照 `configs/.env.example` 抄进来会悄悄关掉 socket activation
或排空；`rollout.sh` 发现这些键就拒绝执行。

**原理**：平时只有一个 api 实例在线。`<项目>-api@1`、`<项目>-api@2` 都写 `Sockets=<项目>-api.socket`，继承的是 systemd 持有的**同一个**
监听 socket、同一个 accept 队列（`HTTP_SOCKET_ACTIVATION=true`，见 `internal/config` 的 `SocketActivation`）。发布时启动
备用实例：它取得 socket（`systemd.Listeners`，按地址认领前台、后台）、初始化、用 `/readyz` 的同一组 `Pinger` 确认依赖可用
（连接池是惰性建连的），之后才 `systemd.Notify(systemd.Ready)`，`Type=notify` 的 `systemctl start` 阻塞到这时。观察 5 秒：这时
旧实例还没收到停止信号，两个实例一起 accept，新实例已承接真实流量，若它崩溃，旧实例照常服务。然后停掉旧实例：它立即关闭
自己那份 fd、不再 accept（新连接全部由新实例接），已建立的 keep-alive 连接回完当前请求就关（`Connection: close`），连接
关完即退出，最多排空 5 秒。socket 始终由 systemd 和存活的实例持有，旧实例下线时队列里的连接由新实例接走，不会被 RST，
也不用排队等新进程启动。网关的 upstream 只有一个地址，看不到实例切换，不需要主动健康检查、
发布不 reload。不用 SO_REUSEPORT：每个 socket 有自己的队列，关闭时队列里已握手的连接被 RST，内核 5.14 的
`tcp_migrate_req` 才能迁移，CentOS 7 没有。

两个实例共享端口，外部探测分不清实例，**READY 是唯一的就绪信号**。指标照常在前台、后台端口的 `/metrics` 由 Alloy 在本机
抓取：抓到的是当时在线的实例，1、2 是同一个服务的前后两任，是一条连续的序列（交接的几秒里可能交替抓到新旧进程）。

单元里三项是必须的：
- `NonBlocking=yes`：默认 no 时 systemd 在启动新实例前清除共享 socket 的 `O_NONBLOCK`，正在服务的实例的 accept 被改成阻塞；
- `[Service]` 段的 `StartLimitInterval=0`：启动频率限制被触发时，systemd 252 起会让 `<项目>-api.socket` 失败并关闭，新实例再也起不来，
  在线实例一停连接即被拒绝；`[Unit]` 段的 `StartLimitIntervalSec=` 在 219 上无效；代价是崩溃循环的实例每秒重启、不会自己停下；
- `Service=<项目>-api@1.service`（socket 单元里）：219 要求 socket 有对应的服务。只有两个实例都停止后有连接进来时才会拉起它（兜底），
  交接期间不会触发。

**systemd 219 的限制**（CentOS 7）：没有 `Type=exec`（worker 用 `simple`）；可执行文件路径里的 `%i` 不展开（迁移经
`/usr/bin/env` 执行）；没有 `FileDescriptorName=`（进程按地址认领 socket，`ListenStream=` 的顺序无关；认领不了、重复、
后台 socket 的有无与后台是否启动不一致时启动失败）；没有 `systemd-run --wait/--pipe`（子命令用 `app-cmd`）；启动即退出的进程日志没有 `_SYSTEMD_UNIT`
（排查用 `journalctl -t <项目>-api-1`，单元都设了 `SyslogIdentifier=`）；不设置 `JOURNAL_STREAM`（日志级别见
[observability.md](observability.md) 的"排查"）；`somaxconn` 默认 128，要装 `sysctl/90-somaxconn.conf`，并在 socket 里写 `Backlog=4096`。

**发布**：`/opt/<项目>/bin/rollout.sh v1.2.3`，顺序是迁移 → 原子切换 `current` → worker（等它的 `/healthz`）→ api（启动不在线的
那个实例、等 READY、观察 5 秒、停掉在线的），1、2 轮流上线，在线实例被 `enable`、下线的被 `disable`。观察期每秒检查新实例
仍在运行且 `MainPID` 没变（219 没有 `NRestarts`），发现被 `Restart=` 重新拉起也算失败。发布前的版本记在
`/opt/<项目>/.rollout-prev`，发布或回滚成功、或退回后删除；`--restart` 不换版本，不动它。worker 必须先于 api，见
[queue.md](queue.md) 的"发布顺序"。实测一次约 10–12 秒。同时只允许一个 `rollout.sh`（`flock`）；在 tmux/screen 里执行：
ssh 断开会中断脚本，已提交给 systemd 的操作会继续，重跑脚本时会先收敛实例状态。`systemctl stop` 打印的
"can still be activated by <项目>-api.socket" 是 systemd 的正常提示。

**失败时自动退回**：新 worker、新 api 起不来，或新 api 在观察期内退出，脚本停掉新实例，把 `current`（发布时还有 worker）
切回原版本，在线实例一直在服务。必须切回：兜底拉起、崩溃重启、开机启动的都是 `current` 指向的版本。上次发布中断过时
（`.rollout-prev` 还在），退回目标是中断前的版本，而不是中断时已经切过去的 `current`。

**回滚**：`rollout.sh --rollback v1.2.2`，跳过迁移，顺序是切换 `current` → api → worker（与发布相反：仍在线的新 api 入队的任务
不能交给旧 worker）。不能不带参数重跑旧版本：旧二进制执行 `migrate up` 时数据库已在新版本，旧二进制里没有这个迁移文件，
golang-migrate 直接报错。前提是迁移遵守 expand/contract，旧代码能跑在新表结构上。

**重启**：`rollout.sh --restart` 不换版本换一个 api 进程。不要用 `systemctl restart <项目>-api@1`：单实例 restart 是先停后起，
请求不会失败，但要在队列里等排空 + 初始化的时间。

**子命令**（`staff create`、`queue cancel-orphans`）：`/opt/<项目>/bin/app-cmd staff create -username root`，以项目用户执行
`/opt/<项目>/current/api`，env 文件逐行 `export`，不经 shell 求值，输出和退出码直接回到终端。

**首次部署**：全机一份的文件只装一次（CentOS 7 装 `sysctl/90-somaxconn.conf`；OpenResty 的 `http.conf`、`snippets/proxy.conf`；
Alloy 的 `host.alloy`）。每个项目：`render.sh` 生成并按它打印的命令安装（建用户、目录、单元、`app.env`、网关和 Alloy 片段），
上传版本到 `/opt/<项目>/releases/<版本>/`，`ln -sfn releases/<版本> /opt/<项目>/current`，`systemctl start <项目>-migrate@<版本>`，
`systemctl enable --now <项目>-api.socket <项目>-worker`，`systemctl enable --now <项目>-api@1`；之后一律用 `rollout.sh`。
**不要在发布中 stop/restart `<项目>-api.socket`**：会关闭 systemd 持有的 fd，`Requires=` 连带停止实例；改端口是维护操作（停实例
→ 重新渲染、替换 socket 和 `app.env` → `daemon-reload` → 启动），期间服务中断。

**故障**：

| 情况 | 结果 | 处理 |
|---|---|---|
| 迁移失败 | 实例都没动 | 修迁移后重跑 |
| 新 worker 起不来 | `current` 和 worker 切回原版本，api 都没动 | 查 `journalctl -t <项目>-worker`；修复后重跑 |
| 新 api 初始化失败、依赖一直不可用（`TimeoutStartSec=60` 内没有 READY）、观察期内退出 | 新实例被停掉，`current`（发布时还有 worker）切回原版本，在线实例继续服务 | 查 `journalctl -t <项目>-api-<1/2>` |
| socket 地址与配置不符、fd 数量与后台是否启动不一致 | 新实例启动失败，同上 | 对照 `<项目>-api.socket` 的 `ListenStream=` 与 `HTTP_ADDR`、`HTTP_ADMIN_ADDR` |
| api 进程崩溃 | 崩溃那一刻在途的请求失败；之后的连接不被拒绝，在 socket 队列里等待：1 秒后 systemd 重启进程，初始化和依赖检查通过后开始处理 | 无需处理 |
| 没有实例在 accept（崩溃循环、依赖一直不可用） | 请求挂起到网关 `proxy_read_timeout`（30s）才失败，不会快速失败；网关连接数随之累积 | 尽快恢复一个实例；崩溃循环的实例要显式 stop 或回滚 |
| worker 被 `kill -9` | 崩溃时 `running` 的任务 1 小时后由 river 的 rescuer 救回重试 | 等 rescuer，不手工改任务状态 |
| 误停 `<项目>-api.socket` | `Requires=` 连带停止实例，服务中断 | `systemctl start <项目>-api.socket <项目>-api@<在线实例>` |

前置：Postgres 版本按 [database.md](database.md) 的"选型"；`HTTP_TRUSTED_PROXY=true`；配置 Redis（交接期间两个
进程各自按内存计数限流）；数据库连接数按两个 api 实例 + worker 计算（交接期间短暂并存，一般十余秒）；Alloy 的运行用户加入
`systemd-journal` 组。
SELinux enforcing 的机器上未验证：若 OpenResty 以 `httpd_t` 受限运行，需要 `setsebool -P httpd_can_network_connect 1`。
Alloy 在 CentOS 7 上读 systemd 219 的 journald 未验证，部署前单独确认；不行就改用 rsyslog 或 filebeat 采集日志。

## 版本固定

同一个版本出现在多处时，`scripts/check-versions.sh` 以一处为准校验其余各处：
Go（以 `go.mod` 为准）、govulncheck / golangci-lint（以 `Makefile` 为准，golangci-lint
还要和生成器夹具测试 `tools/gen/fixture_test.go` 里的一致）、
Postgres 镜像（以 compose 为准）、Node 主版本（以 `web/admin/package.json` 的 `engines` 为准）、
pnpm（以 `packageManager` 为准），并要求后台前端的客户端生成器写确切版本。找不到应有的版本号、或者写成 `latest`，同样会失败。
oapi-codegen、lefthook 由 `go.mod` 的 `tool` 指令固定；本地的 golangci-lint 由 `Makefile`
用 `go run` 固定版本运行（第一次要编译约 1 分钟）。不用 `@latest`：版本漂移会让 CI 因为
上游发版而变色，与本仓库的改动无关。

依赖升级由 dependabot 每周合并成一个 PR（`.github/dependabot.yml`），覆盖 Go 模块、
GitHub Actions、后台前端（`web/admin`）的 npm 依赖和 Dockerfile 里的镜像，不提议 3 天内刚发布的
版本。Go 版本、Node 镜像的主版本、
compose 里的 postgres 镜像不交给 dependabot：它写在多处，要人一起改（postgres 跨大版本
还要处理数据目录升级）。dependabot 之外，`make upgrade` 可以随时在本地做同样的兼容升级。

### 依赖升级

- `make outdated`（`scripts/outdated.sh`）只读，列出所有能升级的版本：Go 直接依赖（标出发布
  日期，冷却期内的标"冷却中"）、Go 工具链、`Makefile` 固定的两个工具、后台前端依赖、镜像，
  每项附 changelog 链接；
- `make upgrade`（`scripts/upgrade.sh`）只做兼容范围内的升级：Go 直接依赖升到同一主版本内
  的最新版（跳过发布不满 3 天的，和 dependabot 一致，`COOLDOWN_DAYS` 可改），后台前端在
  `package.json` 的范围内 `pnpm update`（pnpm 按 `pnpm-workspace.yaml` 的 `minimumReleaseAge`
  跳过刚发布的版本）；然后 `go mod tidy`、`go generate ./...`，跑 `go build`/`vet`/`test` 和前端
  的 `typecheck`/`lint`/`test`，最后列出每项"旧版本 → 新版本 + changelog 链接"。开始前要求
  `go.mod`、`go.sum`、`web/admin/package.json`、`pnpm-lock.yaml` 没有未提交的改动，检查失败时
  打印放弃这次升级的命令；
- 跨大版本、Go 工具链、`Makefile` 固定的工具、镜像不自动升：它们写在多处或可能不兼容，按上面
  "版本固定"一起改，改完跑 `make check-versions`。

**看 changelog 是升级的一部分，不是可选项。** 对 `make upgrade` 列出的每一项（以及手动升级的
每一项），读当前版本到新版本之间的 changelog / release notes，逐条归到下面三类：

1. **不兼容变更、废弃的 API、行为变化**：查本项目是否用到，用到就在同一个改动里改掉；
2. **新特性、性能优化、安全修复**：判断能不能用来替换本项目手写的实现、简化代码、改善性能
   或安全性（例如 river 新的任务选项、pgx 的新能力、oxlint 新规则、vite 的构建优化）。改动小、
   收益明确的，在同一个改动里采用并补测试；改动大或需要取舍的，写进 `docs/proposals/` 或列给
   用户决定，不要顺手做大改；
3. **和本项目无关的**：不处理。

数据库驱动、river、testcontainers 这类和 Postgres 打交道的依赖升级后，还要跑
`make test-integration`。汇报时逐项写：升了什么、changelog 里和本项目相关的要点、采用了
什么、建议了什么。dependabot 的周 PR 合并前同样按这三类过一遍。

postgres 18 起官方镜像的 `PGDATA` 是 `/var/lib/postgresql/<大版本>/docker`，数据卷挂在
`/var/lib/postgresql`（挂在旧的 `.../data` 上容器会拒绝启动）。旧大版本的数据卷**不会**报错：
17 的集群文件在卷根目录，18 只看 `18/docker`，会静默初始化一个空库，看起来一切正常但数据
都不见了。所以升级前必须先处理旧卷：本地开发 `docker compose down -v` 后重建；有数据的环境
用 `pg_upgrade` 或 `pg_dump` / 恢复迁移。

## CI

`.github/workflows/ci.yml`：

| job | 内容 |
|---|---|
| test | vet、golangci-lint（两者都带 `integration`、`genfixture` tag，见 `.golangci.yml` 的 `run.build-tags`）、`shellcheck -x scripts/*.sh`、govulncheck、`make actionlint`（workflow 文件）、`go mod verify`、生成代码一致性（`go generate ./...` 后，各模块 `httpapi/oapi`、`adminapi/oapi` 的 `api.gen.go` 和 `web/admin/src/api/meta.gen.ts` 不能有改动）、tidy 一致性、`go test ./...`（含架构测试）、`scripts/check-docs.sh`、`scripts/check-versions.sh`、构建 |
| integration | `go test -tags=integration ./...`，testcontainers 起真实 Postgres；先装后台前端依赖（升级工具和删除执行器的集成测试要重新生成前端） |
| gitleaks | 密钥泄露扫描：`make gitleaks`（开源 CLI，版本固定在 Makefile；不用 gitleaks-action，它对组织账号要求付费许可证） |
| openapi-lint | redocly 校验 spec |
| docker-build | api / worker 镜像能构建 |
| compose-integration | 起完整栈，等 api `/readyz` 与 worker 健康；按 `starter-removed.yaml` 跑 starter 通用冒烟（`scripts/ci-staff-smoke.sh`：CLI 建管理员、登录、改密、员工与角色、操作日志；worker 启动时执行 user 清理任务），再跑项目的 `project/ci/smoke.sh`（存在时） |

`.github/workflows/modules.yml`：只在 `internal/**`、`api/**`、`web/admin/src/**`、
`web/admin/scripts/**`、`tools/starter/**`（删除演练的执行器）变化时跑 `scripts/check-module-removal.sh`（约 1 分钟）。它先装好后台前端的
依赖：演练要删掉模块的后台页面再做类型检查，CI 里（设置了 `CI` 环境变量）没装依赖时脚本直接失败，
不会悄悄跳过。

`.github/workflows/admin-web.yml`：后台前端的检查和 compose 栈上的端到端测试，只在
`web/admin/**`、`api/admin.yaml`、`internal/modules/*/adminapi/**`、`internal/modules/*/domain/**`、
`internal/platform/httpserver/`、`deploy/` 等路径变化时运行（见 [admin.md](admin.md) 的“检查”）。
新增带后台页面的模块时，把它加进触发路径。

`.github/workflows/generator.yml`：生成器的夹具 CI（见 [generator.md](generator.md) 的“夹具与 CI”），
在仓库的临时副本里用 `tools/gen/testdata/` 的每份夹具和两份示例生成代码，再跑检查；按编号分成 7 片
并行，每片上限 25 分钟。
`tools/gen/**`、`tools/internal/**`（生成器从代码推导模块信息的共享包）、提供前台登录检查的 `internal/modules/user/module.go`、生成代码依赖的公共包和前端零件（`internal/platform/postgres/`、
`internal/platform/httpserver/`、`web/admin/src/lib/`、`web/admin/src/components/` 等，以工作流里的
`paths` 为准）、两份 spec、`.golangci.yml` 变化时运行，另外每周一定时跑一次。各分片共用 `warm-cache`
job 准备的缓存（Go 依赖、编译结果、golangci-lint 及其分析缓存），`go.sum` 和 `Makefile` 不变时直接命中。

`.github/workflows/guard-rules.yml`：PR 改动了检查规则时，要求 `rule-change` 标签和
"规则变更说明"（见 [workflow.md](workflow.md)）。

所有工作流共用的设置，新增工作流时照做：

- 顶层 `permissions: contents: read`，需要更多权限的 job 单独声明（如 gitleaks 的
  `pull-requests: read`）；
- `actions/checkout` 一律 `persist-credentials: false`：没有 job 需要推送，token 不留在
  `.git/config` 里给后续步骤（`npx`、`pnpm install` 的依赖脚本、`go run` 的工具）读到；
- 每个 job 设 `timeout-minutes`，卡住的 job 不跑满默认的 6 小时；
- `concurrency` 按工作流和 PR 分组，同一个 PR 连续推送时取消旧的运行，main 上的推送都跑完；
- action 用主版本 tag（如 `@v7`），由 dependabot 升级；
- `ci.yml` 不对纯文档改动加 `paths-ignore`：`scripts/check-docs.sh` 在其中检查文档引用的路径和
  标识符，跳过会让坏引用直接进 main。确实要省时间，先把文档检查拆成不带 `paths-ignore` 的独立 job。

本地 `make ci` 复现 test job，要求改动已经提交（它检查 `go.mod` 和生成代码是否已提交）。
改了 `api/openapi.yaml` 或 `api/admin.yaml` 后跑 `make generate`，把生成的文件一起提交。
