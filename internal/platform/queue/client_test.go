package queue

import (
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"

	"app.local/internal/platform/tracing"
)

// countingMeterProvider 记录有没有人从 otel 全局 provider 取 Meter。
type countingMeterProvider struct {
	metricnoop.MeterProvider
	calls int
}

func (p *countingMeterProvider) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	p.calls++
	return p.MeterProvider.Meter(name, opts...)
}

// 指标走 Prometheus：otelriver 不能退回 otel 全局 MeterProvider，否则设置全局 provider 后会多出一套重复的队列指标。
func TestNewConfig_DoesNotUseGlobalMeterProvider(t *testing.T) {
	global := &countingMeterProvider{}
	otel.SetMeterProvider(global)
	t.Cleanup(func() { otel.SetMeterProvider(metricnoop.NewMeterProvider()) })
	// SetMeterProvider 会把之前从全局取过的 Meter 转交给新 provider，只统计构造期间的调用。
	global.calls = 0

	_ = newConfig(nil, nil, nil, tracing.Disabled())
	if global.calls != 0 {
		t.Errorf("otelriver 从全局 MeterProvider 取了 %d 次 Meter", global.calls)
	}
}
