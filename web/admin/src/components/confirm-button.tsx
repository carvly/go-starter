import { type ComponentProps, type ReactNode, useState } from "react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";

type ConfirmButtonProps = {
  title: string;
  description?: ReactNode;
  confirmText?: string;
  onConfirm: () => void;
  children: ReactNode;
} & Pick<ComponentProps<typeof Button>, "variant" | "size" | "disabled">;

// ConfirmButton 用于不可撤销或影响他人的操作（停用、重设密码、删除）：先弹确认框。
// shadcn 的 AlertDialogAction 只是普通按钮、不会关闭对话框，所以这里自己控制开关。
export function ConfirmButton({
  title,
  description,
  confirmText = "确定",
  onConfirm,
  children,
  variant = "outline",
  size,
  disabled,
}: ConfirmButtonProps) {
  const [open, setOpen] = useState(false);
  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger render={<Button variant={variant} size={size} disabled={disabled} />}>{children}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          {description && <AlertDialogDescription>{description}</AlertDialogDescription>}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction
            onClick={() => {
              setOpen(false);
              onConfirm();
            }}
          >
            {confirmText}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
