import { keepPreviousData, queryOptions } from "@tanstack/react-query";

import { api } from "@/api/client";
import { call } from "@/api/errors";
import type { components } from "@/api/schema";

export type AuditLog = components["schemas"]["AuditLog"];

export type AuditQuery = { staff_id?: string; from?: string; to?: string; cursor?: string };

export const auditLogsQuery = (q: AuditQuery) =>
  queryOptions({
    queryKey: ["audit-logs", q],
    queryFn: () => call(api.GET("/audit-logs", { params: { query: { ...q, limit: 50 } } })),
    placeholderData: keepPreviousData,
  });
