package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

var (
	globalMeter   metric.Meter
	meterProvider *sdkmetric.MeterProvider
	// Scraped series carry names and labels only known at runtime, so they cannot
	// use the pre-registered instruments. They reuse this exporter and resource
	// instead, which already hold the endpoint, auth and TLS settings.
	scrapeExporter sdkmetric.Exporter
	scrapeResource *resource.Resource
	// Metric state for callbacks
	metricState      *MetricState
	metricStateMutex sync.RWMutex
)

// MetricState holds the current metric values for ObservableGauge callbacks
type MetricState struct {
	// CPU
	CPUUtilization float64
	Load1m         float64
	Load5m         float64
	Load15m        float64

	// Memory
	MemoryUtilization float64
	MemoryTotal       int64
	MemoryUsed        int64

	// Disk
	DiskUtilization float64
	DiskTotal       int64
	DiskFree        int64

	// Network
	NetworkIORateIn  float64
	NetworkIORateOut float64
	NetworkIOIn      int64
	NetworkIOOut     int64

	// Attributes
	Hostname string
	Service  string
}

// initializeOTELMetrics initializes OpenTelemetry metrics exporter
func initializeOTELMetrics(config *AgentConfig) error {
	// The same name registerAgent uses. The machine hostname would not do: the
	// install script can set another one, and the host record is keyed on this,
	// so metrics stamped with a different name correlate to nothing.
	hostname := config.Host.Name
	if hostname == "" {
		hostname, _ = os.Hostname()
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(config.Host.Service),
			semconv.HostNameKey.String(hostname),
		),
	)
	if err != nil {
		return fmt.Errorf("%w: %w", errOTelResource, err)
	}

	// WithEndpoint expects "host:port" only - no scheme, no path (SDK adds http:// and /v1/metrics)
	ctx := context.Background()
	apiURL := config.API.URL
	if !strings.HasPrefix(apiURL, "http://") && !strings.HasPrefix(apiURL, "https://") {
		apiURL = "http://" + apiURL
	}
	parsed, err := url.Parse(apiURL)
	if err != nil {
		return fmt.Errorf("%w: %w", errAPIURLInvalid, err)
	}
	endpoint := parsed.Host // "api.middlemonitor.io"

	headers := make(map[string]string)
	if config.API.APIKey != "" {
		headers["Authorization"] = fmt.Sprintf("Bearer %s", config.API.APIKey)
	}

	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(endpoint),
		otlpmetrichttp.WithHeaders(headers),
		otlpmetrichttp.WithTimeout(30 * time.Second),
		// A scrape compresses about tenfold; uncompressed, a busy exporter crossed
		// the receiver's message size limit and the whole scrape was dropped.
		otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression),
		// Explicit rather than the library default: the receiver answers 503 when
		// its broker is down. A target waits out the retry of each of its batches
		// rather than losing them; other targets run on their own goroutines.
		otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{
			Enabled:         true,
			InitialInterval: 2 * time.Second,
			MaxInterval:     15 * time.Second,
			MaxElapsedTime:  45 * time.Second,
		}),
	}

	if parsed.Scheme == "http" {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}

	exporter, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return fmt.Errorf("%w: %w", errMetricExporter, err)
	}

	// Create metric reader with interval matching agent collection interval
	collectionInterval := time.Duration(config.Interval) * time.Second
	if collectionInterval < 10*time.Second {
		collectionInterval = 10 * time.Second // Minimum 10 seconds
	}

	scrapeExporter = exporter
	scrapeResource = res

	reader := sdkmetric.NewPeriodicReader(exporter,
		sdkmetric.WithInterval(collectionInterval),
	)

	meterProvider = sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(reader),
	)

	otel.SetMeterProvider(meterProvider)
	globalMeter = otel.Meter("middle-monitor-agent")

	metricState = &MetricState{
		Hostname: hostname,
		Service:  config.Host.Service,
	}

	if err := initObservableInstruments(); err != nil {
		return fmt.Errorf("%w: %w", errInstruments, err)
	}

	slog.Info("otel metrics initialized", "service", config.Host.Service, "hostname", hostname, "endpoint", endpoint+"/v1/metrics")

	return nil
}

// hostGauge is one value of metricState published as an observable gauge.
type hostGauge struct {
	name, description string
	float             func(*MetricState) float64
	int               func(*MetricState) int64
	attrs             []attribute.KeyValue
}

func hostGauges() []hostGauge {
	disk := []attribute.KeyValue{attribute.String("disk.device", "/")}
	in := []attribute.KeyValue{attribute.String("network.direction", "receive")}
	out := []attribute.KeyValue{attribute.String("network.direction", "transmit")}
	return []hostGauge{
		{name: "system.cpu.utilization", description: "CPU utilization (0-1)", float: func(m *MetricState) float64 { return m.CPUUtilization }},
		{name: "system.cpu.load_average.1m", description: "CPU load average 1 minute", float: func(m *MetricState) float64 { return m.Load1m }},
		{name: "system.cpu.load_average.5m", description: "CPU load average 5 minutes", float: func(m *MetricState) float64 { return m.Load5m }},
		{name: "system.cpu.load_average.15m", description: "CPU load average 15 minutes", float: func(m *MetricState) float64 { return m.Load15m }},
		{name: "system.memory.utilization", description: "Memory utilization (0-1)", float: func(m *MetricState) float64 { return m.MemoryUtilization }},
		{name: "system.memory.total", description: "Total memory in bytes", int: func(m *MetricState) int64 { return m.MemoryTotal }},
		{name: "system.memory.used", description: "Used memory in bytes", int: func(m *MetricState) int64 { return m.MemoryUsed }},
		{name: "system.disk.utilization", description: "Disk utilization (0-1)", float: func(m *MetricState) float64 { return m.DiskUtilization }, attrs: disk},
		{name: "system.disk.total", description: "Total disk space in bytes", int: func(m *MetricState) int64 { return m.DiskTotal }, attrs: disk},
		{name: "system.disk.free", description: "Free disk space in bytes", int: func(m *MetricState) int64 { return m.DiskFree }, attrs: disk},
		{name: "system.network.io_rate", description: "Network I/O rate in bytes per second", float: func(m *MetricState) float64 { return m.NetworkIORateIn }, attrs: in},
		{name: "system.network.io_rate", description: "Network I/O rate in bytes per second", float: func(m *MetricState) float64 { return m.NetworkIORateOut }, attrs: out},
	}
}

// initObservableInstruments registers the host gauges and the callback that
// reads them from metricState at each collection.
func initObservableInstruments() error {
	gauges := hostGauges()
	floats := map[string]metric.Float64ObservableGauge{}
	ints := map[string]metric.Int64ObservableGauge{}
	var instruments []metric.Observable
	for _, g := range gauges {
		var err error
		switch {
		case g.float != nil && floats[g.name] == nil:
			floats[g.name], err = globalMeter.Float64ObservableGauge(g.name, metric.WithDescription(g.description))
			instruments = append(instruments, floats[g.name])
		case g.int != nil && ints[g.name] == nil:
			ints[g.name], err = globalMeter.Int64ObservableGauge(g.name, metric.WithDescription(g.description))
			instruments = append(instruments, ints[g.name])
		}
		if err != nil {
			return fmt.Errorf("%s: %w: %w", g.name, errGaugeCreate, err)
		}
	}

	identity := []attribute.KeyValue{
		attribute.String("host.name", metricState.Hostname),
		attribute.String("service.name", metricState.Service),
	}
	_, err := globalMeter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		metricStateMutex.RLock()
		defer metricStateMutex.RUnlock()
		for _, g := range gauges {
			attrs := metric.WithAttributes(append(append([]attribute.KeyValue{}, identity...), g.attrs...)...)
			if g.float != nil {
				o.ObserveFloat64(floats[g.name], g.float(metricState), attrs)
			} else {
				o.ObserveInt64(ints[g.name], g.int(metricState), attrs)
			}
		}
		return nil
	}, instruments...)
	if err != nil {
		return fmt.Errorf("%w: %w", errCallbackRegister, err)
	}
	return nil
}

// exportMetricsOTLP updates metric state and triggers export via callbacks
func exportMetricsOTLP(metrics *AgentMetrics) error {
	if globalMeter == nil || metricState == nil {
		return errMeterNotInitialized
	}

	updateMetricState(metrics)

	// Flush now instead of waiting for the periodic reader.
	// The state lock must NOT be held here: ForceFlush runs the observation
	// callback, which read-locks metricStateMutex, and sync.RWMutex is not
	// reentrant - holding the write lock across the flush deadlocks the callback.
	if meterProvider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		// An unreachable backend must not hang the collection loop.
		errCh := make(chan error, 1)
		go func() {
			errCh <- meterProvider.ForceFlush(ctx)
		}()

		select {
		case err := <-errCh:
			if err != nil {
				slog.Warn("flush metrics failed", "err", err)
				return fmt.Errorf("%w: %w", errFlush, err)
			}
		case <-ctx.Done():
			slog.Warn("flush metrics timed out")
			return errFlushTimeout
		}
	}

	return nil
}

// updateMetricState stores the latest collected values for the ObservableGauge
// callbacks to observe on the next collection.
func updateMetricState(metrics *AgentMetrics) {
	metricStateMutex.Lock()
	defer metricStateMutex.Unlock()

	hostname := metrics.Hostname
	metricState.Hostname = hostname
	metricState.Service = metrics.Service

	if cpuValue, ok := metrics.Metrics["cpu"]; ok {
		metricState.CPUUtilization = cpuValue / 100.0 // Convert 0-100% to 0-1
		if val, ok := metrics.Metadata["load_1min"]; ok {
			metricState.Load1m = val
		}
		if val, ok := metrics.Metadata["load_5min"]; ok {
			metricState.Load5m = val
		}
		if val, ok := metrics.Metadata["load_15min"]; ok {
			metricState.Load15m = val
		}
	}

	if ramValue, ok := metrics.Metrics["ram"]; ok {
		metricState.MemoryUtilization = ramValue / 100.0 // Convert 0-100% to 0-1

		if val, ok := metrics.Metadata["ram_total_gb"]; ok {
			metricState.MemoryTotal = int64(val * 1024 * 1024 * 1024) // GB to bytes
			// Calculate used = total * utilization
			usedGB := val * (ramValue / 100.0)
			metricState.MemoryUsed = int64(usedGB * 1024 * 1024 * 1024)
		}
	}

	if diskValue, ok := metrics.Metrics["disk"]; ok {
		metricState.DiskUtilization = diskValue / 100.0 // Convert 0-100% to 0-1

		if val, ok := metrics.Metadata["disk_total_gb"]; ok {
			metricState.DiskTotal = int64(val * 1024 * 1024 * 1024) // GB to bytes
		}
		if val, ok := metrics.Metadata["disk_free_gb"]; ok {
			metricState.DiskFree = int64(val * 1024 * 1024 * 1024) // GB to bytes
		}
	}

	if _, ok := metrics.Metrics["network"]; ok {
		// Network I/O rate (MB/s to bytes/s)
		if val, ok := metrics.Metadata["network_speed_in_mb_per_s"]; ok && val > 0 {
			metricState.NetworkIORateIn = val * 1024 * 1024 // MB/s to bytes/s
		}
		if val, ok := metrics.Metadata["network_speed_out_mb_per_s"]; ok && val > 0 {
			metricState.NetworkIORateOut = val * 1024 * 1024 // MB/s to bytes/s
		}
	}
}
