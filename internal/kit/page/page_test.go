package page_test

import (
	"errors"
	"math"
	"testing"

	"app.local/internal/kit/page"
)

func TestNew(t *testing.T) {
	tests := []struct {
		number, size         int
		wantNumber, wantSize int
		wantOffset           int
	}{
		{1, 20, 1, 20, 0},
		{3, 50, 3, 50, 100},
		{0, 0, 1, page.DefaultSize, 0},
		{-1, 101, 1, page.DefaultSize, 0},
		{2, 100, 2, 100, 100},
	}
	for _, tt := range tests {
		r, err := page.New(tt.number, tt.size)
		if err != nil || r.Number() != tt.wantNumber || r.Size() != tt.wantSize || r.Offset() != tt.wantOffset {
			t.Errorf("New(%d, %d) = %d/%d/%d, %v", tt.number, tt.size, r.Number(), r.Size(), r.Offset(), err)
		}
	}
}

func TestNew_BeyondMaxTotal(t *testing.T) {
	if _, err := page.New(100, 100); err != nil {
		t.Errorf("第 100 页（第 9901–10000 条）应可访问: %v", err)
	}
	if _, err := page.New(101, 100); !errors.Is(err, page.ErrOutOfRange) {
		t.Errorf("第 101 页 err = %v, want ErrOutOfRange", err)
	}
	if _, err := page.New(math.MaxInt, 20); !errors.Is(err, page.ErrOutOfRange) {
		t.Errorf("极大页码 err = %v, want ErrOutOfRange（不能溢出）", err)
	}
	if _, err := page.New(501, 20); !errors.Is(err, page.ErrOutOfRange) {
		t.Errorf("size=20 第 501 页 err = %v, want ErrOutOfRange", err)
	}
}

func TestParseSort(t *testing.T) {
	def := page.Sort{Field: "created_at", Desc: true}
	for in, want := range map[string]page.Sort{
		"":            def,
		"name":        {Field: "name"},
		"-sort_order": {Field: "sort_order", Desc: true},
	} {
		if got, err := page.ParseSort(in, def, "name", "sort_order"); err != nil || got != want {
			t.Errorf("ParseSort(%q) = %+v, %v, want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"price", "--name", "name; DROP TABLE x"} {
		if _, err := page.ParseSort(in, def, "name", "sort_order"); !errors.Is(err, page.ErrInvalidSort) {
			t.Errorf("ParseSort(%q) error = %v, want ErrInvalidSort", in, err)
		}
	}
}
