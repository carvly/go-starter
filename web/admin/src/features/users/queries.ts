import { keepPreviousData, queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";
import type { z } from "zod";

import { api } from "@/api/client";
import { call } from "@/api/errors";
import { listUsersMeta } from "@/api/meta.gen";
import type { components } from "@/api/schema";
import { dayRange } from "@/lib/format";

export type User = components["schemas"]["User"];

// 筛选条件、排序和页码放在 URL 里（日期是 yyyy-mm-dd），刷新或分享链接时保留。
export type UserFilters = z.output<typeof listUsersMeta.search>;

export const usersQuery = ({ created_at_from, created_at_to, last_login_at_from, last_login_at_to, ...filters }: UserFilters) => {
  const createdAt = dayRange(created_at_from, created_at_to);
  const lastLoginAt = dayRange(last_login_at_from, last_login_at_to);
  const query = {
    ...filters,
    created_at_from: createdAt.from,
    created_at_to: createdAt.to,
    last_login_at_from: lastLoginAt.from,
    last_login_at_to: lastLoginAt.to,
    page: filters.page ?? 1,
    size: listUsersMeta.pageSize,
  };
  return queryOptions({
    queryKey: ["users", "list", query],
    queryFn: () => call(api.GET("/users", { params: { query } })),
    placeholderData: keepPreviousData,
  });
};

export const userQuery = (id: string) =>
  queryOptions({
    queryKey: ["users", "detail", id],
    queryFn: () => call(api.GET("/users/{id}", { params: { path: { id } } })),
  });

// 登录方式和会话列表是手写的只读接口，页面挂在用户详情里。
export const userIdentitiesQuery = (id: string) =>
  queryOptions({
    queryKey: ["users", "identities", id],
    queryFn: () => call(api.GET("/users/{id}/identities", { params: { path: { id } } })),
  });

export const userSessionsQuery = (id: string) =>
  queryOptions({
    queryKey: ["users", "sessions", id],
    queryFn: () => call(api.GET("/users/{id}/sessions", { params: { path: { id } } })),
  });

// 写操作都让列表和详情过期。
function useUserMutation<V, R>(fn: (v: V) => Promise<R>, handlesErrors?: readonly string[]) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["users"] }),
    meta: { handlesErrors },
  });
}

const path = (id: string) => ({ params: { path: { id } } });

export const useDisableUser = () => useUserMutation((id: string) => call(api.POST("/users/{id}/disable", path(id))));

export const useEnableUser = () => useUserMutation((id: string) => call(api.POST("/users/{id}/enable", path(id))));

export const useKickUserSession = (userId: string) =>
  useUserMutation((sid: string) => call(api.DELETE("/users/{id}/sessions/{sid}", { params: { path: { id: userId, sid } } })));
