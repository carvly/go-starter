import { describe, expect, it } from "vitest";

import { ApiError } from "@/api/errors";

import { describeError, errorMessage, fieldError, handledByCaller, registerErrorMessages, serverField } from "./server-error";

describe("describeError", () => {
  it("会话失效去登录，但登录本身的 401 只提示", () => {
    expect(describeError(new ApiError(401, "session_invalid", ""), "query")).toEqual({ kind: "login" });
    expect(describeError(new ApiError(401, "invalid_credentials", ""), "mutation").kind).toBe("notify");
    expect(describeError(new ApiError(401, "temporary_password_expired", ""), "mutation").kind).toBe("notify");
  });

  it("需要改密去改密页", () => {
    expect(describeError(new ApiError(403, "password_change_required", ""), "mutation")).toEqual({ kind: "change-password" });
  });

  it("读数据被拒去无权限页，写操作被拒只提示", () => {
    expect(describeError(new ApiError(403, "permission_denied", ""), "query")).toEqual({ kind: "forbidden" });
    expect(describeError(new ApiError(403, "permission_denied", ""), "mutation")).toEqual({
      kind: "notify",
      message: "没有权限执行这个操作",
    });
  });

  it("不是 ApiError 的错误（网络中断）只提示", () => {
    expect(describeError(new TypeError("Failed to fetch"), "query").kind).toBe("notify");
  });
});

describe("handledByCaller", () => {
  it("true 接管全部错误，错误码数组只接管列出的码", () => {
    const stock = new ApiError(409, "insufficient_stock", "");
    expect(handledByCaller(true, new TypeError("x"))).toBe(true);
    expect(handledByCaller(["insufficient_stock"], stock)).toBe(true);
    expect(handledByCaller(["insufficient_stock"], new ApiError(401, "session_invalid", ""))).toBe(false);
    expect(handledByCaller(["insufficient_stock"], new TypeError("Failed to fetch"))).toBe(false);
    expect(handledByCaller(undefined, stock)).toBe(false);
    expect(handledByCaller(false, stock)).toBe(false);
  });
});

describe("fieldError", () => {
  it("只认列出的错误码，返回中文提示", () => {
    expect(fieldError(new ApiError(409, "username_taken", ""), ["username_taken"])).toBe("用户名已被占用");
    expect(fieldError(new ApiError(409, "role_name_taken", ""), ["username_taken"])).toBeUndefined();
    expect(fieldError(new TypeError("x"), ["username_taken"])).toBeUndefined();
  });
});

describe("errorMessage", () => {
  it("已知错误码用中文，未知错误码用后端文案，5xx 不透出细节", () => {
    expect(errorMessage(new ApiError(409, "username_taken", "username already taken"))).toBe("用户名已被占用");
    expect(errorMessage(new ApiError(400, "something_new", "server says"))).toBe("server says");
    expect(errorMessage(new ApiError(500, "internal_error", "Internal Server Error"))).toBe("服务器异常，请稍后再试");
  });

  it("登录失败按情况提示：密码错误只说密码错误，等待中说要等多久", () => {
    expect(errorMessage(new ApiError(401, "invalid_credentials", ""))).toBe("用户名或密码不正确");
    expect(errorMessage(new ApiError(429, "login_throttled", "", 30))).toBe("用户名或密码连续输错多次，请 30 秒后再试");
    expect(errorMessage(new ApiError(429, "login_throttled", "", 61))).toBe("用户名或密码连续输错多次，请 2 分钟后再试");
    expect(errorMessage(new ApiError(429, "login_throttled", ""))).toBe("用户名或密码连续输错多次，请稍后再试");
  });
});

describe("registerErrorMessages", () => {
  it("业务 feature 注册自己的错误文案", () => {
    registerErrorMessages({ feature_error: "业务错误" });
    expect(errorMessage(new ApiError(409, "feature_error", "internal detail"))).toBe("业务错误");
  });
});

describe("serverField", () => {
  const fields = { widget_name_taken: "name" } as const;

  it("映射里的错误码返回字段名和提示", () => {
    expect(serverField(new ApiError(409, "widget_name_taken", "名称已被占用"), fields)).toEqual({ name: "name", message: "名称已被占用" });
  });

  it("其它错误返回 undefined，交给统一错误处理", () => {
    expect(serverField(new ApiError(409, "widget_conflict", ""), fields)).toBeUndefined();
    expect(serverField(new TypeError("Failed to fetch"), fields)).toBeUndefined();
  });
});
