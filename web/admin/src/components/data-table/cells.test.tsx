import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";

import { BoolCell, DateCell, DateTimeCell, EnumCell, MoneyCell, NumberCell, TextCell, actionsColumn } from "./cells";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function text(node: React.ReactNode): string {
  const el = document.body.appendChild(document.createElement("div"));
  const root = createRoot(el);
  act(() => root.render(node));
  const out = el.textContent ?? "";
  act(() => root.unmount());
  return out;
}

afterEach(() => {
  document.body.innerHTML = "";
});

describe("单元格", () => {
  it("空值统一显示成 —", () => {
    expect(text(<TextCell value="" />)).toBe("—");
    expect(text(<NumberCell value={null} />)).toBe("—");
    expect(text(<MoneyCell cents={undefined} />)).toBe("—");
    expect(text(<BoolCell value={null} />)).toBe("—");
    expect(text(<EnumCell value={null} labels={{ a: "A" }} />)).toBe("—");
    expect(text(<DateCell value="" />)).toBe("—");
    expect(text(<DateTimeCell value={undefined} />)).toBe("—");
  });

  it("按类型格式化", () => {
    expect(text(<NumberCell value={1234567} />)).toBe("1,234,567");
    expect(text(<NumberCell value={0} />)).toBe("0");
    expect(text(<MoneyCell cents={123450} />)).toBe("¥1,234.50");
    expect(text(<MoneyCell cents={0} />)).toBe("¥0.00");
    expect(text(<BoolCell value />)).toBe("是");
    expect(text(<BoolCell value={false} labels={["启用", "停用"]} />)).toBe("停用");
    expect(text(<EnumCell<"pending" | "failed"> value="failed" labels={{ pending: "处理中", failed: "失败" }} />)).toBe("失败");
    expect(text(<DateCell value="2026-11-09" />)).toBe("2026-11-09");
  });
});

describe("actionsColumn", () => {
  it("操作列使用固定 id", () => {
    expect(actionsColumn(() => "操作").id).toBe("actions");
  });
});
