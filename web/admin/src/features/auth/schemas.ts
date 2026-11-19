import { z } from "zod";

import { changePasswordRequestMeta } from "@/api/meta.gen";

// 两个密码字段的约束来自 api/admin.yaml（src/api/meta.gen.ts），这里加上只在页面上存在的"再次输入"。
export const changePasswordSchema = changePasswordRequestMeta.form
  .extend({ confirm_password: z.string() })
  .refine((v) => v.new_password === v.confirm_password, { message: "两次输入的新密码不一致", path: ["confirm_password"] })
  .refine((v) => v.new_password !== v.current_password, { message: "新密码不能和当前密码相同", path: ["new_password"] });

// safeRedirect 只接受站内路径，防止登录后被带到别的站点（开放重定向）。
export function safeRedirect(target: string | undefined): string {
  return target && target.startsWith("/") && !target.startsWith("//") && !target.startsWith("/login") ? target : "/";
}
