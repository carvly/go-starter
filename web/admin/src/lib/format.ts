// formatDateTime 按浏览器所在时区显示 RFC 3339 时间，到秒。
export function formatDateTime(value: string | undefined | null): string {
  if (!value) return "—";
  return new Date(value).toLocaleString("zh-CN", { hour12: false });
}

// dayRange 把 yyyy-mm-dd 形式的起止日期（含）换算成后端的半开区间 [from, to)，按浏览器所在时区的日界。
// 不合法的日期当作没有（日期来自 URL，可能被手改）。
export function dayRange(from: string | undefined, to: string | undefined): { from?: string; to?: string } {
  const out: { from?: string; to?: string } = {};
  const start = parseDay(from);
  if (start) out.from = start.toISOString();
  const end = parseDay(to);
  if (end) {
    end.setDate(end.getDate() + 1);
    out.to = end.toISOString();
  }
  return out;
}

// dateRange 同 dayRange，用于 format: date 的字段（不带时区，按日期比较）：止日加一天，换成半开区间。
export function dateRange(from: string | undefined, to: string | undefined): { from?: string; to?: string } {
  const out: { from?: string; to?: string } = {};
  if (parseDay(from)) out.from = from;
  const end = parseDay(to);
  if (end) {
    end.setDate(end.getDate() + 1);
    out.to = formatDay(end);
  }
  return out;
}

// parseDay / formatDay 在 yyyy-mm-dd 和浏览器所在时区当天零点的 Date 之间转换。
// 不用 new Date("yyyy-mm-dd")：那样按 UTC 解析，在 UTC 以西的时区会变成前一天。
export function parseDay(day: string | undefined): Date | undefined {
  if (!day || !/^\d{4}-\d{2}-\d{2}$/.test(day)) return undefined;
  const d = new Date(`${day}T00:00:00`);
  // 2024-02-30 会被 Date 进位成 3 月 1 日，格式化回来对不上就是不存在的日期。
  return Number.isNaN(d.getTime()) || formatDay(d) !== day ? undefined : d;
}

export function formatDay(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}
