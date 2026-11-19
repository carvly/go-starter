import { z } from "zod";

import { parseDay } from "./format";

// 列表页 URL search 里的公共字段。不合法的值丢掉（catch 成 undefined），不报错：URL 可能被手改，
// 或者是旧链接。

// MAX_PAGED_ROWS 同后端的 page.MaxTotal：页码分页只能翻到前 10000 条。
export const MAX_PAGED_ROWS = 10000;

// yyyy-mm-dd，且是真实存在的日期。
export const daySearch = z
  .string()
  .refine((v) => parseDay(v) !== undefined)
  .optional()
  .catch(undefined);

// 关键字搜索。
export const textSearch = z.string().optional().catch(undefined);

// 按 ID 筛选（如只看某个员工）。
export const uuidSearch = z.uuid().optional().catch(undefined);

// 页码：从 1 开始，不超过能翻到的最后一页。
export const pageSearch = (pageSize: number) =>
  z.coerce
    .number()
    .int()
    .min(1)
    .max(Math.ceil(MAX_PAGED_ROWS / pageSize))
    .optional()
    .catch(undefined);

// 枚举多选（EnumFilter）：URL 里是数组（手写的 ?status=pending 是单个字符串，也接受），去掉不认识的
// 取值并去重；一个都不剩时当作不筛选。
export const enumListSearch = <const T extends readonly [string, ...string[]]>(values: T) =>
  z
    .preprocess((v) => (typeof v === "string" ? [v] : v), z.array(z.string()))
    .transform((list) => [...new Set(list.filter((v): v is T[number] => values.includes(v)))])
    .transform((list) => (list.length > 0 ? list : undefined))
    .optional()
    .catch(undefined);

// 单选的枚举（如排序），不认识的取值丢掉。
export const enumSearch = <const T extends readonly [string, ...string[]]>(values: T) => z.enum(values).optional().catch(undefined);

// 布尔筛选：URL 里是 true / false。
export const boolSearch = z
  .preprocess((v) => (v === "true" ? true : v === "false" ? false : v), z.boolean())
  .optional()
  .catch(undefined);
