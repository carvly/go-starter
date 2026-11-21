package process_test

import (
	"testing"

	"app.local/internal/platform/process"
)

// 日志字段和指标标签是同一组名字、同一组值。
func TestIdentity_LogAttrsAndLabelsAgree(t *testing.T) {
	id := process.New("api", "host-a")
	if id.Version == "" {
		t.Error("Version 不应为空")
	}
	labels := id.Labels()
	attrs := id.LogAttrs()
	if len(attrs) != len(labels) {
		t.Fatalf("attrs = %v, labels = %v", attrs, labels)
	}
	for _, a := range attrs {
		if labels[a.Key] != a.Value.String() {
			t.Errorf("%s：日志 %q，指标 %q", a.Key, a.Value.String(), labels[a.Key])
		}
	}
	if labels["service_name"] != "api" || labels["service_instance_id"] != "host-a" {
		t.Errorf("labels = %v", labels)
	}
}
