// 表单的唯一入口：features 只用这里的 useAppForm 和字段组件，不直接 import
// @tanstack/react-form（oxlint 的 no-restricted-imports 检查）。校验用 zod schema
// （Standard Schema，直接作为 validators.onSubmit）。
import { createFormHook } from "@tanstack/react-form";
import { CircleAlert } from "lucide-react";
import type { FormEvent } from "react";

import { ApiError } from "@/api/errors";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

import {
  DateField,
  DateTimeField,
  MoneyField,
  NumberField,
  PasswordField,
  SelectField,
  SwitchField,
  TextareaField,
  TextField,
  fieldContext,
  formContext,
  useFormContext,
} from "./fields";

export function SubmitButton({ children }: { children: string }) {
  const form = useFormContext();
  return (
    <form.Subscribe selector={(s) => s.isSubmitting}>
      {(submitting) => (
        <Button type="submit" disabled={submitting} className="w-full">
          {submitting ? "提交中…" : children}
        </Button>
      )}
    </form.Subscribe>
  );
}

// onFormSubmit 是 <form onSubmit> 的统一写法。onSubmit 里 await 的 mutation 失败时，
// handleSubmit 会把错误再抛出来：ApiError 已经由统一错误处理提示、或由表单自己显示在字段上，
// 这里吞掉，不让它变成未捕获的异常；其它异常（代码 bug）照常抛出。
export function onFormSubmit(form: { handleSubmit: () => Promise<unknown> }, onApiError?: (error: ApiError) => void) {
  return (e: FormEvent) => {
    e.preventDefault();
    form.handleSubmit().catch((error: unknown) => {
      if (!(error instanceof ApiError)) throw error;
      onApiError?.(error);
    });
  };
}

// 归不到具体字段的提交错误（如登录失败）显示在表单顶部。带图标和底色：只有边框和红字时，
// 看起来像一个输入框。
export function FormError({ message }: { message: string | undefined }) {
  if (!message) return null;
  return (
    <Alert variant="destructive" className="border-destructive/30 bg-destructive/5">
      <CircleAlert />
      <AlertDescription>{message}</AlertDescription>
    </Alert>
  );
}

export const { useAppForm } = createFormHook({
  fieldContext,
  formContext,
  fieldComponents: {
    TextField,
    PasswordField,
    TextareaField,
    SwitchField,
    NumberField,
    MoneyField,
    SelectField,
    DateField,
    DateTimeField,
  },
  formComponents: { SubmitButton },
});
