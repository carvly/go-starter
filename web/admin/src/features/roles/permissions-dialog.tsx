import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { toast } from "@/components/ui/toast";

import { groupPermissions } from "./permission-groups";
import { type Role, permissionCatalogQuery, useSetRolePermissions } from "./queries";

export function PermissionsDialog({ role }: { role: Role }) {
  const [open, setOpen] = useState(false);
  const { data: catalog } = useQuery({ ...permissionCatalogQuery, enabled: open });
  const [selected, setSelected] = useState<string[]>(role.permissions);
  const save = useSetRolePermissions();
  const { groups, stale } = groupPermissions(catalog, role.permissions);
  const toggle = (codes: string[], on: boolean) =>
    setSelected((s) => (on ? [...new Set([...s, ...codes])] : s.filter((c) => !codes.includes(c))));

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) setSelected(role.permissions);
        setOpen(next);
      }}
    >
      <DialogTrigger render={<Button variant="outline" size="sm" />}>权限</DialogTrigger>
      <DialogContent className="max-h-[80svh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{role.name} 的权限</DialogTitle>
          <DialogDescription>勾选这个角色能做的事，按模块分组。保存后，对已分配这个角色的员工立即生效。</DialogDescription>
        </DialogHeader>
        {!catalog && <p className="text-sm text-muted-foreground">加载中…</p>}
        {catalog?.length === 0 && (
          <p className="text-sm text-muted-foreground">目前还没有可以勾选的权限：业务功能的后台页面上线后会出现在这里。</p>
        )}
        {groups.map((group) => {
          const codes = group.permissions.map((p) => p.code);
          const all = codes.every((c) => selected.includes(c));
          return (
            <fieldset key={group.tag} className="flex flex-col gap-2">
              <Label className="flex items-center gap-2">
                <Checkbox checked={all} onCheckedChange={(checked) => toggle(codes, !!checked)} />
                {group.title}
              </Label>
              <div className="ml-6 flex flex-col gap-2">
                {group.permissions.map((p) => (
                  <Label key={p.code} className="flex items-start gap-2 font-normal">
                    <Checkbox
                      className="mt-0.5"
                      checked={selected.includes(p.code)}
                      onCheckedChange={(checked) => toggle([p.code], !!checked)}
                    />
                    <span className="flex flex-col gap-0.5">
                      <span>{p.operations.join("、") || p.code}</span>
                      <span className="font-mono text-xs text-muted-foreground">{p.code}</span>
                    </span>
                  </Label>
                ))}
              </div>
            </fieldset>
          );
        })}
        {stale.length > 0 && (
          <p className="flex flex-wrap items-center gap-1 text-sm text-muted-foreground">
            已失效（接口已删除，保存时去掉）：
            {stale.map((code) => (
              <Badge key={code} variant="outline" className="font-mono">
                {code}
              </Badge>
            ))}
          </p>
        )}
        <DialogFooter>
          <Button
            disabled={!catalog || save.isPending}
            onClick={() =>
              save.mutate(
                { id: role.id, permissions: selected.filter((c) => !stale.includes(c)) },
                {
                  onSuccess: () => {
                    toast.add({ title: "已保存" });
                    setOpen(false);
                  },
                },
              )
            }
          >
            保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
