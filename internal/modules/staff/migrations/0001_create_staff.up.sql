-- 后台员工：账号、会话、角色权限、操作日志，全部归 staff 模块。
-- 唯一冲突按约束名翻译成 ErrUsernameTaken / ErrRoleNameTaken
-- （internal/modules/staff/postgres/repository.go），改名要同步改那里。

CREATE TABLE staff_accounts (
    id UUID,
    username TEXT NOT NULL,
    display_name TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    super_admin BOOLEAN NOT NULL DEFAULT FALSE,
    must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
    temp_password_expires_at TIMESTAMPTZ,
    password_changed_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT pk_staff_accounts PRIMARY KEY (id),
    CONSTRAINT uk_staff_accounts_username UNIQUE (username),
    CONSTRAINT ck_staff_accounts_display_name CHECK (char_length(display_name) <= 100)
);

COMMENT ON TABLE staff_accounts IS '后台员工账号，只能由超级管理员或 CLI 创建';
COMMENT ON COLUMN staff_accounts.id IS '员工 ID，应用层生成的 UUIDv7';
COMMENT ON COLUMN staff_accounts.username IS '登录名，domain 层已规范化为去空白的小写';
COMMENT ON COLUMN staff_accounts.display_name IS '显示名';
COMMENT ON COLUMN staff_accounts.password_hash IS 'Argon2id 密码哈希，不存明文';
COMMENT ON COLUMN staff_accounts.super_admin IS '超级管理员：权限检查直接放行，只有超级管理员能管理员工和角色';
COMMENT ON COLUMN staff_accounts.must_change_password IS '为 TRUE 时只放行改密码、登出、查看自己';
COMMENT ON COLUMN staff_accounts.temp_password_expires_at IS '临时密码的过期时间；NULL 表示当前密码不是临时密码';
COMMENT ON COLUMN staff_accounts.password_changed_at IS '最后一次设置密码的时间（创建、改密、重设）；登录失败退避只数这之后的失败';
COMMENT ON COLUMN staff_accounts.status IS '状态：active 正常，disabled 停用（不能登录，已有会话立即失效）';
COMMENT ON COLUMN staff_accounts.created_by IS '创建者的员工 ID（staff_accounts.id），无外键；NULL 表示由 CLI 创建';
COMMENT ON COLUMN staff_accounts.created_at IS '创建时间';
COMMENT ON COLUMN staff_accounts.updated_at IS '最后更新时间';

CREATE TABLE staff_sessions (
    id UUID,
    token_hash TEXT NOT NULL,
    staff_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    ip TEXT NOT NULL,
    user_agent TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT pk_staff_sessions PRIMARY KEY (id),
    CONSTRAINT uk_staff_sessions_token_hash UNIQUE (token_hash)
);

-- 服务"列出某员工的会话"和"吊销某员工的全部会话"。每个请求的会话校验走 uk_staff_sessions_token_hash。
CREATE INDEX idx_staff_sessions_staff_id_created_at ON staff_sessions (staff_id, created_at DESC);

COMMENT ON TABLE staff_sessions IS '后台员工的服务端会话；吊销只标记 revoked_at，不删除行';
COMMENT ON COLUMN staff_sessions.id IS '会话 ID，应用层生成的 UUIDv7，对外暴露用于吊销单个会话';
COMMENT ON COLUMN staff_sessions.token_hash IS 'cookie 里会话 token 的 sha256 十六进制摘要，库里不存 token 明文';
COMMENT ON COLUMN staff_sessions.staff_id IS '员工 ID（staff_accounts.id），无外键';
COMMENT ON COLUMN staff_sessions.created_at IS '登录时间';
COMMENT ON COLUMN staff_sessions.last_used_at IS '最后使用时间，超过 1 分钟才更新；用于空闲超时';
COMMENT ON COLUMN staff_sessions.expires_at IS '绝对过期时间';
COMMENT ON COLUMN staff_sessions.revoked_at IS '吊销时间；NULL 表示未吊销';
COMMENT ON COLUMN staff_sessions.ip IS '登录时的客户端 IP';
COMMENT ON COLUMN staff_sessions.user_agent IS '登录时的 User-Agent';
COMMENT ON COLUMN staff_sessions.updated_at IS '最后更新时间';

CREATE TABLE staff_roles (
    id UUID,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT pk_staff_roles PRIMARY KEY (id),
    CONSTRAINT uk_staff_roles_name UNIQUE (name),
    CONSTRAINT ck_staff_roles_name CHECK (char_length(name) <= 100)
);

COMMENT ON TABLE staff_roles IS '后台角色：一组权限码，运行时配置';
COMMENT ON COLUMN staff_roles.id IS '角色 ID，应用层生成的 UUIDv7';
COMMENT ON COLUMN staff_roles.name IS '角色名';
COMMENT ON COLUMN staff_roles.description IS '说明';
COMMENT ON COLUMN staff_roles.created_at IS '创建时间';
COMMENT ON COLUMN staff_roles.updated_at IS '最后更新时间';

CREATE TABLE staff_role_permissions (
    role_id UUID NOT NULL,
    permission TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT pk_staff_role_permissions PRIMARY KEY (role_id, permission)
);

COMMENT ON TABLE staff_role_permissions IS '角色拥有的权限码';
COMMENT ON COLUMN staff_role_permissions.role_id IS '角色 ID（staff_roles.id），无外键';
COMMENT ON COLUMN staff_role_permissions.permission IS '权限码，<资源>:<动作>，取自 api/admin.yaml 的 x-permission';
COMMENT ON COLUMN staff_role_permissions.created_at IS '授予时间';

CREATE TABLE staff_account_roles (
    staff_id UUID NOT NULL,
    role_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT pk_staff_account_roles PRIMARY KEY (staff_id, role_id)
);

-- 服务删除角色时解除它和员工的绑定。按员工查角色走主键前缀。
CREATE INDEX idx_staff_account_roles_role_id ON staff_account_roles (role_id);

COMMENT ON TABLE staff_account_roles IS '员工绑定的角色，有效权限取并集';
COMMENT ON COLUMN staff_account_roles.staff_id IS '员工 ID（staff_accounts.id），无外键';
COMMENT ON COLUMN staff_account_roles.role_id IS '角色 ID（staff_roles.id），无外键';
COMMENT ON COLUMN staff_account_roles.created_at IS '绑定时间';

CREATE TABLE staff_audit_logs (
    id UUID,
    staff_id UUID,
    login_key TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    target TEXT NOT NULL,
    result TEXT NOT NULL,
    ip TEXT NOT NULL,
    trace_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT pk_staff_audit_logs PRIMARY KEY (id)
);

-- 服务登录失败退避：数同一用户名摘要、同一 IP 上 login_failed 的次数，以及取最近一次
-- login_succeeded 的时间。
CREATE INDEX idx_staff_audit_logs_login_key_ip_action_created_at ON staff_audit_logs (login_key, ip, action, created_at);
-- 服务按员工筛选的日志分页查询。
CREATE INDEX idx_staff_audit_logs_staff_id_created_at_id ON staff_audit_logs (staff_id, created_at DESC, id DESC);
-- 服务操作日志的分页查询和按保留期删除。
CREATE INDEX idx_staff_audit_logs_created_at_id ON staff_audit_logs (created_at DESC, id DESC);

COMMENT ON TABLE staff_audit_logs IS '后台操作日志：写操作、登录成功与失败、CLI 操作；按保留期删除';
COMMENT ON COLUMN staff_audit_logs.id IS '日志 ID，应用层生成的 UUIDv7';
COMMENT ON COLUMN staff_audit_logs.staff_id IS '操作的员工 ID（staff_accounts.id），无外键；NULL 表示 CLI 操作或登录时账号不存在';
COMMENT ON COLUMN staff_audit_logs.login_key IS '登录时输入的用户名（去空格、转小写）的 sha256，不存原文；账号不存在时也有，用于按用户名 + IP 计数退避。非登录日志为空字符串';
COMMENT ON COLUMN staff_audit_logs.action IS '动作：请求方法 + 路由模板（如 POST /admin/v1/accounts），或 login_succeeded、login_failed、login_throttled、cli:<命令>';
COMMENT ON COLUMN staff_audit_logs.target IS '目标：路径参数（如被操作的员工 ID），没有时为空字符串；不记录请求体';
COMMENT ON COLUMN staff_audit_logs.result IS '结果：HTTP 状态码，或登录、CLI 的结果说明';
COMMENT ON COLUMN staff_audit_logs.ip IS '客户端 IP；CLI 操作为空字符串';
COMMENT ON COLUMN staff_audit_logs.trace_id IS '请求的 trace_id，与网关访问日志、应用日志、trace 关联；CLI 操作为空字符串';
COMMENT ON COLUMN staff_audit_logs.created_at IS '发生时间';
