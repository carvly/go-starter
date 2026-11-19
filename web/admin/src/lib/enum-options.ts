// enumOptions 把 meta.gen.ts 里枚举的取值和文案换成下拉框、筛选的选项，按取值在 spec 里的顺序。
export function enumOptions<T extends string>(meta: { values: readonly T[]; labels: Record<T, string> }): { value: T; label: string }[] {
  return meta.values.map((value) => ({ value, label: meta.labels[value] }));
}
