// Package page 是后台列表的页码分页。后台表格要页码和总数，前台列表仍用 cursor 分页
// （internal/platform/postgres/pagination.go）。总数和可翻到的范围都封顶在 MaxTotal：
// 大表上 count(*) 和大 OFFSET 都要扫过前面的全部行。
package page

import (
	"slices"
	"strings"

	"app.local/internal/kit/errs"
)

const (
	DefaultSize = 20
	MaxSize     = 100
	// MaxTotal 是总数最多数到的值，也是能翻到的最远位置。超过时页面显示"10000+"，
	// 要找更后面的数据就加筛选条件。
	MaxTotal = 10000
)

// ErrOutOfRange 表示请求的页超出了 MaxTotal 条之后。
var ErrOutOfRange = errs.New(errs.KindInvalid, "page_out_of_range", "page is beyond the first 10000 rows; narrow the filter")

// Request 是规范化后的分页参数，只能经 New 构造。
type Request struct {
	number, size int
}

// New 规范化页码（从 1 开始，小于 1 按 1）和每页条数（不在 [1, MaxSize] 内时回落到
// DefaultSize，与 cursor 分页的 limit 规则一致）。起始位置超过 MaxTotal 时返回 ErrOutOfRange。
func New(number, size int) (Request, error) {
	if number < 1 {
		number = 1
	}
	if size < 1 || size > MaxSize {
		size = DefaultSize
	}
	// 先比较页码再相乘：页码来自客户端，极大的值相乘会溢出成负数。
	if number > MaxTotal || (number-1)*size >= MaxTotal {
		return Request{}, ErrOutOfRange
	}
	return Request{number: number, size: size}, nil
}

func (r Request) Number() int { return r.number }
func (r Request) Size() int   { return r.size }
func (r Request) Offset() int { return (r.number - 1) * r.size }

// Result 是一页数据和封顶后的总数：TotalCapped 为 true 时，实际条数多于 Total（= MaxTotal）。
type Result[T any] struct {
	Items       []T
	Total       int
	TotalCapped bool
}

// Sort 是列表的排序：Field 是允许排序的字段名（同列名），Desc 为 true 时倒序。列表总是再按
// id 排一次，排序字段有重复值时翻页也不会重复或漏行。
type Sort struct {
	Field string
	Desc  bool
}

// ErrInvalidSort 表示 sort 参数不是允许的取值。
var ErrInvalidSort = errs.New(errs.KindInvalid, "invalid_sort", "sort is not one of the allowed values")

// ParseSort 解析 sort 参数："name" 正序，"-name" 倒序；为空时返回 def。字段必须在 allowed
// 里：排序字段会拼进 SQL，只能来自白名单。
func ParseSort(s string, def Sort, allowed ...string) (Sort, error) {
	if s == "" {
		return def, nil
	}
	field, desc := strings.CutPrefix(s, "-")
	if !slices.Contains(allowed, field) {
		return Sort{}, ErrInvalidSort
	}
	return Sort{Field: field, Desc: desc}, nil
}
