package app

import (
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"

	"app.local/internal/platform/tracing"
)

type countingMeterProvider struct {
	metricnoop.MeterProvider
	calls int
}

func (p *countingMeterProvider) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	p.calls++
	return p.MeterProvider.Meter(name, opts...)
}

// 指标走 Prometheus：otelpgx 不能退回 otel 全局 MeterProvider，否则设置全局 provider 后会多出一套重复的 SQL 指标。
func TestNewPgxTracer_DoesNotUseGlobalMeterProvider(t *testing.T) {
	global := &countingMeterProvider{}
	otel.SetMeterProvider(global)
	t.Cleanup(func() { otel.SetMeterProvider(metricnoop.NewMeterProvider()) })
	// SetMeterProvider 会把之前从全局取过的 Meter 转交给新 provider，只统计构造期间的调用。
	global.calls = 0

	_ = newPgxTracer(tracing.Disabled())
	if global.calls != 0 {
		t.Errorf("otelpgx 从全局 MeterProvider 取了 %d 次 Meter", global.calls)
	}
}
