import { describe, expect, it } from "vitest";

import { boolSearch, daySearch, enumListSearch, enumSearch, pageSearch, uuidSearch } from "./search";

describe("daySearch", () => {
  it("只保留真实存在的日期", () => {
    expect(daySearch.parse("2026-11-09")).toBe("2026-11-09");
    expect(daySearch.parse("2026-13-01")).toBeUndefined();
    expect(daySearch.parse("2026-02-30")).toBeUndefined();
    expect(daySearch.parse(undefined)).toBeUndefined();
  });
});

describe("pageSearch", () => {
  it("页码在 1 到最后一页之间，其余丢掉", () => {
    const page = pageSearch(20);
    expect(page.parse("3")).toBe(3);
    expect(page.parse("500")).toBe(500);
    expect(page.parse("501")).toBeUndefined();
    expect(page.parse("0")).toBeUndefined();
    expect(page.parse("abc")).toBeUndefined();
  });
});

describe("enumListSearch", () => {
  const status = enumListSearch(["pending", "confirmed", "failed"]);
  it("保留认识的取值并去重，单个字符串也接受", () => {
    expect(status.parse(["failed", "pending", "failed"])).toEqual(["failed", "pending"]);
    expect(status.parse("pending")).toEqual(["pending"]);
  });
  it("不认识的取值丢掉，一个都不剩时不筛选", () => {
    expect(status.parse(["shipped", "pending"])).toEqual(["pending"]);
    expect(status.parse(["shipped"])).toBeUndefined();
    expect(status.parse([])).toBeUndefined();
    expect(status.parse(42)).toBeUndefined();
    expect(status.parse(undefined)).toBeUndefined();
  });
});

describe("uuidSearch", () => {
  it("只保留合法的 ID", () => {
    expect(uuidSearch.parse("01936f3c-2b3c-7d4e-8f5a-6b7c8d9e0f1a")).toBe("01936f3c-2b3c-7d4e-8f5a-6b7c8d9e0f1a");
    expect(uuidSearch.parse("x")).toBeUndefined();
  });
});

describe("enumSearch / boolSearch", () => {
  it("只保留认识的值", () => {
    const sort = enumSearch(["name", "-name"]);
    expect(sort.parse("-name")).toBe("-name");
    expect(sort.parse("x")).toBeUndefined();
    expect(boolSearch.parse("true")).toBe(true);
    expect(boolSearch.parse(false)).toBe(false);
    expect(boolSearch.parse("yes")).toBeUndefined();
  });
});
