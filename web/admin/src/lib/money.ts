// 金额在接口里是整数"分"，页面上是"元"。转换只做字符串处理，不经过浮点数：
// 0.1 + 0.2 这类误差在金额上不能出现。

// formatCents 把分格式化成"¥1,234.50"。
export function formatCents(cents: number): string {
  const sign = cents < 0 ? "-" : "";
  const abs = Math.abs(cents);
  const yuan = Math.trunc(abs / 100).toLocaleString("zh-CN");
  return `${sign}¥${yuan}.${String(abs % 100).padStart(2, "0")}`;
}

// centsToYuan 把分换成可编辑的元字符串（"1234.5" → 表单里显示 "1234.50"）。
export function centsToYuan(cents: number): string {
  return `${Math.trunc(cents / 100)}.${String(Math.abs(cents) % 100).padStart(2, "0")}`;
}

// parseYuan 把用户输入的元（最多两位小数，不带符号）换成分；格式不对时返回 undefined。
export function parseYuan(input: string): number | undefined {
  const m = /^(\d+)(?:\.(\d{1,2}))?$/.exec(input.trim());
  if (!m) return undefined;
  const cents = Number(m[1]) * 100 + Number((m[2] ?? "").padEnd(2, "0"));
  return Number.isSafeInteger(cents) ? cents : undefined;
}
