import { keepPreviousData, queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { call } from "@/api/errors";
import type { components } from "@/api/schema";

export type Account = components["schemas"]["AccountDetail"];

// needsRole：启用中的普通员工没有任何角色，登录后只能看到首页，页面上要提示去分配。
export function needsRole(account: Pick<Account, "super_admin" | "status" | "role_ids">): boolean {
  return !account.super_admin && account.status === "active" && account.role_ids.length === 0;
}

export const accountsQuery = (cursor?: string, limit = 20) =>
  queryOptions({
    queryKey: ["accounts", "list", { cursor, limit }],
    queryFn: () => call(api.GET("/accounts", { params: { query: { cursor, limit } } })),
    placeholderData: keepPreviousData,
  });

export const accountQuery = (id: string) =>
  queryOptions({
    queryKey: ["accounts", "detail", id],
    queryFn: () => call(api.GET("/accounts/{id}", { params: { path: { id } } })),
  });

// 员工的写操作都会让列表、详情和"超级管理员数量"过期。
// 新建员工时显示在"用户名"字段下的错误码。
export const USERNAME_ERRORS = ["username_taken"] as const;

function useAccountMutation<V, R>(fn: (v: V) => Promise<R>, handlesErrors?: readonly string[]) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["accounts"] }),
    meta: { handlesErrors },
  });
}

const path = (id: string) => ({ params: { path: { id } } });

export const useCreateAccount = () =>
  useAccountMutation((body: components["schemas"]["CreateAccountRequest"]) => call(api.POST("/accounts", { body })), USERNAME_ERRORS);
export const useDisableAccount = () => useAccountMutation((id: string) => call(api.POST("/accounts/{id}/disable", path(id))));
export const useEnableAccount = () => useAccountMutation((id: string) => call(api.POST("/accounts/{id}/enable", path(id))));
export const useResetPassword = () => useAccountMutation((id: string) => call(api.POST("/accounts/{id}/reset-password", path(id))));
export const useRevokeSessions = () => useAccountMutation((id: string) => call(api.POST("/accounts/{id}/revoke-sessions", path(id))));
export const useSetSuperAdmin = () =>
  useAccountMutation(({ id, superAdmin }: { id: string; superAdmin: boolean }) =>
    call(api.PUT("/accounts/{id}/super-admin", { ...path(id), body: { super_admin: superAdmin } })),
  );
export const useSetAccountRoles = () =>
  useAccountMutation(({ id, roleIds }: { id: string; roleIds: string[] }) =>
    call(api.PUT("/accounts/{id}/roles", { ...path(id), body: { role_ids: roleIds } })),
  );
