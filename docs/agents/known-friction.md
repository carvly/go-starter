# 已知的摩擦

还没解决的摩擦：AI 做事时遇到、但没能在当次改动里修掉的问题。遇到环境、工具或检查的问题时
先看这里，不是每个任务都要读。怎么记、什么时候删见 [workflow.md](workflow.md) 的“任务后的
摩擦回顾”。

每条写：发现日期、信号（见 workflow.md 的信号表）、影响（会让人或 AI 在哪一步出错）、建议的
反哺去向（文档、检查、报错文字、脚本）。解决后删掉条目，解决的结果留在文档和检查里，不在这里
留记录。条目超过三个月还没处理的，重新判断：要么定下反哺去向，要么写明为什么接受它。

## 条目

### `gen apply` 要求工作区干净，和"只在用户要求时提交"冲突

- 发现：2026-11-10（用生成器生成 `auth.user` 时）。
- 信号：流程互相矛盾。
- 影响：同一个任务里先改了别的文件（如删掉旧模块、改生成器）再生成时，`gen apply` 因为已跟踪的文件
  有修改而退出（码 2）；硬规则 10 又不允许 AI 自行提交或 `git stash`（会把用户的改动藏起来）。
- 现在的做法：把工作区复制到临时目录（`git ls-files -co --exclude-standard`，并链接
  `web/admin/node_modules`），在副本里提交一次再 `gen apply`，把新增和修改的文件、`.gen/<资源>/`
  拷回工作区；要重新生成时先回退这些产物。
- 建议的反哺去向：生成器加一个 `--allow-dirty`（只检查要写的路径和被插入的共享文件没有修改，回退
  命令改为按快照精确还原插入的内容），或者内置"在副本里生成再拷回"；任一做法都要改 `generator.md`
  的流程。

### React Feature 之间直接引用内部实现

- 发现：2026-11-19（审核 `docs/ARCHITECTURE.md` 的 Feature 边界规范时）。
- 信号：违规发现得晚。
- 现状：`web/admin/src/features/home/home-page.tsx` 直接引用 `features/auth/queries` 和
  `features/staff/role-guide`；`features/staff/accounts-page.tsx`、`features/roles/roles-page.tsx`、
  `features/audit-log/audit-log-page.tsx` 等也有跨 Feature 内部引用。`routes/` 是规范允许的页面组合点，
  不在本条范围内。
- 影响：Feature 的内部文件路径和实现细节成为隐式公开契约，重构或删除一个 Feature 时会影响其他
  Feature；AI 新增功能时容易沿用这些引用，继续扩大耦合。
- 现在的做法：新代码遵守架构规范，不继续增加跨 Feature 内部引用；现有引用在触及相应页面时收敛，
  不为清债单独重写无关页面。
- 建议的反哺去向：为确实需要复用的能力定义最小公开出口（只给有外部消费者的 Feature 增加公开文件，
  不强制所有 Feature 建 `index.ts`），或把纯组合移到路由/组合层；在 oxlint 中增加 Feature 间私有路径
  引用检查，并用构造违规输入的测试防止规则失效。完成后同步 `docs/agents/admin.md` 并删除本条。

### 通用后台 layout 反向依赖 auth Feature

- 发现：2026-11-19（审核 `docs/ARCHITECTURE.md` 的通用目录依赖方向时）。
- 信号：违规发现得晚。
- 现状：`web/admin/src/components/layout/app-layout.tsx` 和 `use-visible-nav.ts` 直接引用
  `features/auth/queries`，通用 `components/layout` 因而依赖业务 Feature。
- 影响：通用外壳不能独立复用或随后台能力干净删除；auth 的内部查询实现变化会波及布局，AI 也可能把
  更多业务状态继续放进通用组件。
- 现在的做法：新代码不向通用目录增加 Feature 依赖；修改相关布局时优先通过组合层传入数据，或使用
  明确的会话公开能力，不为防腐增加无语义转换的转发层。
- 建议的反哺去向：把会话数据获取移到应用组合层，或由稳定的公开会话契约提供；在 oxlint 中禁止
  `components/`、`lib/` 反向 import `features/`（路由组合点除外），同步 `docs/agents/admin.md` 并删除本条。
