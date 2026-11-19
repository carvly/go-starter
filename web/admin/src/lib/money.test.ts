import { describe, expect, it } from "vitest";

import { centsToYuan, formatCents, parseYuan } from "./money";

describe("formatCents", () => {
  it("分 → ¥元，带千分位和两位小数", () => {
    expect(formatCents(19900)).toBe("¥199.00");
    expect(formatCents(123456789)).toBe("¥1,234,567.89");
    expect(formatCents(5)).toBe("¥0.05");
    expect(formatCents(0)).toBe("¥0.00");
  });
});

describe("centsToYuan / parseYuan", () => {
  it("互相转换，不经过浮点误差", () => {
    expect(centsToYuan(19950)).toBe("199.50");
    expect(parseYuan("199.5")).toBe(19950);
    expect(parseYuan(" 0.1 ")).toBe(10);
    expect(parseYuan("0.29")).toBe(29); // 0.29 * 100 = 28.999999999999996
    expect(parseYuan("12")).toBe(1200);
    expect(parseYuan(centsToYuan(7))).toBe(7);
  });

  it("格式不对返回 undefined", () => {
    for (const bad of ["", "abc", "1.234", "-1", "1e3", ".5", "1,000"]) expect(parseYuan(bad)).toBeUndefined();
  });
});
