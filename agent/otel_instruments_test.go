package main

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// The gauge names, units and attributes become the series users chart and alert
// on: renaming one silently empties their dashboards.
func TestHostGaugesKeepTheirNamesAndAttributes(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	prevMeter, prevState := globalMeter, metricState
	t.Cleanup(func() { globalMeter, metricState = prevMeter, prevState })
	globalMeter = provider.Meter("test")
	metricState = &MetricState{
		Hostname: "web-01", Service: "web",
		CPUUtilization: 0.5, Load1m: 1, Load5m: 2, Load15m: 3,
		MemoryUtilization: 0.25, MemoryTotal: 8, MemoryUsed: 2,
		DiskUtilization: 0.75, DiskTotal: 100, DiskFree: 25,
		NetworkIORateIn: 10, NetworkIORateOut: 20,
	}

	if err := initObservableInstruments(); err != nil {
		t.Fatalf("init: %v", err)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}

	got := map[string]float64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Gauge[float64]:
				for _, dp := range data.DataPoints {
					got[m.Name+pointSuffix(t, dp.Attributes)] = dp.Value
				}
			case metricdata.Gauge[int64]:
				for _, dp := range data.DataPoints {
					got[m.Name+pointSuffix(t, dp.Attributes)] = float64(dp.Value)
				}
			}
		}
	}

	want := map[string]float64{
		"system.cpu.utilization":          0.5,
		"system.cpu.load_average.1m":      1,
		"system.cpu.load_average.5m":      2,
		"system.cpu.load_average.15m":     3,
		"system.memory.utilization":       0.25,
		"system.memory.total":             8,
		"system.memory.used":              2,
		"system.disk.utilization/":        0.75,
		"system.disk.total/":              100,
		"system.disk.free/":               25,
		"system.network.io_rate receive":  10,
		"system.network.io_rate transmit": 20,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d points %v, want %d", len(got), got, len(want))
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %v, want %v (all: %v)", key, got[key], value, got)
		}
	}
}

// pointSuffix checks the identity attributes every point carries and returns
// what tells points of one gauge apart: the disk device or network direction.
func pointSuffix(t *testing.T, set attribute.Set) string {
	t.Helper()
	if v, _ := set.Value("host.name"); v.AsString() != "web-01" {
		t.Errorf("host.name = %q", v.AsString())
	}
	if v, _ := set.Value("service.name"); v.AsString() != "web" {
		t.Errorf("service.name = %q", v.AsString())
	}
	if v, ok := set.Value("disk.device"); ok {
		return v.AsString()
	}
	if v, ok := set.Value("network.direction"); ok {
		return " " + v.AsString()
	}
	return ""
}
