package admin

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestDist(t *testing.T) {
	page, err := fs.ReadFile(Dist(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("dist/index.html"); err == nil {
		if strings.Contains(string(page), "pnpm build") {
			t.Error("构建过前端时应托管 dist/index.html")
		}
	} else if !strings.Contains(string(page), "pnpm build") {
		t.Errorf("没有构建前端时 index.html 应提示怎么构建，得到 %q", page)
	}
}
