import { describe, expect, it } from "vitest";

import { describeAction } from "./describe";

describe("describeAction", () => {
  it("登录和命令行动作翻译成中文，写操作保留路由模板", () => {
    expect(describeAction("login_throttled")).toBe("登录被拒绝（失败次数过多）");
    expect(describeAction("cli:staff create")).toBe("命令行：staff create");
    expect(describeAction("POST /admin/v1/accounts/:id/disable")).toBe("POST /admin/v1/accounts/:id/disable");
  });
});
