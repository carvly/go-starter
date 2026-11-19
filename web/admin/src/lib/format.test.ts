import { describe, expect, it } from "vitest";

import { dateRange, dayRange, formatDay, parseDay } from "./format";

describe("dayRange", () => {
  it("把含两端的日期换成半开区间 [from, to)，to 是结束日期的次日零点", () => {
    const { from, to } = dayRange("2026-11-01", "2026-11-03");
    expect(new Date(from!).getTime()).toBe(new Date("2026-11-01T00:00:00").getTime());
    expect(new Date(to!).getTime()).toBe(new Date("2026-11-04T00:00:00").getTime());
    expect(dayRange(undefined, undefined)).toEqual({});
  });

  it("不合法的日期当作没有，不抛异常", () => {
    expect(dayRange("2024-13-01", "2024-02-30x")).toEqual({});
  });
});

describe("dateRange", () => {
  it("止日加一天，跨月、跨年都对；不合法的日期当作没有", () => {
    expect(dateRange("2026-11-01", "2026-11-30")).toEqual({ from: "2026-11-01", to: "2026-12-01" });
    expect(dateRange(undefined, "2026-12-31")).toEqual({ to: "2027-01-01" });
    expect(dateRange("2026-02-30", "x")).toEqual({});
  });
});

describe("parseDay / formatDay", () => {
  it("按本地时区当天零点互相转换", () => {
    const d = parseDay("2026-03-09")!;
    expect([d.getFullYear(), d.getMonth(), d.getDate(), d.getHours()]).toEqual([2026, 2, 9, 0]);
    expect(formatDay(d)).toBe("2026-03-09");
  });

  it("不合法的值当作没有", () => {
    expect(parseDay(undefined)).toBeUndefined();
    expect(parseDay("2026-3-9")).toBeUndefined();
    expect(parseDay("2026-13-40")).toBeUndefined();
    expect(parseDay("2026-02-30")).toBeUndefined();
  });
});
