import { queryOptions, useMutation, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { call } from "@/api/errors";
import type { components } from "@/api/schema";

export type Session = components["schemas"]["Session"];

export const mySessionsQuery = queryOptions({
  queryKey: ["me", "sessions"],
  queryFn: async () => (await call(api.GET("/me/sessions"))).items,
});

export function useRevokeMySession() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => call(api.DELETE("/me/sessions/{id}", { params: { path: { id } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: mySessionsQuery.queryKey }),
  });
}
