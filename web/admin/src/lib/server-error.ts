import { ApiError } from "@/api/errors";

// 后端错误码 → 给人看的中文提示。没有列出的错误码用后端返回的 message。
const messages: Record<string, string> = {
  invalid_credentials: "用户名或密码不正确",
  temporary_password_expired: "临时密码已过期，请联系管理员重设",
  session_invalid: "登录已失效，请重新登录",
  password_change_required: "请先修改密码",
  permission_denied: "没有权限执行这个操作",
  wrong_password: "当前密码不正确",
  cannot_modify_self: "不能停用自己，也不能取消自己的超级管理员",
  last_super_admin: "至少要保留一个启用的超级管理员",
  username_taken: "用户名已被占用",
  role_name_taken: "角色名已被占用",
  unknown_permission: "包含不存在的权限码",
  invalid_time_range: "开始时间必须早于结束时间",
  page_out_of_range: "只能翻到前 10000 条，请加筛选条件缩小范围",
  invalid_input: "输入不合法",
  invalid_request: "输入不合法",
};

export function registerErrorMessages(entries: Record<string, string>) {
  Object.assign(messages, entries);
}

// 错误发生后应该做什么：去登录、去改密、显示无权限页，或者只提示。
export type ErrorAction = { kind: "login" } | { kind: "change-password" } | { kind: "forbidden" } | { kind: "notify"; message: string };

// describeError 只做判断，不产生副作用，方便测试。forbidden 只用于读取数据（页面打不开）；
// 写操作被拒绝时提示即可，页面本身还能用。
export function describeError(error: unknown, source: "query" | "mutation"): ErrorAction {
  if (!(error instanceof ApiError)) return { kind: "notify", message: "网络错误或服务器异常，请稍后再试" };
  if (error.code === "password_change_required") return { kind: "change-password" };
  if (error.status === 401 && error.code !== "invalid_credentials" && error.code !== "temporary_password_expired") {
    return { kind: "login" };
  }
  if (error.status === 403 && error.code === "permission_denied" && source === "query") return { kind: "forbidden" };
  return { kind: "notify", message: errorMessage(error) };
}

// HandlesErrors 是 mutation 的 meta.handlesErrors：true 表示调用方自己展示全部错误（登录、改密
// 的表单）；错误码数组表示只有这几个由调用方展示（通常显示在对应字段下），其余照常统一处理。
export type HandlesErrors = boolean | readonly string[];

export function handledByCaller(handles: HandlesErrors | undefined, error: unknown): boolean {
  if (handles === true) return true;
  return Array.isArray(handles) && error instanceof ApiError && handles.includes(error.code);
}

// fieldError：error 是 codes 里的错误码时返回提示文案（交给表单显示在字段下），否则 undefined。
// codes 同时写进对应 mutation 的 meta.handlesErrors，统一错误处理才不会再弹一次。
export function fieldError(error: unknown, codes: readonly string[]): string | undefined {
  return error instanceof ApiError && codes.includes(error.code) ? errorMessage(error) : undefined;
}

// serverField 同 fieldError，按错误码 → 字段名的映射找出该显示在哪个字段下（生成的表单用）。映射的键
// 同时写进 mutation 的 meta.handlesErrors。
export function serverField<F extends string>(
  error: unknown,
  fields: Partial<Record<string, F>>,
): { name: F; message: string } | undefined {
  if (!(error instanceof ApiError)) return undefined;
  const name = fields[error.code];
  return name ? { name, message: errorMessage(error) } : undefined;
}

// waitText 把剩余秒数写成"30 秒""3 分钟"（不足一分钟按秒，否则向上取整到分钟）。
function waitText(seconds: number): string {
  return seconds < 60 ? `${seconds} 秒` : `${Math.ceil(seconds / 60)} 分钟`;
}

export function errorMessage(error: unknown): string {
  if (!(error instanceof ApiError)) return "网络错误或服务器异常，请稍后再试";
  if (error.status >= 500) return "服务器异常，请稍后再试";
  if (error.code === "login_throttled") {
    return error.retryAfter ? `用户名或密码连续输错多次，请 ${waitText(error.retryAfter)}后再试` : "用户名或密码连续输错多次，请稍后再试";
  }
  return messages[error.code] ?? error.message;
}
