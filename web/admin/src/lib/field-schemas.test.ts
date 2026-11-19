import { describe, expect, it } from "vitest";

import { dateInput, dateTimeInput, enumInput, intInput, moneyInput, textInput } from "./field-schemas";

const message = (r: { success: boolean; error?: { issues: { message: string }[] } }) => r.error?.issues[0]?.message;

describe("textInput", () => {
  it("去掉首尾空格，按约束提示", () => {
    const name = textInput({ title: "名称", required: true, max: 3 });
    expect(name.parse(" 键盘 ")).toBe("键盘");
    expect(message(name.safeParse("  "))).toBe("请输入名称");
    expect(message(name.safeParse("机械键盘"))).toBe("名称最多 3 个字符");
  });

  it("密码不去空格", () => {
    const pw = textInput({ title: "新密码", required: true, min: 4, trim: false });
    expect(pw.parse(" ab ")).toBe(" ab ");
    expect(message(pw.safeParse("abc"))).toBe("新密码至少 4 个字符");
  });

  it("不是 required 时可以空着", () => {
    expect(textInput({ title: "说明" }).parse("")).toBe("");
  });
});

describe("intInput", () => {
  const stock = intInput({ title: "库存", required: true, min: 0, max: 1_000_000_000 });

  it("转成整数，接受正负号", () => {
    expect(stock.parse(" 10 ")).toBe(10);
    expect(intInput({ title: "增减量", required: true }).parse("-3")).toBe(-3);
  });

  it("按情况提示", () => {
    expect(message(stock.safeParse(""))).toBe("请输入库存");
    expect(message(stock.safeParse("1.5"))).toBe("库存要填整数");
    expect(message(stock.safeParse("1000000001"))).toBe("库存的范围是 0 到 10 亿");
    expect(message(intInput({ title: "数量", required: true, min: 1 }).safeParse("0"))).toBe("数量不能小于 1");
  });

  it("不是 required 时空着得到 undefined", () => {
    expect(intInput({ title: "排序" }).parse("")).toBeUndefined();
  });
});

describe("moneyInput", () => {
  const price = moneyInput({ title: "单价", required: true, min: 1 });

  it("按元输入，转成分", () => {
    expect(price.parse("199.9")).toBe(19990);
  });

  it("按情况提示", () => {
    expect(message(price.safeParse(""))).toBe("请输入单价");
    expect(message(price.safeParse("1.234"))).toBe("单价按元填写，最多两位小数");
    expect(message(price.safeParse("0"))).toBe("单价要大于 0");
    expect(message(moneyInput({ title: "金额", required: true, min: 0, max: 10000 }).safeParse("100.01"))).toBe(
      "金额的范围是 ¥0.00 到 ¥100.00",
    );
  });
});

describe("enumInput / dateInput / dateTimeInput", () => {
  it("没选时 required 提示请选择，否则得到 undefined", () => {
    const status = enumInput(["a", "b"], { title: "状态", required: true });
    expect(status.parse("a")).toBe("a");
    expect(message(status.safeParse(""))).toBe("请选择状态");
    expect(enumInput(["a"], { title: "状态" }).parse("")).toBeUndefined();
    expect(dateInput({ title: "日期", required: true }).parse("2026-11-09")).toBe("2026-11-09");
    expect(message(dateInput({ title: "日期", required: true }).safeParse(""))).toBe("请选择日期");
    expect(dateTimeInput({ title: "时间" }).parse("")).toBeUndefined();
    expect(dateTimeInput({ title: "时间" }).parse("2026-11-09T10:00:00+08:00")).toBe("2026-11-09T10:00:00+08:00");
  });
});
