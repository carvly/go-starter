# 使用 go-starter

事实来源：`deploy/docker-compose.yml`、`Makefile`、`configs/.env.example`、`tools/gen/`

starter 的使用说明：怎么跑起来、常用命令、怎么加模块、怎么建业务项目和升级。结构和依赖方向见
[`ARCHITECTURE.md`](ARCHITECTURE.md)，改代码前按 [`AGENTS.md`](../AGENTS.md) 的索引读 `docs/agents/` 下的主题文档。
这份文档归 starter 所有，随 starter 升级；业务项目的 `README.md` 写项目自己的说明。

## 快速开始

**方式一：docker compose（推荐，一条命令跑通 api+worker+postgres）**

compose 给必需配置填了开发用的默认值。微信登录的四个入口默认都不启用，要用哪个入口就在
compose 文件同级的 `.env` 里配上它的 appid 和 secret（变量见 `configs/.env.example`）。

```bash
docker compose -f deploy/docker-compose.yml up -d
# 或用 Podman（见 deploy/docker-compose.yml 文件末尾的说明）：
podman compose -f deploy/docker-compose.yml up -d
```

**方式二：本地运行**

```bash
cp configs/.env.example .env   # 按需修改，标 [必需] 的字段不能留空
export DB_DSN=postgres://app:app@localhost:5432/app?sslmode=disable
make migrate-up
make run-api      # 另开一个终端
make run-worker
```

跑起来之后，浏览器打开 `http://localhost:8080/docs` 看 Redoc 版 API 文档。文档路由默认关闭，
`.env.example` 和 docker-compose 为本地开发打开了（`HTTP_API_DOCS_ENABLED=true`）。

compose 栈里的后台在 `http://localhost:8081`，先创建首个超级管理员，用输出的临时密码登录：

```bash
docker compose -f deploy/docker-compose.yml run --rm api staff create -username root
```

本地运行时后台前端要先构建（`cd web/admin && pnpm install && pnpm build`），开发用
`pnpm dev`，见 [`docs/agents/admin.md`](agents/admin.md)。

## 常用命令

```bash
make setup             # 安装提交钩子（lefthook）
make test              # 单元测试 + 架构测试，秒级，不需要 Docker
make test-integration  # 真实 Postgres（testcontainers）上的集成测试，需要 Docker、Podman 或 socktainer
make lint              # golangci-lint
make generate          # 改了 api/openapi.yaml 后重新生成各模块的 oapi 代码
make check-modules     # 对每个模块演练一次删除
make ci                # 本地复现 CI 主流程
make migrate-up        # 需要先 export DB_DSN
```

完整 target 列表见 `Makefile`。各能力的实现状态见 `docs/ARCHITECTURE.md` 的"已实现 /
规划中"一节。

## 新增一个业务模块

新增一张表并开放到后台或前台，用生成器：写一份资源描述文件，一次生成 migration、模块代码、
spec 和组合根的注册，再补上描述文件里写明的业务逻辑。

```bash
go run ./tools/gen example admin > .gen/category.yaml   # 照着示例填写
go run ./tools/gen check .gen/category.yaml
go run ./tools/gen apply .gen/category.yaml
```

不需要改 `cmd/*/main.go`。完整说明见 [`docs/agents/generator.md`](agents/generator.md)；手写模块见 [`docs/agents/modules.md`](agents/modules.md)。

## 新建业务项目与升级

- 新建：从 starter 的某个 tag clone，保留共同历史（不能复制文件当模板），裁掉不用的模块，改写项目身份文件，
  见 [`docs/agents/project.md`](agents/project.md)；
- 升级：`make starter-upgrade VERSION=<tag>`，按提示处理后用 `CONTINUE=1` 续跑，见
  [`docs/agents/upgrade.md`](agents/upgrade.md)。

## git blame

纯搬迁和批量注释清理的提交登记在 `.git-blame-ignore-revs`，本地执行一次
`git config blame.ignoreRevsFile .git-blame-ignore-revs` 让 `git blame` 跳过它们。
