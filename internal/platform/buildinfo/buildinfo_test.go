package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestFromSettings(t *testing.T) {
	cases := []struct {
		name     string
		settings []debug.BuildSetting
		want     string
	}{
		{"没有 VCS 信息", nil, "dev"},
		{"截取前 12 位", []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}}, "0123456789ab"},
		{"有未提交改动", []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"},
			{Key: "vcs.modified", Value: "true"},
		}, "0123456789ab-dirty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fromSettings(tc.settings); got != tc.want {
				t.Errorf("fromSettings = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVersion_InjectedValueWins(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	version = "v1.2.3"
	if got := Version(); got != "v1.2.3" {
		t.Errorf("Version = %q, want v1.2.3", got)
	}
}
