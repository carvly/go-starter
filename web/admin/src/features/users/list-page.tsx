import { useQuery } from "@tanstack/react-query";

import { listUsersMeta, userStatusMeta } from "@/api/meta.gen";
import { DataTable, PagePager } from "@/components/data-table";
import { DateRangeFilter, EnumFilter, FilterBar, SearchInput } from "@/components/filters";
import { TitleWithTip } from "@/components/info-tip";
import { enumOptions } from "@/lib/enum-options";

import { columns } from "./columns";
import { type UserFilters, usersQuery } from "./queries";

export function UsersPage({ filters, onChange }: { filters: UserFilters; onChange: (f: UserFilters) => void }) {
  const { data, isLoading } = useQuery(usersQuery(filters));
  // 筛选条件或排序变了回到第一页（page 不带上）。
  const filter = (next: Omit<UserFilters, "page">) => onChange({ ...filters, ...next, page: undefined });
  const filtered = Object.entries(filters).some(([k, v]) => k !== "page" && k !== "sort" && v !== undefined);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-4">
        <h1 className="text-lg font-medium">
          <TitleWithTip tip={"前台用户。账号由登录创建，后台只能查看、停用和启用"}>用户</TitleWithTip>
        </h1>
      </div>
      <FilterBar>
        <SearchInput value={filters.q} placeholder="按昵称搜索" onSearch={(q) => filter({ q })} />
        <EnumFilter label="状态" options={enumOptions(userStatusMeta)} value={filters.status} onChange={(status) => filter({ status })} />
        <DateRangeFilter
          id="users-created_at"
          label="创建时间"
          value={{ from: filters.created_at_from, to: filters.created_at_to }}
          onChange={(r) => filter({ created_at_from: r.from, created_at_to: r.to })}
        />
        <DateRangeFilter
          id="users-last_login_at"
          label="最近登录"
          value={{ from: filters.last_login_at_from, to: filters.last_login_at_to }}
          onChange={(r) => filter({ last_login_at_from: r.from, last_login_at_to: r.to })}
        />
      </FilterBar>
      <DataTable
        columns={columns}
        data={data?.items}
        loading={isLoading}
        getRowId={(v) => v.id}
        empty={filtered ? "没有符合条件的用户" : "还没有用户"}
      />
      <PagePager
        page={filters.page ?? 1}
        size={listUsersMeta.pageSize}
        total={data?.total}
        totalCapped={data?.total_capped}
        onChange={(page) => onChange({ ...filters, page })}
      />
    </div>
  );
}
