import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { z } from "zod";

import { enumInput, moneyInput } from "@/lib/field-schemas";

import { onFormSubmit, useAppForm } from ".";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const schema = z.object({
  price: moneyInput({ title: "单价", required: true, min: 1 }),
  level: z.enum(["low", "high"], { error: "请选择" }),
  day: z.iso.date(),
  at: z.iso.datetime({ offset: true }),
});

function Harness({ onSubmit }: { onSubmit: (v: z.output<typeof schema>) => void }) {
  const form = useAppForm({
    defaultValues: { price: "", level: "", day: "2026-11-09", at: "" },
    validators: { onSubmit: schema },
    onSubmit: ({ value }) => onSubmit(schema.parse(value)),
  });
  return (
    <form onSubmit={onFormSubmit(form)}>
      <form.AppField name="price">{(f) => <f.MoneyField label="单价" />}</form.AppField>
      <form.AppField name="level">
        {(f) => (
          <f.SelectField
            label="级别"
            options={[
              { value: "low", label: "低" },
              { value: "high", label: "高" },
            ]}
          />
        )}
      </form.AppField>
      <form.AppField name="day">{(f) => <f.DateField label="日期" />}</form.AppField>
      <form.AppField name="at">{(f) => <f.DateTimeField label="时间" />}</form.AppField>
    </form>
  );
}

function type(input: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

const field = (label: string) =>
  [...document.querySelectorAll<HTMLElement>("[data-slot=field]")].find((el) => el.querySelector("label")?.textContent === label)!;

afterEach(() => {
  document.body.innerHTML = "";
});

describe("字段组件", () => {
  it("没填的字段提交时显示错误；填好后按接口的类型提交", async () => {
    const onSubmit = vi.fn();
    const root = createRoot(document.body.appendChild(document.createElement("div")));
    act(() => root.render(<Harness onSubmit={onSubmit} />));
    const form = document.querySelector("form")!;

    await act(async () => form.requestSubmit());
    expect(onSubmit).not.toHaveBeenCalled();
    expect(field("单价").textContent).toContain("请输入单价");
    expect(field("级别").textContent).toContain("请选择");
    expect(field("日期").textContent).toContain("2026-11-09");

    act(() => type(document.querySelector<HTMLInputElement>('input[name="price"]')!, "12.5"));

    await act(async () => document.querySelector<HTMLElement>("#level")!.click());
    const high = [...document.querySelectorAll<HTMLElement>("[role=option]")].find((el) => el.textContent === "高")!;
    await act(async () => high.click());

    await act(async () => document.querySelector<HTMLElement>("#at")!.click());
    act(() => type(document.querySelector<HTMLInputElement>('input[aria-label="时间"]')!, "08:30"));

    await act(async () => form.requestSubmit());
    expect(onSubmit).toHaveBeenCalledTimes(1);
    const v = onSubmit.mock.calls[0][0];
    expect(v).toMatchObject({ price: 1250, level: "high", day: "2026-11-09" });
    const at = new Date(v.at);
    expect([at.getHours(), at.getMinutes()]).toEqual([8, 30]);
    act(() => root.unmount());
  });

  it("clearable 的下拉框选过之后可以清回未选择", async () => {
    const onSubmit = vi.fn();
    const optional = z.object({ color: enumInput(["red", "blue"], { title: "颜色" }) });
    function Optional() {
      const form = useAppForm({
        defaultValues: { color: "red" },
        validators: { onSubmit: optional },
        onSubmit: ({ value }) => onSubmit(optional.parse(value)),
      });
      return (
        <form onSubmit={onFormSubmit(form)}>
          <form.AppField name="color">
            {(f) => (
              <f.SelectField
                label="颜色"
                clearable
                options={[
                  { value: "red", label: "红" },
                  { value: "blue", label: "蓝" },
                ]}
              />
            )}
          </form.AppField>
        </form>
      );
    }
    const root = createRoot(document.body.appendChild(document.createElement("div")));
    act(() => root.render(<Optional />));
    await act(async () => document.querySelector<HTMLElement>('[aria-label="清除"]')!.click());
    expect(document.querySelector('[aria-label="清除"]')).toBeNull();
    await act(async () => document.querySelector("form")!.requestSubmit());
    expect(onSubmit).toHaveBeenCalledWith({ color: undefined });
    act(() => root.unmount());
  });
});
