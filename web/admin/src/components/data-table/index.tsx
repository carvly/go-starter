// 服务端分页的数据表格：features 只用这里的 DataTable / DataTableColumn / useCursorPager，
// 不直接 import @tanstack/react-table（oxlint 的 no-restricted-imports 检查）。
import { type Cell, type ColumnDef, type RowData, tableFeatures, useTable } from "@tanstack/react-table";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { type ReactNode, useCallback, useState } from "react";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

// 分页、排序、筛选都在服务端做，只需要核心行模型，不注册任何功能。
const features = tableFeatures({});
const noRows: never[] = [];

// oxlint-disable-next-line typescript/no-explicit-any -- ColumnDef 的第三个参数是单元格值，每列不同
export type DataTableColumn<T extends RowData> = ColumnDef<typeof features, T, any>;

// renderCell 把列定义的 cell 函数当作普通函数调用，而不是像 FlexRender 那样当作组件渲染：
// 页面常在组件里按权限拼列定义，每次渲染 cell 都是新函数，当作组件时 React 认为类型变了，
// 会重新挂载整个单元格，里面打开的对话框在列表刷新时就关掉了。cell 函数里因此不能用 hook，
// 需要状态的放进它返回的组件里。
function renderCell<T extends RowData>(cell: Cell<typeof features, T, unknown>): ReactNode {
  const def = cell.column.columnDef.cell;
  if (typeof def === "function") return def(cell.getContext()) as ReactNode;
  return def ?? (cell.getValue() as ReactNode);
}

type DataTableProps<T extends RowData> = {
  columns: DataTableColumn<T>[];
  data: T[] | undefined;
  loading?: boolean;
  empty?: ReactNode;
  getRowId?: (row: T) => string;
};

export function DataTable<T extends RowData>({ columns, data, loading, empty = "暂无数据", getRowId }: DataTableProps<T>) {
  const table = useTable({ features, data: data ?? noRows, columns, getRowId });
  return (
    <div className="overflow-hidden rounded-lg border">
      <Table>
        <TableHeader>
          {table.getHeaderGroups().map((group) => (
            <TableRow key={group.id}>
              {group.headers.map((header) => (
                <TableHead key={header.id}>{header.isPlaceholder ? null : <table.FlexRender header={header} />}</TableHead>
              ))}
            </TableRow>
          ))}
        </TableHeader>
        <TableBody>
          {loading ? (
            <TableRow>
              <TableCell colSpan={columns.length}>
                <Skeleton className="h-6 w-full" />
              </TableCell>
            </TableRow>
          ) : table.getRowModel().rows.length === 0 ? (
            <TableRow>
              <TableCell colSpan={columns.length} className="h-24 text-center text-muted-foreground">
                {empty}
              </TableCell>
            </TableRow>
          ) : (
            table.getRowModel().rows.map((row) => (
              <TableRow key={row.id}>
                {row.getAllCells().map((cell) => (
                  <TableCell key={cell.id}>{renderCell(cell)}</TableCell>
                ))}
              </TableRow>
            ))
          )}
        </TableBody>
      </Table>
    </div>
  );
}

// useCursorPager 管理 cursor 分页：后端只给"下一页的 cursor"，上一页靠记住走过的 cursor。
// resetKey（通常是序列化的筛选条件）变化时回到第一页：在同一次渲染里就丢掉旧 cursor，
// 不会先拿旧 cursor 配新条件发一次请求。
export function useCursorPager(resetKey = "") {
  const [state, setState] = useState<{ key: string; stack: string[] }>({ key: resetKey, stack: [] });
  const stack = state.key === resetKey ? state.stack : [];
  const update = useCallback(
    (fn: (s: string[]) => string[]) => setState((prev) => ({ key: resetKey, stack: fn(prev.key === resetKey ? prev.stack : []) })),
    [resetKey],
  );
  const next = useCallback((cursor: string) => update((s) => [...s, cursor]), [update]);
  const prev = useCallback(() => update((s) => s.slice(0, -1)), [update]);
  return { cursor: stack.at(-1), page: stack.length + 1, hasPrev: stack.length > 0, next, prev };
}

export function Pager({
  page,
  hasPrev,
  nextCursor,
  onPrev,
  onNext,
}: {
  page: number;
  hasPrev: boolean;
  nextCursor: string | undefined;
  onPrev: () => void;
  onNext: (cursor: string) => void;
}) {
  return (
    <div className="flex items-center justify-end gap-2 text-sm text-muted-foreground">
      <span>第 {page} 页</span>
      <Button variant="outline" size="icon-sm" aria-label="上一页" disabled={!hasPrev} onClick={onPrev}>
        <ChevronLeft />
      </Button>
      <Button variant="outline" size="icon-sm" aria-label="下一页" disabled={!nextCursor} onClick={() => nextCursor && onNext(nextCursor)}>
        <ChevronRight />
      </Button>
    </div>
  );
}

// PagePager 是页码分页（后台的 page / size 列表）的翻页条。后端的总数最多数到 10000，
// totalCapped 时显示"10000+"，也只能翻到第 10000 条为止。
export function PagePager({
  page,
  size,
  total,
  totalCapped,
  onChange,
}: {
  page: number;
  size: number;
  total: number | undefined;
  totalCapped: boolean | undefined;
  onChange: (page: number) => void;
}) {
  const pages = Math.max(1, Math.ceil((total ?? 0) / size));
  return (
    <div className="flex items-center justify-end gap-2 text-sm text-muted-foreground">
      <span>
        共 {total ?? "—"}
        {totalCapped && "+"} 条 · 第 {page} / {pages} 页
      </span>
      <Button variant="outline" size="icon-sm" aria-label="上一页" disabled={page <= 1} onClick={() => onChange(page - 1)}>
        <ChevronLeft />
      </Button>
      <Button variant="outline" size="icon-sm" aria-label="下一页" disabled={page >= pages} onClick={() => onChange(page + 1)}>
        <ChevronRight />
      </Button>
    </div>
  );
}
