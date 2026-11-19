import { type ComponentType, type ReactElement, type ReactNode, useState } from "react";

import { ApiError } from "@/api/errors";

import { FormError, SubmitButton, onFormSubmit } from "@/components/form";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";

// 编辑时带的 version 已经不是最新（接口返回 409 <资源>_conflict）。对应的 mutation 要把这个
// 错误码写进 meta.handlesErrors，由 FormDialog 显示，不再弹统一的错误提示。
export const isConflict = (error: ApiError) => error.status === 409 && error.code.endsWith("_conflict");

const conflictMessage = "这条数据在你打开表单之后被别人修改过，表单已换成最新内容。请确认后重新修改并保存。";

type FormApi = {
  handleSubmit: () => Promise<unknown>;
  AppForm: ComponentType<{ children?: ReactNode }>;
};

// FormDialog 是"按钮 + 对话框里的表单"：新建、编辑、带输入的动作（如调库存）都用它。
// - onOpen 在每次打开时调用，用来把表单重置成当前数据（编辑）或空白（新建、动作），
//   上次没提交的内容留着容易误提交；
// - 提交成功后由调用方的 onSubmit 调 close() 关闭；
// - onConflict 只用于编辑：保存时发现数据已被别人修改，取回最新数据并重置表单（带上新的
//   version），对话框保持打开并说明发生了什么。
export function FormDialog({
  trigger,
  title,
  description,
  submitText,
  form,
  open,
  onOpenChange,
  onOpen,
  onConflict,
  children,
}: {
  trigger: ReactElement;
  title: ReactNode;
  description?: ReactNode;
  submitText: string;
  form: FormApi;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onOpen?: () => void;
  onConflict?: () => Promise<void>;
  children: ReactNode;
}) {
  const [notice, setNotice] = useState<string>();
  const handleConflict = async (error: ApiError) => {
    if (!onConflict || !isConflict(error)) return;
    try {
      await onConflict();
      setNotice(conflictMessage);
    } catch {
      setNotice("这条数据在你打开表单之后被别人修改过，取最新内容失败。请关闭对话框后重新打开。");
    }
  };
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) {
          setNotice(undefined);
          onOpen?.();
        }
        onOpenChange(next);
      }}
    >
      <DialogTrigger render={trigger} />
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description && <DialogDescription>{description}</DialogDescription>}
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            setNotice(undefined);
            onFormSubmit(form, (error) => void handleConflict(error))(e);
          }}
        >
          <FormError message={notice} />
          {children}
          <form.AppForm>
            <SubmitButton>{submitText}</SubmitButton>
          </form.AppForm>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// useDialog 是 FormDialog 的开关状态：onSubmit 里成功后调 close()。
export function useDialog() {
  const [open, setOpen] = useState(false);
  return { open, onOpenChange: setOpen, close: () => setOpen(false) };
}

// DescriptionList 是详情页的"标签：值"列表，标签列固定宽度。
export function DescriptionList({ children }: { children: ReactNode }) {
  return <dl className="divide-y">{children}</dl>;
}

export function DescriptionItem({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[8rem_1fr] gap-2 py-1.5 text-sm">
      <dt className="text-muted-foreground">{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}
