# go-starter

生产级 Go 后端 starter：基于 [Echo v5](https://echo.labstack.com/)，按业务模块垂直切分（每个模块自带 domain、
存储、HTTP、任务），模块之间只经组合根粘合，依赖方向由架构测试强制。在此之上补齐了鉴权、缓存、可观测性、
HTTP 安全与流量控制、队列/多实例协调、健康检查、Docker 部署产物、CI 等"几乎每个新 Go 后端服务都要回答一次"的问题。

- 怎么跑起来、常用命令、新建业务项目与升级：[`docs/STARTER.md`](docs/STARTER.md)
- 结构、依赖方向、已实现的能力：[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
- 改代码前读哪篇：[`AGENTS.md`](AGENTS.md)
- 发版说明：[`starter-changelog.md`](starter-changelog.md)

基于 starter 的业务项目把这份 README 改写成项目自己的说明，starter 的使用说明留在 `docs/STARTER.md`。

## 许可证

MIT，见 `LICENSE`。
