import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it } from "vitest";

import { DataTable, type DataTableColumn, useCursorPager } from ".";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe("useCursorPager", () => {
  it("resetKey 变化的那次渲染就回到第一页，不带旧 cursor", () => {
    const renders: { key: string; cursor?: string }[] = [];
    let pager!: ReturnType<typeof useCursorPager>;
    function Probe({ filterKey }: { filterKey: string }) {
      pager = useCursorPager(filterKey);
      renders.push({ key: filterKey, cursor: pager.cursor });
      return null;
    }

    const root = createRoot(document.createElement("div"));
    act(() => root.render(<Probe filterKey="a" />));
    act(() => pager.next("c1"));
    act(() => pager.next("c2"));
    expect(pager).toMatchObject({ cursor: "c2", page: 3, hasPrev: true });
    act(() => pager.prev());
    expect(pager).toMatchObject({ cursor: "c1", page: 2 });

    act(() => root.render(<Probe filterKey="b" />));
    expect(renders.filter((r) => r.key === "b").every((r) => r.cursor === undefined)).toBe(true);
    expect(pager).toMatchObject({ page: 1, hasPrev: false });

    act(() => pager.next("d1"));
    expect(pager).toMatchObject({ cursor: "d1", page: 2 });
    act(() => root.unmount());
  });
});

describe("DataTable", () => {
  type Row = { id: string; name: string };
  const columns: DataTableColumn<Row>[] = [
    { header: "名称", accessorKey: "name" },
    { id: "op", header: () => "操作", cell: ({ row }) => <button type="button">编辑 {row.id}</button> },
  ];

  function render(data: Row[] | undefined) {
    const el = document.createElement("div");
    const root = createRoot(el);
    act(() => root.render(<DataTable columns={columns} data={data} getRowId={(r) => r.id} empty="空" />));
    return { el, unmount: () => act(() => root.unmount()) };
  }

  it("渲染表头、单元格和自定义单元格", () => {
    const { el, unmount } = render([
      { id: "a1", name: "甲" },
      { id: "b2", name: "乙" },
    ]);
    expect([...el.querySelectorAll("th")].map((th) => th.textContent)).toEqual(["名称", "操作"]);
    expect([...el.querySelectorAll("tbody tr")].map((tr) => tr.textContent)).toEqual(["甲编辑 a1", "乙编辑 b2"]);
    unmount();
  });

  // 页面在组件里按权限拼列定义，每次渲染 cell 函数都是新的；单元格里的对话框、输入框不能因此
  // 被重新挂载（打开的对话框会在列表刷新时关掉）。
  it("列定义每次渲染都重新创建时，单元格里组件的状态保留", () => {
    function Counter() {
      const [n, setN] = useState(0);
      return (
        <button type="button" onClick={() => setN(n + 1)}>
          {n}
        </button>
      );
    }
    const el = document.createElement("div");
    const root = createRoot(el);
    const draw = (version: number) =>
      act(() =>
        root.render(
          <DataTable<Row>
            columns={[{ id: `op`, header: `v${version}`, cell: () => <Counter /> }]}
            data={[{ id: "a1", name: "甲" }]}
            getRowId={(r) => r.id}
          />,
        ),
      );
    draw(1);
    act(() => el.querySelector("button")!.click());
    expect(el.querySelector("button")!.textContent).toBe("1");
    draw(2);
    expect(el.querySelector("th")!.textContent).toBe("v2");
    expect(el.querySelector("button")!.textContent).toBe("1");
    act(() => root.unmount());
  });

  it("没有数据时显示空状态", () => {
    const { el, unmount } = render(undefined);
    expect(el.querySelector("tbody")?.textContent).toBe("空");
    unmount();
  });
});
