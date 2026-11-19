// 单元格按 0005 的字段类型矩阵一种类型一个，列定义里用 cell: ({ row }) => <MoneyCell cents={...} />。
// 空值统一显示成灰色的"—"。
import type { RowData } from "@tanstack/react-table";
import type { ReactNode } from "react";

import { Badge } from "@/components/ui/badge";
import { formatDateTime } from "@/lib/format";
import { formatCents } from "@/lib/money";

import type { DataTableColumn } from ".";

function Empty() {
  return <span className="text-muted-foreground">—</span>;
}

// TextCell 用于 string 和 text；truncate 用于长文本，超出一行截断，悬停显示全文。
export function TextCell({ value, truncate }: { value: string | null | undefined; truncate?: boolean }) {
  if (!value) return <Empty />;
  if (!truncate) return value;
  return (
    <span className="block max-w-xs truncate" title={value}>
      {value}
    </span>
  );
}

export function NumberCell({ value }: { value: number | null | undefined }) {
  if (value === null || value === undefined) return <Empty />;
  return <span className="tabular-nums">{value.toLocaleString("zh-CN")}</span>;
}

export function MoneyCell({ cents }: { cents: number | null | undefined }) {
  if (cents === null || cents === undefined) return <Empty />;
  return <span className="tabular-nums">{formatCents(cents)}</span>;
}

export function BoolCell({ value, labels = ["是", "否"] }: { value: boolean | null | undefined; labels?: readonly [string, string] }) {
  if (value === null || value === undefined) return <Empty />;
  return value ? labels[0] : <span className="text-muted-foreground">{labels[1]}</span>;
}

export type EnumVariant = "default" | "secondary" | "outline" | "destructive";

// EnumCell 显示枚举的中文文案徽标；variants 按取值区分颜色（如失败用 destructive），默认 outline。
export function EnumCell<T extends string>({
  value,
  labels,
  variants,
}: {
  value: T | null | undefined;
  labels: Record<T, string>;
  variants?: Partial<Record<T, EnumVariant>>;
}) {
  if (!value) return <Empty />;
  return <Badge variant={variants?.[value] ?? "outline"}>{labels[value] ?? value}</Badge>;
}

// DateCell 的值是接口的 format: date（yyyy-mm-dd），不做时区换算，原样显示。
export function DateCell({ value }: { value: string | null | undefined }) {
  return value ? <span className="tabular-nums">{value}</span> : <Empty />;
}

export function DateTimeCell({ value }: { value: string | null | undefined }) {
  return value ? <span className="tabular-nums">{formatDateTime(value)}</span> : <Empty />;
}

// actionsColumn 是行末的操作列：按钮靠右，按钮的显隐由调用方用 useCan 决定，
// 一个按钮都没有时调用方不加这一列。
export function actionsColumn<T extends RowData>(render: (row: T) => ReactNode): DataTableColumn<T> {
  return {
    id: "actions",
    header: "",
    cell: ({ row }) => <div className="flex justify-end gap-2">{render(row.original)}</div>,
  };
}
