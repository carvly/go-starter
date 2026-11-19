import { describe, expect, it } from "vitest";

import { ApiError } from "@/api/errors";

import { FormDialog, isConflict, useDialog } from ".";

describe("overlay", () => {
  it("识别乐观锁冲突，并暴露对话框组件", () => {
    expect(isConflict(new ApiError(409, "item_conflict", ""))).toBe(true);
    expect(isConflict(new ApiError(404, "item_not_found", ""))).toBe(false);
    expect(FormDialog).toBeTypeOf("function");
    expect(useDialog).toBeTypeOf("function");
  });
});
