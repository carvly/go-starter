import { accountMeta } from "@/api/meta.gen";
import { Badge } from "@/components/ui/badge";

import type { Account } from "./queries";

const { fields } = accountMeta;

export function AccountBadges({ account }: { account: Pick<Account, "status" | "super_admin" | "must_change_password"> }) {
  return (
    <span className="flex flex-wrap gap-1">
      <Badge variant={account.status === "disabled" ? "destructive" : "secondary"}>{fields.status.labels[account.status]}</Badge>
      {account.super_admin && <Badge>{fields.super_admin.title}</Badge>}
      {account.must_change_password && <Badge variant="outline">{fields.must_change_password.title}</Badge>}
    </span>
  );
}
