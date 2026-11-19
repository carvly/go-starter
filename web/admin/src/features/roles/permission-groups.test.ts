import { describe, expect, it } from "vitest";

import { groupPermissions } from "./permission-groups";

describe("groupPermissions", () => {
  it("按模块分组（用模块的中文名），找出 spec 里已经删掉的权限码", () => {
    const orderRead = { code: "order:read", tag: "orders", tag_summary: "订单", operations: ["订单列表", "订单详情"] };
    const productRead = { code: "product:read", tag: "products", tag_summary: "商品", operations: ["商品列表"] };
    const productUpdate = { code: "product:update", tag: "products", tag_summary: "", operations: [] };
    expect(groupPermissions([orderRead, productRead, productUpdate], ["product:read", "legacy:thing"])).toEqual({
      groups: [
        { tag: "orders", title: "订单", permissions: [orderRead] },
        { tag: "products", title: "商品", permissions: [productRead, productUpdate] },
      ],
      stale: ["legacy:thing"],
    });
  });

  it("模块没有中文名时用 tag", () => {
    const p = { code: "thing:read", tag: "things", tag_summary: "", operations: [] };
    expect(groupPermissions([p], []).groups[0]?.title).toBe("things");
  });

  it("目录还没加载时不把已有权限当成失效", () => {
    expect(groupPermissions(undefined, ["product:read"])).toEqual({ groups: [], stale: [] });
  });
});
