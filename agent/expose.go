package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultExposeListen    = "127.0.0.1:9099"
	defaultExposePath      = "/metrics"
	defaultExposeStaleness = 5 * time.Minute
	// A runaway exporter must not grow the agent's memory without bound.
	maxExposedSeries = 50000
)

// ExposeConfig serves what the agent holds back in the Prometheus text format,
// so an existing Prometheus can keep scraping during a migration.
type ExposeConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"` // default 127.0.0.1:9099
	Path    string `yaml:"path"`   // default /metrics
	// Staleness drops series not refreshed for this long, in seconds. Serving a
	// value the agent stopped receiving would report a dead target as healthy.
	Staleness int `yaml:"staleness"`
}

func (c ExposeConfig) listen() string { return orDefault(c.Listen, defaultExposeListen) }
func (c ExposeConfig) path() string   { return orDefault(c.Path, defaultExposePath) }

func (c ExposeConfig) staleness() time.Duration {
	if c.Staleness > 0 {
		return time.Duration(c.Staleness) * time.Second
	}
	return defaultExposeStaleness
}

// exposedSample is the last value seen for one series.
type exposedSample struct {
	Name   string
	Labels map[string]string
	Value  float64
	Seen   time.Time
}

// seriesCache holds the most recent value of every scraped series.
type seriesCache struct {
	mu      sync.RWMutex
	samples map[string]exposedSample
	full    bool
}

var exposedSeries = &seriesCache{samples: map[string]exposedSample{}}

// seriesKey identifies a series by name and label set, order-independent.
func seriesKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}
	pairs := make([]string, 0, len(labels))
	for key, value := range labels {
		pairs = append(pairs, key+"="+value)
	}
	sort.Strings(pairs)
	return name + "{" + strings.Join(pairs, ",") + "}"
}

// Record stores the latest value of a series.
func (c *seriesCache) Record(name string, labels map[string]string, value float64, seen time.Time) {
	key := seriesKey(name, labels)

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, known := c.samples[key]; !known && len(c.samples) >= maxExposedSeries {
		if !c.full {
			c.full = true
			slog.Warn("expose series cap reached, new series are not exposed", "cap", maxExposedSeries)
		}
		return
	}
	c.samples[key] = exposedSample{Name: name, Labels: labels, Value: value, Seen: seen}
}

// Fresh returns the series still within the staleness window, sorted so the
// output is stable between scrapes.
func (c *seriesCache) Fresh(now time.Time, staleness time.Duration) []exposedSample {
	c.mu.RLock()
	defer c.mu.RUnlock()

	fresh := make([]exposedSample, 0, len(c.samples))
	for _, sample := range c.samples {
		if now.Sub(sample.Seen) <= staleness {
			fresh = append(fresh, sample)
		}
	}
	sort.Slice(fresh, func(i, j int) bool {
		if fresh[i].Name != fresh[j].Name {
			return fresh[i].Name < fresh[j].Name
		}
		return seriesKey(fresh[i].Name, fresh[i].Labels) < seriesKey(fresh[j].Name, fresh[j].Labels)
	})
	return fresh
}

// EvictStale drops samples the staleness window has already excluded from
// Fresh and un-latches the cap. Fresh only filters at read time, so without
// this a churned-out series occupies its slot forever: on a scheduler,
// rescheduling changes nomad_alloc on every redeploy, so a long-running agent
// eventually hits maxExposedSeries and starts silently dropping series that
// are still live.
func (c *seriesCache) EvictStale(now time.Time, staleness time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, sample := range c.samples {
		if now.Sub(sample.Seen) > staleness {
			delete(c.samples, key)
		}
	}
	c.full = false
}

// escapeLabelValue applies the escapes the exposition format defines.
func escapeLabelValue(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return replacer.Replace(value)
}

func renderSample(b *strings.Builder, sample exposedSample) {
	b.WriteString(sample.Name)
	if len(sample.Labels) > 0 {
		keys := make([]string, 0, len(sample.Labels))
		for key := range sample.Labels {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		b.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(key)
			b.WriteString(`="`)
			b.WriteString(escapeLabelValue(sample.Labels[key]))
			b.WriteByte('"')
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(strconv.FormatFloat(sample.Value, 'g', -1, 64))
	// The original scrape time, not now: re-stamping a value the agent read 50
	// seconds ago would present stale data as current.
	b.WriteByte(' ')
	b.WriteString(strconv.FormatInt(sample.Seen.UnixMilli(), 10))
	b.WriteByte('\n')
}

// RenderExposition writes the fresh series in the Prometheus text format.
func RenderExposition(now time.Time, staleness time.Duration, host []exposedSample) string {
	var b strings.Builder
	for _, sample := range host {
		renderSample(&b, sample)
	}
	for _, sample := range exposedSeries.Fresh(now, staleness) {
		renderSample(&b, sample)
	}
	return b.String()
}

// hostSamples renders the agent's own host metrics, so a Prometheus scraping the
// agent sees everything it holds and not only what it pulled from elsewhere.
func hostSamples(now time.Time) []exposedSample {
	metricStateMutex.RLock()
	state := metricState
	metricStateMutex.RUnlock()
	if state == nil {
		return nil
	}

	labels := map[string]string{"host": state.Hostname, "service": state.Service}
	values := map[string]float64{
		"middle_monitor_cpu_utilization":    state.CPUUtilization,
		"middle_monitor_memory_utilization": state.MemoryUtilization,
		"middle_monitor_disk_utilization":   state.DiskUtilization,
		"middle_monitor_load1":              state.Load1m,
		"middle_monitor_load5":              state.Load5m,
		"middle_monitor_load15":             state.Load15m,
		"middle_monitor_network_in_bytes":   state.NetworkIORateIn,
		"middle_monitor_network_out_bytes":  state.NetworkIORateOut,
	}

	samples := make([]exposedSample, 0, len(values))
	for name, value := range values {
		samples = append(samples, exposedSample{Name: name, Labels: labels, Value: value, Seen: now})
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].Name < samples[j].Name })
	return samples
}

// StartCacheEviction periodically sweeps the exposition cache. exportScrapedSamples
// records into it whenever scraping is enabled, regardless of whether
// exposition itself is — so this runs independently of StartExposition too.
func StartCacheEviction(ctx context.Context, cfg ExposeConfig) {
	staleness := cfg.staleness()
	interval := staleness
	if interval > time.Minute {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				exposedSeries.EvictStale(time.Now(), staleness)
			}
		}
	}()
}

// StartExposition serves the agent's metrics for an existing Prometheus to
// scrape. It binds to localhost by default: turning the feature on must not
// publish the endpoint to the network by accident.
func StartExposition(ctx context.Context, cfg ExposeConfig) *http.Server {
	if !cfg.Enabled {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc(cfg.path(), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		now := time.Now()
		fmt.Fprint(w, RenderExposition(now, cfg.staleness(), hostSamples(now)))
	})

	server := &http.Server{
		Addr:              cfg.listen(),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("expose serving", "path", cfg.path(), "listen", cfg.listen())
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Warn("expose server stopped", "err", err)
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	return server
}
