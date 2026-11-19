import type { PermissionInfo } from "./queries";

// title 是模块的中文名（tag 的 summary），没有时用 tag。
export type PermissionGroup = { tag: string; title: string; permissions: PermissionInfo[] };

// groupPermissions 按 tag（模块）分组；角色里有、但 admin.yaml 已经删掉的权限码单独列出，
// 页面上标为"已失效"（检查时后端已经忽略它们，保存时去掉）。目录还没加载时（catalog 为
// undefined）不判断失效：否则角色已有的权限会全被当成失效、保存时清空。
export function groupPermissions(
  catalog: readonly PermissionInfo[] | undefined,
  granted: readonly string[],
): { groups: PermissionGroup[]; stale: string[] } {
  if (!catalog) return { groups: [], stale: [] };
  const groups = new Map<string, PermissionGroup>();
  for (const p of catalog) {
    const g = groups.get(p.tag) ?? { tag: p.tag, title: p.tag_summary || p.tag, permissions: [] };
    g.permissions.push(p);
    groups.set(p.tag, g);
  }
  const known = new Set(catalog.map((p) => p.code));
  return {
    groups: [...groups.values()],
    stale: granted.filter((code) => !known.has(code)),
  };
}
