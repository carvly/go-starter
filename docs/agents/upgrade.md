# starter 发版与业务项目升级

事实来源：`starter-changelog.md`、`Makefile`、`scripts/starter-upgrade-entry.sh`、`scripts/starter-upgrade.sh`、`tools/starter/`

## starter 发版

使用 semver tag。`v0.x` 的破坏性改动升 minor，兼容修复升 patch；破坏性改动必须在
`starter-changelog.md` 写清需要修改的文件、命令、验证方式，以及执行时机。

starter 的发版说明写在 `starter-changelog.md`（根目录 `CHANGELOG.md` 留给业务项目）。升级说明的格式是机器读取的，必须照写：版本标题 `## vX.Y.Z ...`，其下的小节标题只能是
`#### 合并前`、`#### 合并后迁移前`、`#### 迁移后`、`#### 部署后`（`tools/starter/notes.go` 的
`upgradeNotes`）。升级时只读取基线之后、到目标为止的版本段；某个小节有内容，升级才会在对应的点停下等确认，
没有就直接走过。发版前 starter 和示例项目都要通过 CI。`make starter-upgrade` 的入口契约
（fetch → 目标 tag 临时 worktree → 执行目标脚本）不能改变。

## 正常升级

```bash
make starter-upgrade VERSION=v0.1.0
# 脚本停下要求处理手动事项或确认后：
make starter-upgrade VERSION=v0.1.0 CONTINUE=1
```

目标版本的 `tools/starter` 在临时只读 worktree 中执行，状态保存在项目 git 目录的
`starter-upgrade/state.json`（经 `git rev-parse --git-path` 定位，项目在 git worktree 里也能用）。
裁剪、归属比较和重放都由同一个 `tools/starter` 完成。阶段依次是：

1. `precheck`：打印升级说明，有"合并前"事项时停下；
2. `drift`：检查 starter 文件和 spec 漂移，有漂移时停下；
3. `merge`：merge 目标版本，然后把**全部** starter 文件取为目标版本（不论是否冲突）；会重新生成的文件冲突时
   自动处理：派生文件（`*.gen.*`、`schema.d.ts`）取目标版本，`go.sum` 取两边的并集（之后 `go mod tidy` 收口）；
   共享或项目文件还有冲突时停下；
4. `replay`：重放已删除模块，`go generate`，后台前端 `gen:api`、`gen:routes`；
5. `build`：有"合并后迁移前"事项时先停下，业务代码在这里改到能编译；然后 `go mod tidy`、`go build ./...`；
6. `migrate`：`go run ./cmd/api migrate up`。迁移的是当前环境的 `DB_DSN`，shell 里已有的变量优先于
   `.env`，升级前确认它指向本地库；
7. `after-migrate`：有"迁移后"事项时停下；
8. `verify`：`go test ./...` 和模块删除演练；
9. `complete`：打印下一步。工具不提交，由开发者复核并提交，重新应用 `project/starter-patches.md`
   里仍需要的补丁。之后用新的 `VERSION` 执行就是下一次升级，基线是刚完成的目标。

目标与基线首次运行后固定，续跑不能换目标；已有 `MERGE_HEAD` 时不会重复 merge。某一步失败时修好再执行同一命令，
从失败的阶段重来。

停点有五个：升级说明的"合并前""合并后迁移前""迁移后"，项目改过 starter 文件（漂移），以及合并冲突。漂移只算
"相对基线改过、且和目标版本也不一样"的文件——合并后它们会恢复为目标版本；已经和目标版本内容相同的（例如提前同步了
上游修复）不会丢东西，不算漂移。共享 spec 里被项目改过的 starter 操作和 schema 也会列出，但 spec 是共享文件，保留
三方合并结果、不会恢复，需要合并后人工与目标版本对齐。停下时状态里记下 `pending`，`CONTINUE=1` 只确认这一个点，下一个有内容的确认点照样
会停；停下时会再打印该小节的升级说明。`starter-removed.yaml` 里有目标版本不存在的模块（比如被改名）时，重放直接
报错，不会把模块悄悄加回来。

停下和完成都以 0 退出，故障以非 0 退出（make 会把非 0 统一改成 2）。脚本和 Agent 读输出里以
`starter-upgrade-status:` 开头的一行判断结果：`stopped point=<停点>`（停点为上面的小节名、`drift` 或
`conflict`）或 `complete target=<版本>`；没有这一行就是故障，看前面的错误输出。

共享来源（两份 spec、`go.mod`、`package.json`、compose 等）保留三方合并结果。若有文本冲突，
解决并暂存后只需用 `CONTINUE=1` 重新执行固定入口；不要在带冲突标记的来源上运行生成器。`pnpm-lock.yaml`
是解析结果，不能按行合并：先解决 `package.json`，再以项目一侧为起点（`git checkout --ours`），在 `web/admin`
执行 `pnpm install` 重新解析后暂存——业务依赖保持项目实际在用的版本，starter 依赖按合并后的 `package.json` 解析。

## v0.1.0 之前的首次升级

旧项目没有 make 入口时，不使用旧 Makefile，也不手工 merge。直接执行同一自举命令：

```bash
git fetch starter --tags
tmp="$(mktemp -d)"
git worktree add --detach "$tmp/target" v0.1.0
PROJECT_ROOT="$PWD" TARGET_ROOT="$tmp/target" CONTINUE= "$tmp/target/scripts/starter-upgrade.sh"
git worktree remove --force "$tmp/target"
rm -rf "$tmp"
```

没有已合并 starter tag 时以 `git merge-base HEAD <目标>` 为基线。首次完成后即可使用固定
make 入口。只想要某个修复时可以 `git cherry-pick`。

## 升级改动的验收矩阵

修改升级工具、删除执行器或状态机时，必须用临时 Git 仓库和真实数据库保留以下持久测试：
每个确认点只被它自己那一次 `CONTINUE=1` 确认（`TestUpgradeStopsAtEachConfirmationOnce`）；完成并提交后连续升级到
下一个版本（`TestUpgradeTwiceInSequence`）；合并时没冲突的漂移文件也恢复为目标版本
（`TestUpgradeRestoresCleanlyMergedStarterFiles`）；业务代码需要修改时停在"合并后迁移前"而不是 tidy 失败
（`TestUpgradeStopsForCodeFixBeforeBuild`）；spec 漂移提示不说会恢复（`TestUpgradeDriftMessageKeepsSpecMergeResult`）；
派生文件和 `go.sum` 冲突不拦住升级（`TestUpgradeResolvesRegeneratedConflicts`）；目标工具发生变化；starter 文件与 spec 漂移；merge 非冲突失败；共享 spec
冲突解决后续跑；删除 user/staff 后重放；不同目标拒绝续跑；副本权限和可执行脚本正确。测试必须区分
“发现漂移”和“工具故障”，不能只验证正常路径。

## 部署后

所有实例都升级后，再执行说明中的部署后步骤。删除过模块时运行 `api queue cancel-orphans`；
worker 启动日志也会对缺少 worker 的存量任务给出提醒。
