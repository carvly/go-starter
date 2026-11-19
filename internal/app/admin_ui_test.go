package app

import "testing"

func TestAdminUIWired(t *testing.T) {
	if adminStatic == nil {
		t.Fatal("admin_ui.go 应把后台前端交给后台 server（adminStatic 为 nil）")
	}
}
