import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";

import { staffLoginRequestMeta } from "@/api/meta.gen";
import { FormError, onFormSubmit, useAppForm } from "@/components/form";
import { errorMessage } from "@/lib/server-error";

import { AuthCard } from "./auth-card";
import { useLogin } from "./queries";
import { safeRedirect } from "./schemas";

const { fields, form: loginSchema } = staffLoginRequestMeta;

export function LoginPage({ redirect }: { redirect?: string }) {
  const login = useLogin();
  const navigate = useNavigate();
  const [error, setError] = useState<string>();
  const form = useAppForm({
    defaultValues: { username: "", password: "" },
    validators: { onSubmit: loginSchema },
    onSubmit: async ({ value }) => {
      setError(undefined);
      try {
        // 校验器只校验、不回写转换后的值（如去掉用户名首尾空格），提交前自己 parse 一次。
        const me = await login.mutateAsync(loginSchema.parse(value));
        // 回跳地址带查询参数，必须用 href：to 只当作路径，会把 ? 编码进路径。
        if (me.must_change_password) {
          await navigate({ to: "/change-password", search: { redirect: safeRedirect(redirect) } });
        } else {
          await navigate({ href: safeRedirect(redirect) });
        }
      } catch (e) {
        setError(errorMessage(e));
      }
    },
  });

  return (
    <AuthCard title="登录管理后台">
      <form className="flex flex-col gap-4" onSubmit={onFormSubmit(form)}>
        <FormError message={error} />
        <form.AppField name="username">
          {(f) => <f.TextField label={fields.username.title} autoComplete="username" autoFocus />}
        </form.AppField>
        <form.AppField name="password">
          {(f) => <f.PasswordField label={fields.password.title} autoComplete="current-password" />}
        </form.AppField>
        <form.AppForm>
          <form.SubmitButton>登录</form.SubmitButton>
        </form.AppForm>
      </form>
    </AuthCard>
  );
}
