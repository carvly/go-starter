# 更新日志

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
