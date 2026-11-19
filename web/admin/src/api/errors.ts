// 后端的错误体是 { code, message, retry_after? }（internal/platform/httpserver/errors.go 的 ErrorBody）。
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  // retryAfter 是多少秒之后可以再试，只有需要等待的错误（429）才有。
  readonly retryAfter: number | undefined;

  constructor(status: number, code: string, message: string, retryAfter?: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.retryAfter = retryAfter;
  }
}

type FetchResult<T> = { data?: T; error?: unknown; response: Response };

// call 把 openapi-fetch 的 { data, error } 转成"成功返回数据、失败抛 ApiError"，
// 交给 TanStack Query 处理。204 等没有响应体的成功返回 undefined。
export async function call<T>(request: Promise<FetchResult<T>>): Promise<T> {
  const { data, error, response } = await request;
  if (response.ok) return data as T;
  const body = (error ?? {}) as { code?: unknown; message?: unknown; retry_after?: unknown };
  throw new ApiError(
    response.status,
    typeof body.code === "string" ? body.code : "http_error",
    // 非 JSON 的错误体（反向代理的 HTML 页面）没有 message；HTTP/2 下 statusText 也是空的。
    typeof body.message === "string" && body.message !== "" ? body.message : `请求失败（${response.status}）`,
    typeof body.retry_after === "number" ? body.retry_after : undefined,
  );
}
