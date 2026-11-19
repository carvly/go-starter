import { Plus } from "lucide-react";
import { useState } from "react";
import { createAccountRequestMeta } from "@/api/meta.gen";
import { onFormSubmit, useAppForm } from "@/components/form";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";

import { fieldError } from "@/lib/server-error";

import { USERNAME_ERRORS, useCreateAccount } from "./queries";
import { type TempPassword, TempPasswordDialog } from "./temp-password-dialog";

const { fields } = createAccountRequestMeta;

// 其余字段的约束来自 api/admin.yaml（src/api/meta.gen.ts）；用户名的格式与后端 staff 的
// usernamePattern 一致（domain 里再校验一次），spec 里只写了说明。
const schema = createAccountRequestMeta.form.extend({
  username: createAccountRequestMeta.form.shape.username
    .toLowerCase()
    .regex(/^[a-z0-9][a-z0-9._-]{1,63}$/, "2–64 个字符：小写字母、数字和 . _ -，以字母或数字开头"),
});

export function CreateAccountDialog() {
  const [open, setOpen] = useState(false);
  const [created, setCreated] = useState<TempPassword>();
  const create = useCreateAccount();
  const form = useAppForm({
    defaultValues: { username: "", display_name: "", super_admin: false },
    validators: { onSubmit: schema },
    onSubmit: async ({ value, formApi }) => {
      const body = schema.parse(value);
      let res;
      try {
        res = await create.mutateAsync({ ...body, display_name: body.display_name || undefined });
      } catch (e) {
        const message = fieldError(e, USERNAME_ERRORS);
        if (!message) throw e;
        formApi.setFieldMeta("username", (m) => ({ ...m, errorMap: { ...m.errorMap, onServer: message } }));
        return;
      }
      setOpen(false);
      formApi.reset();
      setCreated({
        username: res.account.username,
        password: res.temporary_password,
        expiresAt: res.temporary_password_expires_at,
        assignRoleTo: res.account.super_admin ? undefined : res.account.id,
      });
    },
  });

  return (
    <>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogTrigger render={<Button />}>
          <Plus />
          新建员工
        </DialogTrigger>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>新建员工</DialogTitle>
          </DialogHeader>
          <form className="flex flex-col gap-4" onSubmit={onFormSubmit(form)}>
            <form.AppField name="username">
              {(f) => <f.TextField label={fields.username.title} autoComplete="off" autoFocus />}
            </form.AppField>
            <form.AppField name="display_name">
              {(f) => <f.TextField label={fields.display_name.title} description="不填时同用户名" />}
            </form.AppField>
            <form.AppField name="super_admin">
              {(f) => <f.SwitchField label={fields.super_admin.title} description="可以管理员工、角色，查看操作日志，并执行所有操作" />}
            </form.AppField>
            <form.AppForm>
              <form.SubmitButton>创建并生成临时密码</form.SubmitButton>
            </form.AppForm>
          </form>
        </DialogContent>
      </Dialog>
      <TempPasswordDialog value={created} onClose={() => setCreated(undefined)} />
    </>
  );
}
