import { describe, expect, it } from "vitest";

import { needsRole } from "./queries";

describe("needsRole", () => {
  it("只有启用中、没有角色的普通员工需要提示分配角色", () => {
    expect(needsRole({ super_admin: false, status: "active", role_ids: [] })).toBe(true);
    expect(needsRole({ super_admin: false, status: "active", role_ids: ["r1"] })).toBe(false);
    expect(needsRole({ super_admin: true, status: "active", role_ids: [] })).toBe(false);
    expect(needsRole({ super_admin: false, status: "disabled", role_ids: [] })).toBe(false);
  });
});
