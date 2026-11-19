import { describe, expect, it } from "vitest";

import { changePasswordSchema, safeRedirect } from "./schemas";

describe("safeRedirect", () => {
  it("只接受站内路径", () => {
    expect(safeRedirect("/accounts?page=2")).toBe("/accounts?page=2");
    expect(safeRedirect(undefined)).toBe("/");
    expect(safeRedirect("https://evil.example.com")).toBe("/");
    expect(safeRedirect("//evil.example.com")).toBe("/");
    expect(safeRedirect("/login?redirect=/x")).toBe("/");
  });
});

describe("changePasswordSchema", () => {
  const ok = { current_password: "temp-password", new_password: "a-long-new-password", confirm_password: "a-long-new-password" };
  it("校验长度、两次一致、不能与当前密码相同", () => {
    expect(changePasswordSchema.safeParse(ok).success).toBe(true);
    expect(changePasswordSchema.safeParse({ ...ok, new_password: "short", confirm_password: "short" }).success).toBe(false);
    expect(changePasswordSchema.safeParse({ ...ok, confirm_password: "different-password" }).success).toBe(false);
    expect(changePasswordSchema.safeParse({ ...ok, current_password: ok.new_password }).success).toBe(false);
  });
});
