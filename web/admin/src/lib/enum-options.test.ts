import { describe, expect, it } from "vitest";

import { enumOptions } from "./enum-options";

describe("enumOptions", () => {
  it("按取值的顺序配上文案", () => {
    const meta = { values: ["b", "a"], labels: { a: "甲", b: "乙" } } as const;
    expect(enumOptions(meta)).toEqual([
      { value: "b", label: "乙" },
      { value: "a", label: "甲" },
    ]);
  });
});
