import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { call } from "@/api/errors";
import type { components } from "@/api/schema";

export type Role = components["schemas"]["Role"];
export type PermissionInfo = components["schemas"]["Permission"];

export const rolesQuery = queryOptions({
  queryKey: ["roles"],
  queryFn: async () => (await call(api.GET("/roles"))).items,
});

// 可授予的权限码取自 admin.yaml，随版本发布才变化。
export const permissionCatalogQuery = queryOptions({
  queryKey: ["permissions", "catalog"],
  queryFn: async () => (await call(api.GET("/permissions"))).items,
  staleTime: Infinity,
});

// 新建、修改角色时显示在"角色名"字段下的错误码。
export const ROLE_NAME_ERRORS = ["role_name_taken"] as const;

function useRoleMutation<V, R>(fn: (v: V) => Promise<R>, handlesErrors?: readonly string[]) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: fn,
    meta: { handlesErrors },
    // 角色变化会影响员工的有效权限（包括自己）；删除角色还会改变员工绑定的角色。
    onSuccess: () => Promise.all(["roles", "me", "accounts"].map((key) => queryClient.invalidateQueries({ queryKey: [key] }))),
  });
}

type RoleBody = components["schemas"]["RoleRequest"];

export const useCreateRole = () => useRoleMutation((body: RoleBody) => call(api.POST("/roles", { body })), ROLE_NAME_ERRORS);
export const useUpdateRole = () =>
  useRoleMutation(
    ({ id, ...body }: RoleBody & { id: string }) => call(api.PUT("/roles/{id}", { params: { path: { id } }, body })),
    ROLE_NAME_ERRORS,
  );
export const useDeleteRole = () => useRoleMutation((id: string) => call(api.DELETE("/roles/{id}", { params: { path: { id } } })));
export const useSetRolePermissions = () =>
  useRoleMutation(({ id, permissions }: { id: string; permissions: string[] }) =>
    call(api.PUT("/roles/{id}/permissions", { params: { path: { id } }, body: { permissions } })),
  );
