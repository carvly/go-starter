-- 全机一份（不随项目渲染）：放到 /etc/openresty/lua/gateway_auth.lua。开了网关鉴权的项目（render.sh --internal-port）在
-- location /v1/ 里用 access_by_lua_file 引用它（不在全机共享的 http.conf 里加 lua_package_path：同机别的配置已经写过时
-- 会重复定义）。约定见 docs/agents/auth.md 的"网关鉴权"。
--
-- 清掉客户端带来的身份头 → 没带令牌直接放行（匿名）→ 读完请求体 → 子请求 /_auth（user 服务的校验端点）换身份断言 →
-- 把断言或失效原因转给后端。校验端点限流（429）时原样回给客户端；其余失败返回 503 auth_unavailable（不返回 401：
-- 客户端收到 401 会清令牌）。

local function finish(status, content_type, body, retry_after)
    ngx.status = status
    ngx.header["Content-Type"] = content_type
    ngx.header["X-Trace-Id"] = ngx.var.trace_id
    if retry_after then
        ngx.header["Retry-After"] = retry_after
    end
    ngx.print(body)
    return ngx.exit(status)
end

ngx.req.clear_header("X-Auth-Assertion")
ngx.req.clear_header("X-Auth-Status")
local authz = ngx.var.http_authorization
if authz == nil or authz == "" then
    ngx.var.auth_result = "anonymous"
    return
end
-- 断言 30 秒有效：先读完请求体再换，慢上传的时间不能算进去（nginx 默认收完请求体才连上游）。
ngx.req.read_body()
-- 子请求默认不共享父请求的变量，trace_id 显式传过去：校验和业务在同一个 trace 下。
local res = ngx.location.capture("/_auth", { vars = { auth_trace_id = ngx.var.trace_id } })
if res.status == ngx.HTTP_TOO_MANY_REQUESTS then
    ngx.var.auth_result = "rate_limited"
    return finish(res.status, res.header["Content-Type"] or "application/json", res.body or "", res.header["Retry-After"])
end
if res.status ~= ngx.HTTP_OK then
    ngx.var.auth_result = "unavailable"
    ngx.log(ngx.ERR, "gateway auth: verify returned ", res.status)
    return finish(ngx.HTTP_SERVICE_UNAVAILABLE, "application/json",
        '{"code":"auth_unavailable","message":"登录状态校验暂时不可用，请稍后重试"}\n')
end
local assertion = res.header["X-Auth-Assertion"]
if assertion then
    ngx.var.auth_result = "ok"
    ngx.req.set_header("X-Auth-Assertion", assertion)
    return
end
local status = res.header["X-Auth-Status"] or "invalid_token"
ngx.var.auth_result = status
ngx.req.set_header("X-Auth-Status", status)
