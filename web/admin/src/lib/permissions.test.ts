import { House, Shield, Users } from "lucide-react";
import { describe, expect, it } from "vitest";

import { hasPermission, isNavActive, visibleNav } from "./permissions";

const staff = { id: "1", username: "alice", display_name: "Alice", super_admin: false, must_change_password: false };
const root = { ...staff, super_admin: true };

describe("hasPermission", () => {
  it("按保留值和权限码判断，超级管理员短路", () => {
    expect(hasPermission(staff, [], undefined)).toBe(true);
    expect(hasPermission(staff, [], "authenticated")).toBe(true);
    expect(hasPermission(staff, ["document:read"], "superadmin")).toBe(false);
    expect(hasPermission(staff, ["document:read"], "document:read")).toBe(true);
    expect(hasPermission(staff, ["document:read"], "document:update")).toBe(false);
    expect(hasPermission(root, [], "superadmin")).toBe(true);
    expect(hasPermission(root, [], "document:update")).toBe(true);
  });
});

describe("visibleNav", () => {
  const routes = [
    { fullPath: "/", options: { staticData: { nav: { title: "首页", icon: House, order: -1 } } } },
    { fullPath: "/accounts", options: { staticData: { permission: "superadmin", nav: { title: "员工", icon: Users, group: "系统" } } } },
    { fullPath: "/documents/", options: { staticData: { permission: "document:read", nav: { title: "文档", icon: Shield, order: 2 } } } },
    { fullPath: "/forbidden", options: { staticData: {} } },
    { fullPath: "/__root__", options: {} },
  ];

  it("只列出有权限、带 nav 的路由，按组和顺序排列", () => {
    expect(visibleNav(routes, staff, ["document:read"])).toEqual([
      {
        title: "常用",
        items: [
          { title: "首页", icon: House, order: -1, to: "/" },
          { title: "文档", icon: Shield, order: 2, to: "/documents" },
        ],
      },
    ]);
    expect(visibleNav(routes, root, []).map((g) => g.title)).toEqual(["常用", "系统"]);
  });

  it("分组按固定顺序排列（常用、业务、系统，其余在后），不取决于路由树的遍历顺序", () => {
    const shuffled = [
      { fullPath: "/accounts", options: { staticData: { nav: { title: "员工", icon: Users, group: "系统" } } } },
      { fullPath: "/reports", options: { staticData: { nav: { title: "报表", icon: Shield, group: "其它" } } } },
      { fullPath: "/reports", options: { staticData: { nav: { title: "报告", icon: Shield, group: "业务" } } } },
      { fullPath: "/", options: { staticData: { nav: { title: "首页", icon: House } } } },
    ];
    expect(visibleNav(shuffled, root, []).map((g) => g.title)).toEqual(["常用", "业务", "系统", "其它"]);
  });
});

describe("isNavActive", () => {
  it("按路径段匹配，前缀相同的兄弟路由不互相点亮", () => {
    expect(isNavActive("/", "/")).toBe(true);
    expect(isNavActive("/roles", "/")).toBe(false);
    expect(isNavActive("/roles", "/roles")).toBe(true);
    expect(isNavActive("/roles/1", "/roles")).toBe(true);
    expect(isNavActive("/roles-audit", "/roles")).toBe(false);
  });
});
