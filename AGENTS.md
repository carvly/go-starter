# go-starter — Agent 须知

这是一份索引：命令、最容易违反的硬规则、改哪类代码前读哪篇文档。第一次接触项目先读
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)（结构和依赖方向，几分钟读完）。

## 改哪类代码前读哪篇

| 要做的事 | 先读 |
|---|---|
| 新增一个资源（新表，后台或前台） | [`docs/agents/generator.md`](docs/agents/generator.md) |
| 新增/删除业务模块、跨模块协作、改 `internal/app/` | [`docs/agents/modules.md`](docs/agents/modules.md) |
| 设计表结构、写 migration、写 Repository、加缓存、集成测试 | [`docs/agents/database.md`](docs/agents/database.md) |
| 需要多步写操作原子提交 | [`docs/agents/transactions.md`](docs/agents/transactions.md) |
| 发布/消费事件、定时任务 | [`docs/agents/queue.md`](docs/agents/queue.md) |
| 改接口（`api/openapi.yaml`、`api/admin.yaml`）、handler、错误码、中间件 | [`docs/agents/http.md`](docs/agents/http.md) |
| 鉴权、前台登录与会话（微信等第三方登录）、给接口加登录保护、密码相关、后台员工与权限 | [`docs/agents/auth.md`](docs/agents/auth.md) |
| 后台前端（`web/admin/`）、改 `api/admin.yaml` 后生成客户端 | [`docs/agents/admin.md`](docs/agents/admin.md) |
| 改架构图（`docs/diagrams/`），或改了图里画的模块、依赖、进程拓扑 | [`docs/agents/diagrams.md`](docs/agents/diagrams.md) |
| 日志、指标、健康检查 | [`docs/agents/observability.md`](docs/agents/observability.md) |
| 配置、Dockerfile/compose、CI、依赖版本 | [`docs/agents/deploy-ci.md`](docs/agents/deploy-ci.md) |
| 创建业务项目、开发业务模块、需要改 starter | [`docs/agents/project.md`](docs/agents/project.md) |
| starter 发版与业务项目升级 | [`docs/agents/upgrade.md`](docs/agents/upgrade.md) |
| 检查失败怎么办、改完跑什么、任务结束时的摩擦回顾、文档和提交规范 | [`docs/agents/workflow.md`](docs/agents/workflow.md) |
| 遇到环境、工具或检查的问题（按需读） | [`docs/agents/known-friction.md`](docs/agents/known-friction.md) |

选型及理由见 [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) 的"技术选型"；规划中的改动在
`docs/proposals/`。

## 命令

```bash
go build ./... && go test ./<改动的包>/...   # 每改一次就跑，秒级
go test ./internal/archtest/                 # 依赖方向，秒级
go test ./...                                # 全部单元测试 + 架构测试 + 注册完整性测试
make ci                                      # 本地复现 CI 主流程（要求改动已提交）
make test-integration                        # 真实 Postgres 上的测试，需要 Docker、Podman 或 socktainer
make check-modules                           # 对每个模块演练删除，约 1 分钟
make remove-modules M="auth"                 # 真实删除模块并记录 starter-removed.yaml
make starter-upgrade VERSION=<tag>           # 升级 starter；手动事项完成后加 CONTINUE=1
make generate                                # 改了 api/openapi.yaml 之后
make doc-impact                              # 列出可能受改动影响的文档，逐篇核对
make outdated                                # 列出能升级的版本（只读）
make upgrade                                 # 兼容范围内升级依赖，之后按 deploy-ci.md 看 changelog
make setup                                   # 安装提交钩子
go run ./tools/gen check|plan|apply <描述文件> # 新增一个资源（新模块），见 generator.md
```

## 硬规则

1. **检查失败时修代码，不修检查。** 不改测试期望值来让它通过，不往
   `internal/archtest/exceptions.go` 加例外，不加 `//nolint`，不改受保护文件（清单见
   [`docs/agents/workflow.md`](docs/agents/workflow.md) 的"规则变更的标记"）来放宽检查，
   不用 `--no-verify`。认为检查本身错了，就停下来向用户说明理由，由用户决定。
2. **依赖方向**：`modules/<m>/domain` 只依赖标准库、`internal/kit` 和本模块
   `events`；模块之间不互相 import（`events` 除外），跨模块能力由 `internal/app` 的适配器
   提供；`platform` 不认识模块；模块只读写自己在 `postgres/tables.go` 登记的表；
   `httpapi`/`jobs` 只经本模块 Service 读写数据，不碰 `postgres` 和 Repository。违反时
   `go test ./internal/archtest/` 会失败并写明改法。
3. **新增/删除模块时组合根只改 `internal/app/modules.go`、`migrate.go`、
   `module_configs.go`**（外加跨模块适配器 `adapters_*.go`；spec、migration、模块目录和后台页面随模块增删），不改 `cmd/*/main.go`。
   每个模块在 `modules.go` 里只占固定的几行，变量名是 `<m>M`。完整步骤见
   [`docs/agents/modules.md`](docs/agents/modules.md)。
4. **spec-first**：接口先改 `api/openapi.yaml`（后台改 `api/admin.yaml`，每个操作还要写
   `x-permission`；operation 必须带模块的 tag，请求字段约束必须同时写
   `x-oapi-codegen-extra-tags`），再 `go generate ./...`，最后改 handler。
   不手写业务路由，不手改 `api.gen.go`。
5. **业务错误用 `errs.New(kind, code, message)`**，Code/Message 会原样返回给客户端，
   不能带内部细节；只在模块内部使用的错误用 `KindInternal`。
6. **Repository 一律经 `handle.Resolve(ctx)` 取连接**，多步写操作用
   `UnitOfWork.Run` 包起来，入队必须在同一个事务里。竞态敏感的判断写进同一条 SQL。
7. **事件的 `Kind()` 和 JSON 字段名发布后不能改**，消费方必须幂等。
8. **项目特有规则与通用规则分开**：业务模块、项目冒烟和项目硬规则写在
   [`AGENTS.project.md`](AGENTS.project.md)，模块清单与链路写在 `docs/PROJECT.md`；
   不把项目特有内容写进 starter 的通用文件。
9. **每个 bug fix 带一个改动前失败、改动后通过的测试。** 改了哪块代码，同一个改动里
   更新对应的 `docs/agents/` 文档：照最终代码写，跑 `make doc-impact` 把列出的文档逐篇
   核对；文档里引用的路径和标识符必须存在（`scripts/check-docs.sh`）。
10. **只在用户明确要求时才创建 commit。**
11. **边界先行，保持模块内聚和项目防腐。** 新增或修改模块、组合根、通用工具时，先遵守
    [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) 的"长期演化与项目防腐"：优先增加声明或使用
    公开扩展点，不给特定模块增加通用工具的专用分支或独立豁免；业务 domain 只依赖自己的模型和窄端口，
    跨边界的模型和错误由 `internal/app` 适配器翻译。发现两方频繁修改同一文件时先调整边界，不先加
    补丁式检查。
12. **业务项目不改 starter 所有的文件**（`starter-owned.txt`）。需要改时按
    [`docs/agents/project.md`](docs/agents/project.md) 的“需要改 starter 时”处理；
    临时补丁必须登记 `project/starter-patches.md`，升级预检会逐项确认。
13. **发现的债务不能当次处理时，先记录到
    [`docs/agents/known-friction.md`](docs/agents/known-friction.md)，避免丢失。** 写明发现日期、证据、影响和
    建议的反哺去向；新代码不得继续扩大债务。解决后把结果留在正式文档和检查里，并删除对应条目。
14. **高风险改动先验证关键不变量和异常场景。** 涉及外部版本、merge、状态恢复、删除或权限的改动，
    任务拆分时先明确关键不变量及正常、异常、恢复场景，提供对应的持久测试；未通过不得验收。

## 语言与风格

文档、代码注释、commit 信息用中文；代码标识符、文件路径、命令保持英文。默认不写注释，
只在"为什么"不明显时写一行，不写"改了什么、修的是哪个问题"。
