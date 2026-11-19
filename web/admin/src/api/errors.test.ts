import { describe, expect, it } from "vitest";

import { ApiError, call } from "./errors";

const res = (status: number) => new Response(null, { status });

async function failure(result: { error?: unknown; response: Response }): Promise<ApiError> {
  try {
    await call(Promise.resolve(result));
  } catch (e) {
    return e as ApiError;
  }
  throw new Error("call 应该抛出 ApiError");
}

describe("call", () => {
  it("成功返回数据，204 返回 undefined", async () => {
    expect(await call(Promise.resolve({ data: { a: 1 }, response: res(200) }))).toEqual({ a: 1 });
    expect(await call(Promise.resolve({ response: res(204) }))).toBeUndefined();
  });

  it("后端的错误体转成 ApiError", async () => {
    const err = await failure({ error: { code: "username_taken", message: "taken" }, response: res(409) });
    expect(err).toBeInstanceOf(ApiError);
    expect([err.status, err.code, err.message]).toEqual([409, "username_taken", "taken"]);
  });

  it("需要等待的错误带上 retry_after", async () => {
    const err = await failure({ error: { code: "login_throttled", message: "wait", retry_after: 120 }, response: res(429) });
    expect([err.code, err.retryAfter]).toEqual(["login_throttled", 120]);
    expect((await failure({ error: { code: "x", message: "m" }, response: res(400) })).retryAfter).toBeUndefined();
  });

  it("非 JSON 的错误体（反向代理的页面）给出带状态码的通用文案，不是空字符串", async () => {
    const err = await failure({ error: "<html>", response: res(413) });
    expect([err.code, err.message]).toEqual(["http_error", "请求失败（413）"]);
  });
});
