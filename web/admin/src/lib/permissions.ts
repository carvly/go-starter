import type { LucideIcon } from "lucide-react";

import type { components } from "@/api/schema";

type Me = components["schemas"]["Me"];

// 页面声明的权限，取值同 api/admin.yaml 的 x-permission（public 只用于登录页，不在这里出现）：
// 不写或 authenticated 表示登录即可，superadmin 只有超级管理员，其余是 <资源>:<动作> 权限码。
export type Permission = string;

export type NavItem = {
  title: string;
  icon: LucideIcon;
  // 侧边栏分组名，不写归入"常用"；分组的先后见 groupOrder。
  group?: string;
  // 同组内的顺序，小的在前。
  order?: number;
};

declare module "@tanstack/react-router" {
  interface StaticDataRouteOption {
    permission?: Permission;
    nav?: NavItem;
  }
}

// hasPermission 只决定界面上显不显示；真正的拦截在后端。
export function hasPermission(me: Me, permissions: readonly string[], required: Permission | undefined): boolean {
  if (!required || required === "authenticated") return true;
  if (me.super_admin) return true;
  if (required === "superadmin") return false;
  return permissions.includes(required);
}

export type NavEntry = NavItem & { to: string };

// 侧边栏分组的顺序；不在这里的分组按名称排在后面。
const groupOrder = ["常用", "业务", "系统"];
const groupRank = (title: string) => {
  const i = groupOrder.indexOf(title);
  return i < 0 ? groupOrder.length : i;
};

export type NavGroup = { title: string; items: NavEntry[] };

type RouteLike = { fullPath: string; options: { staticData?: { permission?: Permission; nav?: NavItem } } };

// visibleNav 从路由树收集带 nav 的路由，按权限过滤后分组：新增页面只需在路由上写 nav，
// 不改共享的菜单数据。
export function visibleNav(routes: readonly RouteLike[], me: Me, permissions: readonly string[]): NavGroup[] {
  const groups = new Map<string, NavEntry[]>();
  for (const route of routes) {
    const meta = route.options.staticData;
    if (!meta?.nav || !hasPermission(me, permissions, meta.permission)) continue;
    const group = meta.nav.group ?? "常用";
    const items = groups.get(group) ?? [];
    items.push({ ...meta.nav, to: route.fullPath.replace(/\/$/, "") || "/" });
    groups.set(group, items);
  }
  return [...groups]
    .map(([title, items]) => ({ title, items: items.sort((a, b) => (a.order ?? 0) - (b.order ?? 0)) }))
    .sort((a, b) => groupRank(a.title) - groupRank(b.title) || a.title.localeCompare(b.title, "zh-CN"));
}

// isNavActive 按路径段匹配：/roles 不能把 /roles-audit 也标成当前项。
export function isNavActive(pathname: string, to: string): boolean {
  if (to === "/") return pathname === "/";
  return pathname === to || pathname.startsWith(to + "/");
}
