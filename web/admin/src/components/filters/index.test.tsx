import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

import { BoolFilter, EnumFilter, SearchInput, SortSelect } from ".";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function render(node: React.ReactNode) {
  const root = createRoot(document.body.appendChild(document.createElement("div")));
  act(() => root.render(node));
  return root;
}

async function choose(trigger: string, option: string) {
  await act(async () => document.querySelector<HTMLElement>(`[aria-label="${trigger}"]`)!.click());
  const item = [...document.querySelectorAll<HTMLElement>("[role=option]")].find((el) => el.textContent === option)!;
  await act(async () => item.click());
}

afterEach(() => {
  document.body.innerHTML = "";
});

describe("EnumFilter", () => {
  const options = [
    { value: "pending", label: "处理中" },
    { value: "failed", label: "失败" },
  ] as const;

  it("没选时显示全部，选中后显示文案", () => {
    const root = render(<EnumFilter label="状态" options={options} value={undefined} onChange={() => {}} allLabel="全部状态" />);
    expect(document.querySelector('[aria-label="状态"]')!.textContent).toContain("全部状态");
    act(() => root.render(<EnumFilter label="状态" options={options} value={["pending", "failed"]} onChange={() => {}} />));
    expect(document.querySelector('[aria-label="状态"]')!.textContent).toContain("处理中、失败");
  });

  it("多选：在已选的基础上加，取消最后一个时回调 undefined", async () => {
    const onChange = vi.fn();
    const root = render(<EnumFilter label="状态" options={options} value={["pending"]} onChange={onChange} />);
    await choose("状态", "失败");
    expect(onChange).toHaveBeenLastCalledWith(["pending", "failed"]);
    act(() => root.render(<EnumFilter label="状态" options={options} value={["pending"]} onChange={onChange} />));
    const item = [...document.querySelectorAll<HTMLElement>("[role=option]")].find((el) => el.textContent === "处理中")!;
    await act(async () => item.click());
    expect(onChange).toHaveBeenLastCalledWith(undefined);
  });
});

describe("BoolFilter", () => {
  it("全部 / 是 / 否 对应 undefined / true / false", async () => {
    const onChange = vi.fn();
    render(<BoolFilter label="上架" value={undefined} onChange={onChange} labels={["已上架", "已下架"]} />);
    await choose("上架", "已下架");
    expect(onChange).toHaveBeenLastCalledWith(false);
    await choose("上架", "全部");
    expect(onChange).toHaveBeenLastCalledWith(undefined);
  });
});

describe("SearchInput", () => {
  it("提交去掉首尾空格，空关键字回调 undefined", () => {
    const onSearch = vi.fn();
    render(<SearchInput value="键盘" placeholder="按名称搜索" onSearch={onSearch} />);
    const input = document.querySelector<HTMLInputElement>("input")!;
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "  ");
    act(() => input.dispatchEvent(new Event("input", { bubbles: true })));
    act(() => input.form!.requestSubmit());
    expect(onSearch).toHaveBeenLastCalledWith(undefined);
  });
});

describe("SortSelect", () => {
  const options = [
    { value: "-created_at", label: "创建时间降序" },
    { value: "created_at", label: "创建时间升序" },
  ] as const;

  it("没选时显示默认排序；选回默认的那一项回调 undefined", async () => {
    const onChange = vi.fn();
    const root = render(<SortSelect options={options} value={undefined} defaultValue="-created_at" onChange={onChange} />);
    expect(document.querySelector('[aria-label="排序"]')!.textContent).toContain("创建时间降序");
    await choose("排序", "创建时间升序");
    expect(onChange).toHaveBeenLastCalledWith("created_at");
    act(() => root.render(<SortSelect options={options} value="created_at" defaultValue="-created_at" onChange={onChange} />));
    await choose("排序", "创建时间降序");
    expect(onChange).toHaveBeenLastCalledWith(undefined);
  });
});
