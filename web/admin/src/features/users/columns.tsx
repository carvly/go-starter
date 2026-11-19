import { Link } from "@tanstack/react-router";

import { userMeta, userSessionClientMeta, userStatusMeta } from "@/api/meta.gen";
import type { DataTableColumn } from "@/components/data-table";
import { DateTimeCell, EnumCell } from "@/components/data-table/cells";

import type { User } from "./queries";

const { fields } = userMeta;

// 列表的列。昵称链接到详情页，修改和其它操作都在详情页上。
export const columns: DataTableColumn<User>[] = [
  {
    header: fields.display_name.title,
    cell: ({ row }) => (
      <Link to="/users/$id" params={{ id: row.original.id }} className="font-medium underline-offset-4 hover:underline">
        {row.original.display_name}
      </Link>
    ),
  },
  { header: fields.status.title, cell: ({ row }) => <EnumCell value={row.original.status} labels={userStatusMeta.labels} /> },
  {
    header: fields.signup_client.title,
    cell: ({ row }) => <EnumCell value={row.original.signup_client} labels={userSessionClientMeta.labels} />,
  },
  { header: fields.last_login_at.title, cell: ({ row }) => <DateTimeCell value={row.original.last_login_at} /> },
  { header: fields.created_at.title, cell: ({ row }) => <DateTimeCell value={row.original.created_at} /> },
];
