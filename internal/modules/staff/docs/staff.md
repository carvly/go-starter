# staff 模块：后台员工

事实来源：`internal/modules/staff/`

改 `internal/modules/staff/` 之前读这篇；两套身份体系的划分和给后台操作加权限见 `docs/agents/auth.md`，后台接入面见
`docs/agents/http.md`，后台前端见 `docs/agents/admin.md`。本文随模块一起删除。

## 职责与规则

后台账号和前台用户是两套独立体系：账号只能由超级管理员（或 CLI）创建，用户名 + 密码登录，
服务端会话 cookie，没有 JWT 和 refresh。实现在 `internal/modules/staff/`，接口在
`api/admin.yaml`（tag `staff`）。

账号、会话、角色权限放在同一个 staff 模块：每个请求都要同时用到会话、账号状态和权限，拆成两个模块就不能
JOIN（表归属规则），还要经适配器互相调用。

- **首个超级管理员**用 CLI 创建：`api staff create -username root`，输出临时密码和它的过期
  时间（`STAFF_TEMP_PASSWORD_TTL`，默认 24 小时），首次登录后必须改密；
  `api staff reset-password -username root` 是所有超级管理员都进不来时的恢复手段（在一个事务里
  重设密码、启用账号、吊销全部会话）。CLI 操作记操作日志，操作人为空；
- **会话**：登录成功下发 `__Host-admin_session`（HttpOnly、Secure、SameSite=Strict、Path=/），
  库里只存 token 的 sha256 摘要。每个请求用一条查询取出会话和账号状态，不缓存：吊销、停用、
  空闲超时（`STAFF_SESSION_IDLE_TIMEOUT`，默认 30 分钟）、绝对过期（`STAFF_SESSION_MAX_AGE`，
  默认 12 小时）立即生效，查询失败也拒绝。改密吊销其它会话，重设密码和停用吊销全部会话；
- **登录**：用户名不存在、密码错误、账号停用返回同一个 401 `invalid_credentials`，不存在时也
  做一次哑比较，响应时间不暴露账号是否存在。退避按"用户名摘要 + IP"计数，不管账号是否存在：
  同一用户名在同一 IP 连续失败 5 次开始等待，从 30 秒起逐次翻倍，最长 15 分钟，等待期内返回
  429 `login_throttled`（带 `retry_after`），也做一次哑比较。不存在的用户名一样进入等待，所以
  单独返回等待不会暴露账号是否存在；以后改退避逻辑时这一点不能丢。只数这个 IP 上次登录成功和
  上次设置密码之后的失败（别的 IP 登录成功不清零，重设密码清零），计数从 `staff_audit_logs`
  的 `login_key`（规范化用户名的 sha256，不存原文）里数，不另建表；不做硬锁定。存在的账号的
  登录在账号行锁上排队，并发请求绕不过阈值。后台登录页按情况提示（`web/admin/src/lib/server-error.ts`）：
  "用户名或密码不正确"，或"用户名或密码连续输错多次，请 N 分钟后再试"；
- **鉴权中间件**（`internal/modules/staff/adminapi/guard.go`，经组合根 `adminAuth()` 注入后台
  server）：按"请求方法 + 路由模板"查 `x-permission`（组合根从内嵌的 `admin.yaml` 解析，
  `internal/app/admin_spec.go`）→ 路径不存在 404、查不到映射拒绝 → `public` 放行 → 会话校验 →
  必须改密时只放行改密、登出、查看自己 → 权限检查（`authenticated` 放行，`superadmin` 只放行
  超级管理员，权限码每次查库，超级管理员短路）→ 为写操作记日志（方法 + 路由模板、路径参数、
  状态码、IP、request_id，不记请求体）：会话校验通过之后被拒绝的写请求也记，未登录的不记。
  路径存在但方法不对返回 405；
- **权限码**由代码定义（`admin.yaml` 的 `x-permission`），角色在运行时配置，只存权限码。权限码
  每次查库、不缓存：和会话一样，收回角色立即生效；Redis 是可选的，权限依赖缓存会让没配 Redis 或
  Redis 故障时整个后台不可用。超级管理员短路、员工和角色只有超级管理员能管理，不采用"只能授予自己
  已有的权限"：目标是个人或小团队，不下放管理权限，这样不存在普通员工给自己加权限的路径；
  spec 里删掉的权限码在检查和展示时忽略。员工和角色只有超级管理员能管理；超级管理员不能停用
  自己、不能取消自己的标记；停用或取消超级管理员时在锁里检查至少留下一个 active 的超级管理员
  （`Repository.LockActiveSuperAdmins`），两个超级管理员同时互相停用也不会全部失效。员工列表
  第一页返回的 `active_super_admins`（`CountActiveSuperAdmins`，不加锁）只用于页面提示，
  不能用来做这类检查；
- **操作日志**按保留期（`STAFF_AUDIT_RETENTION`，默认 180 天）由每天一次的定时任务删除。日志的
  `target` 可能是前台用户 ID，但不含其它个人信息，且按保留期删除，所以 staff 不登记注销清理端口。
  操作日志直接写库、不经 river：入队本身也是一次 INSERT，代价和直接写差不多，还多一次 worker 写入。

## 离职交接与应急恢复

系统的最终权限在能执行 CLI 的人（有部署环境和数据库权限）手里，不在后台的某个账号上。后台的
"至少留下一个 active 超级管理员"只能防止超级管理员一个都不剩，防不了"剩下的那个人走了"，
这一层靠流程：

- 平时至少保持两个 active 的超级管理员；
- 员工离职时，由另一个超级管理员先停用他的账号（停用会立即吊销全部会话），再交接；员工不能
  删除，操作日志和 `created_by` 仍然指向他；
- 唯一的超级管理员已经离开、或所有超级管理员都进不来时，运维执行
  `api staff reset-password -username <留任的员工>`（容器里是
  `docker compose run --rm api staff reset-password -username <留任的员工>`）：重设密码、
  启用账号、吊销旧会话（这个命令不改变超级管理员标记，所以要对一个超级管理员账号执行，比如
  之前被停用的另一个超级管理员）；留任的人里一个超级管理员都没有时，用
  `api staff create -username <新登录名>` 新建一个。登录改密后，停用离职者的账号；
- CLI 操作记入操作日志（动作 `cli:staff reset-password`，操作人为空），事后可查。
