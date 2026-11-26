package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"app.local/internal/kit/errs"
)

// 原生登录 App 入口的请求不完整。它们检查的是 HTTP 请求本身（请求头、请求体字段），所以定义在接入层、文案写明要改
// 哪里；返回时还没有使用授权码，客户端修正后可以直接重试。
var (
	errClientPlatformInvalid = errs.New(errs.KindInvalid, "client_platform_invalid",
		"App 登录必须带请求头 X-Client-Platform，取值 android、ios 或 harmonyos")
	errDeviceClassInvalid = errs.New(errs.KindInvalid, "device_class_invalid",
		"App 登录的请求体必须带 device_class，取值 phone 或 tablet")
)

func errUnknownProvider(provider string) error {
	return echo.NewHTTPError(http.StatusBadRequest, "unknown provider: "+provider)
}
