// describeAction 把日志的动作换成人看的说明；写操作保留"方法 + 路由模板"原样，
// 它和接口文档一一对应，便于查是哪个接口。
export function describeAction(action: string): string {
  switch (action) {
    case "login_succeeded":
      return "登录成功";
    case "login_failed":
      return "登录失败";
    case "login_throttled":
      return "登录被拒绝（失败次数过多）";
  }
  if (action.startsWith("cli:")) return `命令行：${action.slice(4)}`;
  return action;
}
