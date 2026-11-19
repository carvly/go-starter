import { type ComponentProps, type ReactNode, useState } from "react";
import { roleRequestMeta } from "@/api/meta.gen";
import { onFormSubmit, useAppForm } from "@/components/form";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { toast } from "@/components/ui/toast";

import { fieldError } from "@/lib/server-error";

import { ROLE_NAME_ERRORS, type Role, useCreateRole, useUpdateRole } from "./queries";

const { fields, form: schema } = roleRequestMeta;

// RoleFormDialog 新建（不传 role）或修改角色名和说明。
type RoleFormDialogProps = { role?: Role; children: ReactNode } & Pick<ComponentProps<typeof Button>, "variant" | "size">;

export function RoleFormDialog({ role, children, variant, size }: RoleFormDialogProps) {
  const [open, setOpen] = useState(false);
  const create = useCreateRole();
  const update = useUpdateRole();
  const form = useAppForm({
    defaultValues: { name: role?.name ?? "", description: role?.description ?? "" },
    validators: { onSubmit: schema },
    onSubmit: async ({ value, formApi }) => {
      const body = schema.parse(value);
      try {
        if (role) await update.mutateAsync({ id: role.id, ...body });
        else await create.mutateAsync(body);
      } catch (e) {
        const message = fieldError(e, ROLE_NAME_ERRORS);
        if (!message) throw e;
        formApi.setFieldMeta("name", (m) => ({ ...m, errorMap: { ...m.errorMap, onServer: message } }));
        return;
      }
      setOpen(false);
      if (!role) {
        formApi.reset();
        toast.add({ title: "角色已创建", description: "点这一行的“权限”勾选它能做的事，在员工详情里把它分配给员工，两步顺序不限。" });
      }
    },
  });

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button variant={variant} size={size} />}>{children}</DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{role ? "修改角色" : "新建角色"}</DialogTitle>
        </DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={onFormSubmit(form)}>
          <form.AppField name="name">{(f) => <f.TextField label={fields.name.title} autoFocus />}</form.AppField>
          <form.AppField name="description">{(f) => <f.TextareaField label={fields.description.title} />}</form.AppField>
          <form.AppForm>
            <form.SubmitButton>保存</form.SubmitButton>
          </form.AppForm>
        </form>
      </DialogContent>
    </Dialog>
  );
}
