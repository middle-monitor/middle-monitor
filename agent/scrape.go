package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const (
	defaultScrapeInterval = 15 * time.Second
	defaultScrapeTimeout  = 10 * time.Second
	// An exporter answering with megabytes would exhaust the agent's memory.
	maxScrapeBodyBytes = 16 << 20
	// About 560 KB before compression: well inside the receiver's per-message limit.
	maxSamplesPerExport = 2000
)

// ScrapeTarget is one exposition endpoint the agent pulls from.
type ScrapeTarget struct {
	Name     string            `yaml:"name"`
	URL      string            `yaml:"url"`
	Interval int               `yaml:"interval"` // seconds; 0 uses the global default
	Timeout  int               `yaml:"timeout"`  // seconds; 0 uses the global default
	Labels   map[string]string `yaml:"labels"`   // added to every series from this target

	// Query parameters. A scalar is fixed; a list expands into one target per
	// value, crossed with the other lists.
	Params       ParamValues `yaml:"params"`
	ParamsMatrix ParamValues `yaml:"params_matrix"`

	// Series filtering, applied in this order: keep_metrics (a whitelist when
	// non-empty), drop_metrics, keep_if_labels, drop_labels.
	KeepMetrics  []string      `yaml:"keep_metrics"`
	DropMetrics  []string      `yaml:"drop_metrics"`
	KeepIfLabels []LabelFilter `yaml:"keep_if_labels"`
	DropLabels   []string      `yaml:"drop_labels"`

	// Credentials and TLS presented to the endpoint.
	BearerToken     string            `yaml:"bearer_token"`
	BearerTokenFile string            `yaml:"bearer_token_file"`
	BasicAuth       *BasicAuth        `yaml:"basic_auth"`
	Headers         map[string]string `yaml:"headers"`
	TLSConfig       *TLSConfig        `yaml:"tls_config"`
}

// ScrapeConfig is the agent's pull configuration.
type ScrapeConfig struct {
	Enabled  bool           `yaml:"enabled"`
	Interval int            `yaml:"interval"` // seconds, default 15
	Timeout  int            `yaml:"timeout"`  // seconds, default 10
	Targets  []ScrapeTarget `yaml:"targets"`  // static targets

	// Fragments merged into targets, so a config-management role can drop the
	// scrape config of its exporter next to the exporter it installs.
	Include StringOrList `yaml:"include"`
	// Labels applied to every target of this agent unless the target sets them.
	Labels map[string]string `yaml:"labels"`

	// Service discovery. Rediscovered every DiscoveryInterval seconds; targets
	// that disappear have their loop stopped.
	DiscoveryInterval int            `yaml:"discovery_interval"` // seconds, default 30
	Nomad             NomadConfigs   `yaml:"nomad"`
	SRV               []SRVConfig    `yaml:"srv"`
	HTTPSD            []HTTPSDConfig `yaml:"http_sd"`
}

func (c ScrapeConfig) discoveryInterval() time.Duration {
	if c.DiscoveryInterval > 0 {
		return time.Duration(c.DiscoveryInterval) * time.Second
	}
	return defaultDiscoveryInterval
}

func (c ScrapeConfig) usesDiscovery() bool {
	return len(c.Nomad) > 0 || len(c.SRV) > 0 || len(c.HTTPSD) > 0
}

func (c ScrapeConfig) intervalFor(t ScrapeTarget) time.Duration {
	if t.Interval > 0 {
		return time.Duration(t.Interval) * time.Second
	}
	if c.Interval > 0 {
		return time.Duration(c.Interval) * time.Second
	}
	return defaultScrapeInterval
}

func (c ScrapeConfig) timeoutFor(t ScrapeTarget) time.Duration {
	if t.Timeout > 0 {
		return time.Duration(t.Timeout) * time.Second
	}
	if c.Timeout > 0 {
		return time.Duration(c.Timeout) * time.Second
	}
	return defaultScrapeTimeout
}

// runningTarget is a live scrape loop, kept with the target it was started from
// so a reload can tell a changed target from an untouched one.
type runningTarget struct {
	target ScrapeTarget
	cancel context.CancelFunc
}

// scrapeManager keeps one goroutine per live target, starting and stopping them
// as discovery adds and removes instances.
type scrapeManager struct {
	mu              sync.Mutex
	cfg             ScrapeConfig
	running         map[string]*runningTarget // keyed by target url
	discoveryCancel context.CancelFunc
	wg              sync.WaitGroup
}

// StartScraping runs one goroutine per target. A target that fails or answers
// slowly must not hold up the others, so they never share a ticker.
func StartScraping(ctx context.Context, cfg ScrapeConfig) *scrapeManager {
	m := &scrapeManager{running: map[string]*runningTarget{}}
	m.Reload(ctx, cfg)
	return m
}

// Reload brings the running loops in line with a new configuration, which is
// what SIGHUP does: a restart would drop the current scrape cycle and the
// config changes far more often than the binary.
func (m *scrapeManager) Reload(ctx context.Context, cfg ScrapeConfig) {
	m.mu.Lock()
	m.cfg = cfg
	if m.discoveryCancel != nil {
		m.discoveryCancel()
		m.discoveryCancel = nil
	}
	m.mu.Unlock()

	wanted := []ScrapeTarget{}
	if cfg.Enabled {
		wanted = cfg.Targets
	}
	m.reconcile(ctx, wanted)

	if !cfg.Enabled || !cfg.usesDiscovery() {
		slog.Info("scrape reloaded", "enabled", cfg.Enabled, "static_targets", len(wanted))
		return
	}

	discoveryCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.discoveryCancel = cancel
	m.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.discoveryLoop(discoveryCtx)
	}()
	slog.Info("scrape reloaded", "enabled", cfg.Enabled, "static_targets", len(wanted))
}

// Wait blocks until every loop the manager started has returned, so a shutdown
// does not leave a scrape mid-flight.
func (m *scrapeManager) Wait() {
	m.wg.Wait()
}

func (m *scrapeManager) config() ScrapeConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

// reconcile brings the running loops in line with the wanted target set.
func (m *scrapeManager) reconcile(ctx context.Context, wanted []ScrapeTarget) {
	byURL := make(map[string]ScrapeTarget, len(wanted))
	for _, target := range wanted {
		if target.URL == "" {
			slog.Warn("scrape target skipped, no url", "target", target.Name)
			continue
		}
		byURL[target.URL] = target
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	cfg := m.cfg

	for url, running := range m.running {
		wantedTarget, keep := byURL[url]
		if keep && reflect.DeepEqual(running.target, wantedTarget) {
			continue
		}
		// A target whose filters or credentials changed has to be restarted:
		// its loop compiled them once, at start.
		running.cancel()
		delete(m.running, url)
		if !keep {
			slog.Info("scrape target removed", "url", url)
		}
	}

	for url, target := range byURL {
		if _, already := m.running[url]; already {
			continue
		}
		targetCtx, cancel := context.WithCancel(ctx)
		m.running[url] = &runningTarget{target: target, cancel: cancel}
		m.wg.Add(1)
		go func(target ScrapeTarget) {
			defer m.wg.Done()
			runTargetLoop(targetCtx, cfg, target)
		}(target)
		slog.Info("scrape target added", "url", url)
	}
}

// discoveryLoop refreshes the discovered targets, keeping the static ones.
func (m *scrapeManager) discoveryLoop(ctx context.Context) {
	cfg := m.config()
	client := &http.Client{Timeout: cfg.timeoutFor(ScrapeTarget{})}
	ticker := time.NewTicker(cfg.discoveryInterval())
	defer ticker.Stop()

	for {
		if discovered, ok := m.discover(ctx, client); ok {
			// A reload cancels this loop; without the check it could still
			// reconcile the target set of the configuration it replaced.
			if ctx.Err() == nil {
				cfg := m.config()
				discovered = decorateDiscovered(cfg, discovered)
				m.reconcile(ctx, append(append([]ScrapeTarget{}, cfg.Targets...), discovered...))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// decorateDiscovered gives discovered targets the same global labels as the
// static ones: an operator setting cluster= means every series of that agent.
func decorateDiscovered(cfg ScrapeConfig, targets []ScrapeTarget) []ScrapeTarget {
	if len(cfg.Labels) == 0 {
		return targets
	}
	for i := range targets {
		targets[i].Labels = withGlobalLabels(cfg.Labels, targets[i].Labels)
	}
	return targets
}

// discover asks every configured source. It reports failure rather than a
// partial list: acting on one would tear down every target of a source that is
// merely unreachable for a cycle, and a scrape gap looks exactly like an outage.
func (m *scrapeManager) discover(ctx context.Context, client *http.Client) ([]ScrapeTarget, bool) {
	cfg := m.config()
	discovered := []ScrapeTarget{}

	for _, nomad := range cfg.Nomad {
		targets, err := DiscoverNomad(ctx, client, nomad)
		if err != nil {
			slog.Warn("nomad discovery failed, keeping current targets", "err", err)
			return nil, false
		}
		discovered = append(discovered, targets...)
	}

	for _, srv := range cfg.SRV {
		targets, err := DiscoverSRV(srv)
		if err != nil {
			slog.Warn("srv discovery failed, keeping current targets", "record", srv.Name, "err", err)
			return nil, false
		}
		discovered = append(discovered, targets...)
	}

	for _, httpSD := range cfg.HTTPSD {
		targets, err := DiscoverHTTP(ctx, client, httpSD)
		if err != nil {
			slog.Warn("http_sd discovery failed, keeping current targets", "url", httpSD.URL, "err", err)
			return nil, false
		}
		discovered = append(discovered, targets...)
	}
	return discovered, true
}

func runTargetLoop(ctx context.Context, cfg ScrapeConfig, target ScrapeTarget) {
	filter, err := target.compileFilter()
	if err != nil {
		slog.Warn("scrape target disabled", "target", target.Name, "err", err)
		return
	}
	client, err := clientFor(cfg, target)
	if err != nil {
		slog.Warn("scrape target disabled", "target", target.Name, "err", err)
		return
	}

	// The interval is read again every round: the budget can lengthen it.
	key, base := targetKey(target), cfg.intervalFor(target)
	scrapeBudget.register(key, base)
	defer scrapeBudget.unregister(key)
	timer := time.NewTimer(scrapeBudget.interval(key, base))
	defer timer.Stop()

	scrapeOnce(ctx, client, target, filter)
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			scrapeOnce(ctx, client, target, filter)
			timer.Reset(scrapeBudget.interval(key, base))
		}
	}
}

func scrapeOnce(ctx context.Context, client *http.Client, target ScrapeTarget, filter *seriesFilter) {
	samples, err := fetchTarget(ctx, client, target)
	if err != nil {
		slog.Warn("scrape failed", "target", target.Name, "url", target.URL, "err", err)
		return
	}
	scraped := len(samples)
	samples = filter.apply(samples)
	if len(samples) == 0 {
		slog.Warn("scrape returned no samples", "target", target.Name, "scraped", scraped)
		return
	}
	if err := exportScrapedSamples(ctx, target, samples); err != nil {
		slog.Warn("scrape export failed", "target", target.Name, "err", err)
		return
	}
	scrapeBudget.observe(targetKey(target), len(samples))
	// "exported" rather than "samples": the shipping step is the one a silent
	// pipeline makes invisible, and the count is what tells it happened.
	slog.Info("scraped", "target", target.Name, "scraped", scraped, "exported", len(samples))
}

func fetchTarget(ctx context.Context, client *http.Client, target ScrapeTarget) ([]ScrapedSample, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", target.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/plain;version=0.0.4;q=1,*/*;q=0.1")
	if err := applyAuth(req, target); err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &scrapeStatusError{StatusCode: resp.StatusCode}
	}
	return ParseExposition(&limitedReader{r: resp.Body, remaining: maxScrapeBodyBytes})
}

// exportScrapedSamples ships one scrape as OTLP gauges. Counters are sent as
// gauges too: the backend stores raw values and computes rates at query time, so
// re-deriving a delta here would only lose information.
func exportScrapedSamples(ctx context.Context, target ScrapeTarget, samples []ScrapedSample) error {
	if scrapeExporter == nil {
		return errScrapeExporterMissing
	}

	now := time.Now()
	metrics := make([]metricdata.Metrics, 0, len(samples))
	for _, sample := range samples {
		attrs := make([]attribute.KeyValue, 0, len(sample.Labels)+len(target.Labels)+1)
		for key, value := range sample.Labels {
			attrs = append(attrs, attribute.String(key, value))
		}
		// Target labels are applied last so they win over a colliding series
		// label: they are what the operator configured deliberately.
		for key, value := range target.Labels {
			attrs = append(attrs, attribute.String(key, value))
		}
		if target.Name != "" {
			attrs = append(attrs, attribute.String("scrape_target", target.Name))
		}

		// Kept so an existing Prometheus can scrape the agent during a migration.
		exposedSeries.Record(sample.Name, labelsFor(attrs), sample.Value, now)

		metrics = append(metrics, metricdata.Metrics{
			Name: sample.Name,
			Data: metricdata.Gauge[float64]{
				DataPoints: []metricdata.DataPoint[float64]{{
					Attributes: attribute.NewSet(attrs...),
					Time:       now,
					Value:      sample.Value,
				}},
			},
		})
	}

	// Batches bound each request, so one oversized target cannot fail as a whole
	// and a retry resends a part rather than everything.
	var errs []error
	for start := 0; start < len(metrics); start += maxSamplesPerExport {
		end := min(start+maxSamplesPerExport, len(metrics))
		if err := scrapeExporter.Export(ctx, &metricdata.ResourceMetrics{
			Resource:     scrapeResource,
			ScopeMetrics: []metricdata.ScopeMetrics{{Metrics: metrics[start:end]}},
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// labelsFor renders the attributes back as plain labels for the exposition cache.
func labelsFor(attrs []attribute.KeyValue) map[string]string {
	labels := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		labels[string(attr.Key)] = attr.Value.Emit()
	}
	return labels
}

// limitedReader stops reading past a byte budget without pretending the stream
// ended cleanly, so a truncated scrape is visible rather than silently partial.
type limitedReader struct {
	r         interface{ Read([]byte) (int, error) }
	remaining int64
	mu        sync.Mutex
}

func (l *limitedReader) Read(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.remaining <= 0 {
		return 0, errScrapeBodyTooLarge
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.r.Read(p)
	l.remaining -= int64(n)
	return n, err
}
