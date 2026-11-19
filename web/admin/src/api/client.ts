import createClient from "openapi-fetch";

import type { paths } from "./schema";

// 会话在 HttpOnly cookie 里，同源请求自动携带；前端不接触任何凭据。
// 数组查询参数用逗号分隔（?status=pending,failed），和 admin.yaml 里的 explode: false 一致；
// openapi-fetch 默认是 ?status=pending&status=failed。
export const api = createClient<paths>({
  baseUrl: "/admin/v1",
  querySerializer: { array: { style: "form", explode: false } },
});
