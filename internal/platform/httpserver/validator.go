package httpserver

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"

	"app.local/internal/kit/errs"
)

// requestValidator 校验 oapi-codegen 经 x-oapi-codegen-extra-tags 注入的 validate 标签。
type requestValidator struct {
	v *validator.Validate
}

func newRequestValidator() *requestValidator {
	v := validator.New(validator.WithRequiredStructEnabled())
	// 报错用客户端看到的 JSON 字段名，而不是 Go 字段名。
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			return ""
		}
		return name
	})
	return &requestValidator{v: v}
}

// Validate 失败时返回 KindInvalid 的业务错误，message 只列出 JSON 字段路径和没通过的规则
// （如 "product_id: uuid; quantity: gt=0"），不带 Go 结构体名。
func (rv *requestValidator) Validate(i any) error {
	err := rv.v.Struct(i)
	if err == nil {
		return nil
	}
	var fieldErrs validator.ValidationErrors
	if !errors.As(err, &fieldErrs) {
		// 传进来的不是结构体等调用方的编程错误，按 500 处理。
		return fmt.Errorf("validate request: %w", err)
	}
	parts := make([]string, 0, len(fieldErrs))
	for _, fe := range fieldErrs {
		_, field, _ := strings.Cut(fe.Namespace(), ".")
		rule := fe.Tag()
		if fe.Param() != "" {
			rule += "=" + fe.Param()
		}
		parts = append(parts, field+": "+rule)
	}
	return errs.New(errs.KindInvalid, "invalid_request", "invalid request: "+strings.Join(parts, "; "))
}
