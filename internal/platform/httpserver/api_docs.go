package httpserver

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// redocBundleURL 锁定 redoc 的具体版本，升级要显式改这里，
// 不跟 CDN 上的浮动 tag——与项目"版本固定"的惯例一致。
const redocBundleURL = "https://cdn.redoc.ly/redoc/v3.2.0/bundles/redoc.standalone.js"

// redocCSP 只用于 /docs。redoc v3 是 ESM 外联脚本，script-src 只需放行 CDN 域，
// 不需要 'unsafe-inline'；运行时注入样式，style-src 仍要 'unsafe-inline'；
// connect-src 'self' 只够它 fetch 同源的 spec（遥测已通过 disable-telemetry
// 关闭，没有外联请求）。
const redocCSP = "default-src 'self'; script-src https://cdn.redoc.ly; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'"

// redocPage 是 Redoc v3 的挂载页。disable-telemetry="true" 必须保留：v3 默认向
// Redocly 上报匿名使用数据（OpenTelemetry traces）。注意必须显式写 ="true"——
// redoc 用 String(v) === "true" 判断布尔选项，裸写属性（空字符串值）不生效。
const redocPageTemplate = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>API 文档</title>
</head>
<body>
  <redoc spec-url="{{SPEC_URL}}" disable-telemetry="true"></redoc>
  <script type="module" src="` + redocBundleURL + `"></script>
</body>
</html>
`

// registerAPIDocs 挂载 GET <specPath>（spec 原文）和 /docs（Redoc）；enabled 为 false 时
// 整组不注册。前台 server 挂 /openapi.yaml，后台 server 挂 /admin.yaml，各自只暴露自己的
// spec。文档 UI 只有 Redoc 一个：曾经并存的 Swagger UI（echo-swagger）已移除——
// swaggo/files 上游最新版停在 v2.0.2（2024-12，捆绑 swagger-ui 5.18.2），而 swagger-ui
// 到 v5.32.0（2026-02）才官方支持 OpenAPI 3.2。
func registerAPIDocs(e *echo.Echo, enabled bool, specPath string, spec []byte) error {
	if !enabled {
		return nil
	}

	redocCSPMw, err := (middleware.SecureConfig{ContentSecurityPolicy: redocCSP}).ToMiddleware()
	if err != nil {
		return err
	}

	e.GET(specPath, func(c *echo.Context) error {
		return c.Blob(http.StatusOK, "application/yaml", spec)
	})

	page := strings.Replace(redocPageTemplate, "{{SPEC_URL}}", specPath, 1)
	e.GET("/docs", func(c *echo.Context) error {
		return c.HTML(http.StatusOK, page)
	}, redocCSPMw)
	return nil
}
