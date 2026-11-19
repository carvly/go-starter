// 列表页的筛选控件，按 0005 的字段类型矩阵：string 进 q 搜索（SearchInput），bool 用 BoolFilter，
// enum 用 EnumFilter（多选），date / datetime 用 DateRangeFilter。筛选条件放在 URL 的 search 里，
// 控件只负责显示和回调，条件变了回到第一页由页面处理。
import { Search } from "lucide-react";
import { type ReactNode, useState } from "react";

import { type DayRange, DateRangePicker } from "@/components/date-range-picker";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

export type FilterOption<T extends string = string> = { value: T; label: string };

// FilterBar 把筛选控件排成一行，放不下时换行。
export function FilterBar({ children }: { children: ReactNode }) {
  return <div className="flex flex-wrap items-end gap-3">{children}</div>;
}

function FilterItem({ label, htmlFor, children }: { label: string; htmlFor?: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
    </div>
  );
}

function SearchForm({
  initial,
  placeholder,
  onSearch,
}: {
  initial: string;
  placeholder: string;
  onSearch: (q: string | undefined) => void;
}) {
  const [keyword, setKeyword] = useState(initial);
  return (
    <form
      className="flex w-full max-w-sm gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        onSearch(keyword.trim() || undefined);
      }}
    >
      <Input value={keyword} onChange={(e) => setKeyword(e.target.value)} placeholder={placeholder} aria-label={placeholder} />
      <Button type="submit" variant="outline">
        <Search />
        搜索
      </Button>
    </form>
  );
}

// SearchInput 回车或点搜索才查询，空关键字回调 undefined。URL 里的关键字变了（比如浏览器后退）
// 时输入框跟着重置。
export function SearchInput({
  value,
  placeholder,
  onSearch,
}: {
  value: string | undefined;
  placeholder: string;
  onSearch: (q: string | undefined) => void;
}) {
  return <SearchForm key={value ?? ""} initial={value ?? ""} placeholder={placeholder} onSearch={onSearch} />;
}

// EnumFilter 是多选，什么都不选表示不筛选（回调 undefined）。
export function EnumFilter<T extends string>({
  label,
  options,
  value,
  onChange,
  allLabel = "全部",
}: {
  label: string;
  options: readonly FilterOption<T>[];
  value: readonly T[] | undefined;
  onChange: (value: T[] | undefined) => void;
  allLabel?: string;
}) {
  const labels = new Map<string, string>(options.map((o) => [o.value, o.label]));
  return (
    <FilterItem label={label}>
      <Select<T, true> multiple items={options} value={[...(value ?? [])]} onValueChange={(v) => onChange(v.length > 0 ? v : undefined)}>
        <SelectTrigger className="w-44" aria-label={label}>
          <SelectValue>
            {(selected: T[]) =>
              selected.length === 0
                ? allLabel
                : selected.length <= 2
                  ? selected.map((v) => labels.get(v) ?? v).join("、")
                  : `已选 ${selected.length} 项`
            }
          </SelectValue>
        </SelectTrigger>
        <SelectContent>
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value}>
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </FilterItem>
  );
}

const boolItems = [
  { value: null, label: "全部" },
  { value: "true", label: "是" },
  { value: "false", label: "否" },
];

// BoolFilter 是"全部 / 是 / 否"三选一，全部回调 undefined。
export function BoolFilter({
  label,
  value,
  onChange,
  labels = ["是", "否"],
}: {
  label: string;
  value: boolean | undefined;
  onChange: (value: boolean | undefined) => void;
  labels?: readonly [string, string];
}) {
  const items = [boolItems[0], { ...boolItems[1], label: labels[0] }, { ...boolItems[2], label: labels[1] }];
  return (
    <FilterItem label={label}>
      <Select
        items={items}
        value={value === undefined ? null : String(value)}
        onValueChange={(v) => onChange(v === null ? undefined : v === "true")}
      >
        <SelectTrigger className="w-28" aria-label={label}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {items.map((item) => (
            <SelectItem key={item.value ?? "all"} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </FilterItem>
  );
}

// DateRangeFilter 选的是日期（含两端，yyyy-mm-dd）；datetime 字段查询前用 dayRange 换算成
// 按浏览器时区日界的半开区间。
export function DateRangeFilter({
  id,
  label,
  value,
  onChange,
}: {
  id: string;
  label: string;
  value: DayRange;
  onChange: (range: DayRange) => void;
}) {
  return (
    <FilterItem label={label} htmlFor={id}>
      <DateRangePicker id={id} value={value} onChange={onChange} placeholder="全部日期" />
    </FilterItem>
  );
}

// SortSelect 是列表的排序方式。value 为空表示用默认排序（defaultValue，不进 URL）；选回默认的
// 那一项时回调 undefined。
export function SortSelect<T extends string>({
  options,
  value,
  defaultValue,
  onChange,
}: {
  options: readonly FilterOption<T>[];
  value: T | undefined;
  defaultValue: T;
  onChange: (value: T | undefined) => void;
}) {
  return (
    <FilterItem label="排序">
      <Select<T>
        items={options}
        value={value ?? defaultValue}
        onValueChange={(v) => onChange(v === null || v === defaultValue ? undefined : v)}
      >
        <SelectTrigger className="w-40" aria-label="排序">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value}>
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </FilterItem>
  );
}

// SelectFilter 是单选、选项来自数据（如操作日志的员工），null 表示全部。
export function SelectFilter({
  label,
  options,
  value,
  onChange,
  allLabel = "全部",
}: {
  label: string;
  options: readonly FilterOption[];
  value: string | undefined;
  onChange: (value: string | undefined) => void;
  allLabel?: string;
}) {
  const items = [{ value: null, label: allLabel }, ...options];
  return (
    <FilterItem label={label}>
      <Select items={items} value={value ?? null} onValueChange={(v) => onChange(v ?? undefined)}>
        <SelectTrigger className="w-48" aria-label={label}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {items.map((item) => (
            <SelectItem key={item.value ?? "all"} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </FilterItem>
  );
}
