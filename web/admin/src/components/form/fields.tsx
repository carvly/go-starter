// 字段组件按 0005 的字段类型矩阵一种类型一个，经 useAppForm 的 form.AppField 使用。
// 表单里的值一律是字符串（开关是布尔值）：输入框里的内容先原样保存，提交时再由 zod schema
// 校验并转换（整数、元 → 分，见 src/lib/field-schemas.ts）。
import { createFormHookContexts } from "@tanstack/react-form";
import { CalendarIcon, X } from "lucide-react";
import { type ComponentProps, type ReactNode, useState } from "react";
import { zhCN } from "react-day-picker/locale";

import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { formatDay, parseDay } from "@/lib/format";

export const { fieldContext, formContext, useFieldContext, useFormContext } = createFormHookContexts();

type BaseProps = { label: string; description?: string };

// errors 里既可能是 zod 的 issue（有 message），也可能是字符串（服务端错误写回时用）。
function toErrors(errors: unknown[]): { message?: string }[] {
  return errors.map((e) => (typeof e === "string" ? { message: e } : (e as { message?: string })));
}

// useStringField 是所有输入类字段共用的状态：显示错误的时机、值的修改。
// 服务端返回的、能归到某个字段的错误（如"当前密码不正确"）写进字段的 errorMap.onServer：
// form.setFieldMeta(name, (m) => ({ ...m, errorMap: { ...m.errorMap, onServer: message } }))。
// 用户一改这个字段就清掉，不必等下次提交。
function useStringField() {
  const field = useFieldContext<string>();
  const invalid = field.state.meta.isTouched && field.state.meta.errors.length > 0;
  const change = (value: string) => {
    if (field.state.meta.errorMap.onServer !== undefined) {
      field.setMeta((m) => ({ ...m, errorMap: { ...m.errorMap, onServer: undefined } }));
    }
    field.handleChange(value);
  };
  return { field, invalid, change };
}

function FieldShell({
  label,
  description,
  htmlFor,
  invalid,
  errors,
  children,
}: BaseProps & { htmlFor: string; invalid: boolean; errors: unknown[]; children: ReactNode }) {
  return (
    <Field data-invalid={invalid || undefined}>
      <FieldLabel htmlFor={htmlFor}>{label}</FieldLabel>
      {children}
      {description && <FieldDescription>{description}</FieldDescription>}
      {invalid && <FieldError errors={toErrors(errors)} />}
    </Field>
  );
}

type TextFieldProps = BaseProps & Pick<ComponentProps<typeof Input>, "type" | "autoComplete" | "autoFocus" | "placeholder" | "inputMode">;

export function TextField({ label, description, type = "text", ...props }: TextFieldProps) {
  const { field, invalid, change } = useStringField();
  return (
    <FieldShell label={label} description={description} htmlFor={field.name} invalid={invalid} errors={field.state.meta.errors}>
      <Input
        id={field.name}
        name={field.name}
        type={type}
        value={field.state.value}
        onBlur={field.handleBlur}
        onChange={(e) => change(e.target.value)}
        aria-invalid={invalid || undefined}
        {...props}
      />
    </FieldShell>
  );
}

export function PasswordField(props: Omit<TextFieldProps, "type">) {
  return <TextField type="password" {...props} />;
}

// NumberField 是整数（矩阵的 int）。不用 type="number"：它会接受 1e3、滚轮会误改数值，
// 清空和非法输入时拿到的都是空字符串，没法提示"请输入整数"。
export function NumberField(props: Omit<TextFieldProps, "type" | "inputMode">) {
  return <TextField inputMode="numeric" autoComplete="off" {...props} />;
}

// MoneyField 按元输入（最多两位小数），schema 用 moneyInput() 换成分提交。
export function MoneyField({ label, description, ...props }: Omit<TextFieldProps, "type" | "inputMode">) {
  const { field, invalid, change } = useStringField();
  return (
    <FieldShell label={label} description={description} htmlFor={field.name} invalid={invalid} errors={field.state.meta.errors}>
      <div className="flex items-center gap-2">
        <Input
          id={field.name}
          name={field.name}
          inputMode="decimal"
          autoComplete="off"
          value={field.state.value}
          onBlur={field.handleBlur}
          onChange={(e) => change(e.target.value)}
          aria-invalid={invalid || undefined}
          {...props}
        />
        <span className="shrink-0 text-sm text-muted-foreground">元</span>
      </div>
    </FieldShell>
  );
}

export function TextareaField({ label, description }: BaseProps) {
  const { field, invalid, change } = useStringField();
  return (
    <FieldShell label={label} description={description} htmlFor={field.name} invalid={invalid} errors={field.state.meta.errors}>
      <Textarea
        id={field.name}
        name={field.name}
        value={field.state.value}
        onBlur={field.handleBlur}
        onChange={(e) => change(e.target.value)}
        aria-invalid={invalid || undefined}
      />
    </FieldShell>
  );
}

export function SwitchField({ label, description }: BaseProps) {
  const field = useFieldContext<boolean>();
  return (
    <Field orientation="horizontal">
      <Switch id={field.name} checked={field.state.value} onCheckedChange={(checked) => field.handleChange(checked)} />
      <FieldLabel htmlFor={field.name}>{label}</FieldLabel>
      {description && <FieldDescription>{description}</FieldDescription>}
    </Field>
  );
}

export type Option = { value: string; label: string };

// SelectField 是枚举（矩阵的 enum）。值为空字符串表示还没选，必填由 schema 判断。clearable 用于非必填
// 的枚举：选过之后可以清回"未选择"（下拉框本身没有空选项）。
export function SelectField({
  label,
  description,
  options,
  placeholder = "请选择",
  clearable,
}: BaseProps & { options: readonly Option[]; placeholder?: string; clearable?: boolean }) {
  const { field, invalid, change } = useStringField();
  const items = options.map((o) => ({ value: o.value, label: o.label }));
  return (
    <FieldShell label={label} description={description} htmlFor={field.name} invalid={invalid} errors={field.state.meta.errors}>
      <div className="flex items-center gap-1">
        <Select
          items={items}
          value={field.state.value || null}
          onValueChange={(v) => {
            change(v ?? "");
            field.handleBlur();
          }}
        >
          <SelectTrigger id={field.name} className="w-full flex-1" aria-invalid={invalid || undefined}>
            <SelectValue placeholder={placeholder} />
          </SelectTrigger>
          <SelectContent>
            {items.map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {o.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {clearable && field.state.value && <ClearButton onClear={() => change("")} />}
      </div>
    </FieldShell>
  );
}

function PickerTrigger({ id, empty, invalid, children }: { id: string; empty: boolean; invalid: boolean; children: ReactNode }) {
  return (
    <PopoverTrigger
      render={<Button id={id} variant="outline" className="flex-1 justify-start font-normal data-[empty=true]:text-muted-foreground" />}
      data-empty={empty}
      aria-invalid={invalid || undefined}
    >
      <CalendarIcon />
      {children}
    </PopoverTrigger>
  );
}

function ClearButton({ onClear }: { onClear: () => void }) {
  return (
    <Button variant="ghost" size="icon-sm" aria-label="清除" onClick={onClear}>
      <X />
    </Button>
  );
}

// DateField 是日期（矩阵的 date），值是 yyyy-mm-dd，和接口的 format: date 一致。
export function DateField({ label, description, placeholder = "选择日期" }: BaseProps & { placeholder?: string }) {
  const { field, invalid, change } = useStringField();
  const [open, setOpen] = useState(false);
  const selected = parseDay(field.state.value);
  return (
    <FieldShell label={label} description={description} htmlFor={field.name} invalid={invalid} errors={field.state.meta.errors}>
      <div className="flex items-center gap-1">
        <Popover open={open} onOpenChange={setOpen}>
          <PickerTrigger id={field.name} empty={!selected} invalid={invalid}>
            {selected ? field.state.value : placeholder}
          </PickerTrigger>
          <PopoverContent className="w-auto p-0" align="start">
            <Calendar
              mode="single"
              locale={zhCN}
              captionLayout="dropdown"
              defaultMonth={selected}
              selected={selected}
              onSelect={(d) => {
                change(d ? formatDay(d) : "");
                field.handleBlur();
                setOpen(false);
              }}
            />
          </PopoverContent>
        </Popover>
        {selected && <ClearButton onClear={() => change("")} />}
      </div>
    </FieldShell>
  );
}

const pad = (n: number) => String(n).padStart(2, "0");

// DateTimeField 是时间点（矩阵的 datetime），值是 RFC 3339（UTC），页面上按浏览器所在时区选日期和时分。
export function DateTimeField({ label, description, placeholder = "选择时间" }: BaseProps & { placeholder?: string }) {
  const { field, invalid, change } = useStringField();
  const value = field.state.value ? new Date(field.state.value) : undefined;
  const current = value && !Number.isNaN(value.getTime()) ? value : undefined;
  const time = current ? `${pad(current.getHours())}:${pad(current.getMinutes())}` : "00:00";
  const set = (day: Date, hhmm: string) => {
    const [h, m] = hhmm.split(":").map(Number);
    change(new Date(day.getFullYear(), day.getMonth(), day.getDate(), h, m).toISOString());
  };
  return (
    <FieldShell label={label} description={description} htmlFor={field.name} invalid={invalid} errors={field.state.meta.errors}>
      <div className="flex items-center gap-1">
        <Popover onOpenChange={(open) => !open && field.handleBlur()}>
          <PickerTrigger id={field.name} empty={!current} invalid={invalid}>
            {current ? `${formatDay(current)} ${time}` : placeholder}
          </PickerTrigger>
          <PopoverContent className="w-auto p-0" align="start">
            <Calendar
              mode="single"
              locale={zhCN}
              captionLayout="dropdown"
              defaultMonth={current}
              selected={current}
              onSelect={(d) => d && set(d, time)}
            />
            <div className="flex items-center gap-2 border-t p-3">
              <span className="text-sm text-muted-foreground">时间</span>
              <Input
                type="time"
                aria-label="时间"
                className="w-32"
                value={time}
                onChange={(e) => e.target.value && set(current ?? new Date(), e.target.value)}
              />
            </div>
          </PopoverContent>
        </Popover>
        {current && <ClearButton onClear={() => change("")} />}
      </div>
    </FieldShell>
  );
}
