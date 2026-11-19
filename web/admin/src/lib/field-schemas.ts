import { z } from "zod";

import { formatCents, parseYuan } from "./money";

// 表单字段的 zod 规则，和 src/components/form 的字段组件一一对应：组件里的值是用户输入的字符串，
// 这里校验并转换成接口要的类型。src/api/meta.gen.ts 按 spec 里的约束调用它们，提示文字统一写在
// 这里，所有表单的说法一致。

type Common = { title: string; required?: boolean };
type Range = { min?: number; max?: number };

// textInput 对应 TextField、TextareaField、PasswordField。默认去掉首尾空格；密码不去（trim: false），
// 空格也是密码的一部分。required 表示不能为空。
export function textInput({ title, required, min, max, trim = true }: Common & Range & { trim?: boolean }) {
  let s = z.string();
  if (trim) s = s.trim();
  if (required) s = s.min(1, `请输入${title}`);
  if (min !== undefined && min > 1) s = s.min(min, `${title}至少 ${min} 个字符`);
  if (max !== undefined) s = s.max(max, `${title}最多 ${max} 个字符`);
  return s;
}

// intInput 对应 NumberField：整数，范围 [min, max]。不是 required 时空着提交 undefined。
export function intInput(o: Common & Range & { required: true }): z.ZodType<number, string>;
export function intInput(o: Common & Range): z.ZodType<number | undefined, string>;
export function intInput({ title, required, min, max }: Common & Range): z.ZodType<number | undefined, string> {
  return z.string().transform((raw, ctx) => {
    const fail = (message: string) => {
      ctx.addIssue({ code: "custom", message });
      return z.NEVER;
    };
    const v = raw.trim();
    if (v === "") return required ? fail(`请输入${title}`) : undefined;
    const n = Number(v);
    if (!/^[+-]?\d+$/.test(v) || !Number.isSafeInteger(n)) return fail(`${title}要填整数`);
    if (!inRange(n, min, max)) return fail(rangeMessage(title, min, max, bound));
    return n;
  });
}

// moneyInput 对应 MoneyField：按元输入（最多两位小数），转换成分；min、max 的单位是分。
export function moneyInput(o: Common & Range & { required: true }): z.ZodType<number, string>;
export function moneyInput(o: Common & Range): z.ZodType<number | undefined, string>;
export function moneyInput({ title, required, min, max }: Common & Range): z.ZodType<number | undefined, string> {
  return z.string().transform((raw, ctx) => {
    const fail = (message: string) => {
      ctx.addIssue({ code: "custom", message });
      return z.NEVER;
    };
    const v = raw.trim();
    if (v === "") return required ? fail(`请输入${title}`) : undefined;
    const cents = parseYuan(v);
    if (cents === undefined) return fail(`${title}按元填写，最多两位小数`);
    if (!inRange(cents, min, max))
      return fail(min === 1 && max === undefined ? `${title}要大于 0` : rangeMessage(title, min, max, formatCents));
    return cents;
  });
}

function inRange(n: number, min: number | undefined, max: number | undefined) {
  return (min === undefined || n >= min) && (max === undefined || n <= max);
}

function rangeMessage(title: string, min: number | undefined, max: number | undefined, show: (n: number) => string) {
  if (min !== undefined && max !== undefined) return `${title}的范围是 ${show(min)} 到 ${show(max)}`;
  if (min !== undefined) return `${title}不能小于 ${show(min)}`;
  return `${title}不能大于 ${show(max ?? 0)}`;
}

// bound 把整万、整亿的边界写成"10 亿"，比 1000000000 好读。
function bound(n: number) {
  const abs = Math.abs(n);
  if (abs >= 1e8 && abs % 1e8 === 0) return `${n / 1e8} 亿`;
  if (abs >= 1e4 && abs % 1e4 === 0) return `${n / 1e4} 万`;
  return String(n);
}

// enumInput 对应 SelectField：值是取值之一；没选时是空串，不是 required 时提交 undefined。
export function enumInput<const T extends readonly [string, ...string[]]>(
  values: T,
  o: Common & { required: true },
): z.ZodType<T[number], string>;
export function enumInput<const T extends readonly [string, ...string[]]>(values: T, o: Common): z.ZodType<T[number] | undefined, string>;
export function enumInput<const T extends readonly [string, ...string[]]>(
  values: T,
  { title, required }: Common,
): z.ZodType<T[number] | undefined, string> {
  return z.string().transform((v, ctx) => {
    if (v === "") {
      if (!required) return undefined;
      ctx.addIssue({ code: "custom", message: `请选择${title}` });
      return z.NEVER;
    }
    if (!(values as readonly string[]).includes(v)) {
      ctx.addIssue({ code: "custom", message: `请选择${title}` });
      return z.NEVER;
    }
    return v as T[number];
  });
}

// dateInput 对应 DateField（yyyy-mm-dd），dateTimeInput 对应 DateTimeField（带时区的 RFC 3339）。
// 空着时：required 提示"请选择"，否则提交 undefined。
export function dateInput(o: Common & { required: true }): z.ZodType<string, string>;
export function dateInput(o: Common): z.ZodType<string | undefined, string>;
export function dateInput({ title, required }: Common): z.ZodType<string | undefined, string> {
  return optionalString(z.iso.date({ error: `请选择${title}` }), title, required);
}

export function dateTimeInput(o: Common & { required: true }): z.ZodType<string, string>;
export function dateTimeInput(o: Common): z.ZodType<string | undefined, string>;
export function dateTimeInput({ title, required }: Common): z.ZodType<string | undefined, string> {
  return optionalString(z.iso.datetime({ offset: true, error: `请选择${title}` }), title, required);
}

function optionalString(schema: z.ZodType<string, string>, title: string, required?: boolean) {
  return z.string().transform((v, ctx) => {
    if (v === "") {
      if (!required) return undefined;
      ctx.addIssue({ code: "custom", message: `请选择${title}` });
      return z.NEVER;
    }
    const r = schema.safeParse(v);
    if (!r.success) {
      ctx.addIssue({ code: "custom", message: `请选择${title}` });
      return z.NEVER;
    }
    return r.data;
  });
}
