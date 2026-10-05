package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// Version is stamped at build time by build.sh (-X main.Version).
var Version = "dev"

const defaultConfigPath = "/etc/middle-monitor/config.yaml"

type AgentConfig struct {
	API struct {
		URL    string `yaml:"url"`
		APIKey string `yaml:"api_key,omitempty"`
	} `yaml:"api"`
	Host struct {
		Name    string `yaml:"name"`
		Service string `yaml:"service"`
	} `yaml:"host"`
	Metrics struct {
		CPU     bool `yaml:"cpu"`     // Default: true
		RAM     bool `yaml:"ram"`     // Default: true
		Disk    bool `yaml:"disk"`    // Default: true
		Network bool `yaml:"network"` // Default: true
	} `yaml:"metrics"`
	Interval int `yaml:"interval"` // Interval in seconds, default: 60
	// Pull configuration: endpoints exposing the Prometheus text format that the
	// agent scrapes itself, on top of the host metrics it collects.
	Scrape ScrapeConfig `yaml:"scrape"`
	// Serve what the agent holds in the Prometheus text format, so an existing
	// Prometheus can keep scraping while the customer migrates.
	Expose ExposeConfig `yaml:"expose"`
	// Extra scrape targets served by the platform for this host, so a target can
	// be added without touching the machine. Off by default.
	RemoteConfig RemoteConfigConfig `yaml:"remote_config"`
}

type AgentRegistration struct {
	Hostname string   `json:"hostname"`
	Name     string   `json:"name"`
	Service  string   `json:"service"`
	Metrics  []string `json:"metrics"` // ["cpu", "ram", "disk"]
}

type AgentMetrics struct {
	Hostname  string             `json:"hostname"`
	Service   string             `json:"service"`
	Metrics   map[string]float64 `json:"metrics"`            // {"cpu": 45.2, "ram": 67.8, "disk": 23.1}
	Metadata  map[string]float64 `json:"metadata,omitempty"` // {"ram_total_gb": 16.0, "disk_total_gb": 500.0}
	Timestamp time.Time          `json:"timestamp"`
}

type AgentRegistrationResponse struct {
	TargetID int64   `json:"target_id"`
	CheckIDs []int64 `json:"check_ids"`
}

func loadConfig(path string) (*AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errConfigRead, err)
	}

	var config AgentConfig
	// Unmarshal only overwrites the keys present, so these stay on unless disabled.
	config.Metrics.CPU, config.Metrics.RAM, config.Metrics.Disk, config.Metrics.Network = true, true, true, true
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("%w: %w", errConfigParse, err)
	}

	if config.Interval <= 0 {
		config.Interval = 60
	}
	if config.Host.Name == "" {
		hostname, _ := os.Hostname()
		config.Host.Name = hostname
	}
	if config.Host.Service == "" {
		config.Host.Service = "default"
	}

	// Fragments, parameter expansion and validation happen here so a bad scrape
	// config fails the load rather than one target loop at a time.
	if err := PrepareScrape(&config.Scrape); err != nil {
		return nil, err
	}

	return &config, nil
}

// resolveConfigPath prefers the flag, then the environment, then the packaged
// location.
func resolveConfigPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if fromEnv := os.Getenv("MIDDLE_MONITOR_CONFIG"); fromEnv != "" {
		return fromEnv
	}
	return defaultConfigPath
}

func registerAgent(config *AgentConfig) (*AgentRegistrationResponse, error) {
	// Use config.Host.Name (from install script / config) to link to existing host or create with that name
	// os.Hostname() would use machine hostname, ignoring user's choice in install script
	hostname := config.Host.Name
	if hostname == "" {
		hostname, _ = os.Hostname()
	}

	metrics := []string{}
	if config.Metrics.CPU {
		metrics = append(metrics, "cpu")
	}
	if config.Metrics.RAM {
		metrics = append(metrics, "ram")
	}
	if config.Metrics.Disk {
		metrics = append(metrics, "disk")
	}
	if config.Metrics.Network {
		metrics = append(metrics, "network")
	}

	registration := AgentRegistration{
		Hostname: hostname,
		Name:     config.Host.Name,
		Service:  config.Host.Service,
		Metrics:  metrics,
	}

	jsonData, err := json.Marshal(registration)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errRegistrationMarshal, err)
	}

	url := fmt.Sprintf("%s/api/v1/agents/register", config.API.URL)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errRequestCreate, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if config.API.APIKey != "" {
		req.Header.Set("X-Install-Token", config.API.APIKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errRegister, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, &registrationStatusError{StatusCode: resp.StatusCode}
	}

	var response AgentRegistrationResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("%w: %w", errRegistrationDecode, err)
	}

	return &response, nil
}

func collectMetrics(config *AgentConfig) (*AgentMetrics, error) {
	// Use same hostname as registration (config.Host.Name) so StoreMetrics finds the services
	hostname := config.Host.Name
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	metrics := make(map[string]float64)
	metadata := make(map[string]float64)

	if config.Metrics.CPU {
		cpu, err := getCPUUsage()
		if err != nil {
			slog.Warn("read cpu usage failed", "err", err)
		} else {
			metrics["cpu"] = cpu
		}
		loadAvg, err := getLoadAverage()
		if err == nil {
			metadata["load_1min"] = loadAvg[0]
			metadata["load_5min"] = loadAvg[1]
			metadata["load_15min"] = loadAvg[2]
		}
	}

	if config.Metrics.RAM {
		ram, err := getRAMUsage()
		if err != nil {
			slog.Warn("read ram usage failed", "err", err)
		} else {
			metrics["ram"] = ram
			ramTotal, err := getRAMTotal()
			if err == nil {
				metadata["ram_total_gb"] = ramTotal
			}
		}
	}

	if config.Metrics.Disk {
		disk, err := getDiskUsage()
		if err != nil {
			slog.Warn("read disk usage failed", "err", err)
		} else {
			metrics["disk"] = disk
			// Get disk total and free space for metadata
			diskTotal, err := getDiskTotal()
			if err == nil {
				metadata["disk_total_gb"] = diskTotal
			}
			// Get disk free space directly (more accurate than calculating from percentage)
			diskFree, err := getDiskFree()
			if err == nil {
				metadata["disk_free_gb"] = diskFree
			}
		}
	}

	if config.Metrics.Network {
		networkMetrics, networkMetadata, err := getNetworkMetrics()
		if err != nil {
			slog.Warn("read network metrics failed", "err", err)
		} else {
			for k, v := range networkMetrics {
				metrics[k] = v
			}
			for k, v := range networkMetadata {
				metadata[k] = v
			}
		}
	}

	return &AgentMetrics{
		Hostname:  hostname,
		Service:   config.Host.Service,
		Metrics:   metrics,
		Metadata:  metadata,
		Timestamp: time.Now(),
	}, nil
}

// sendMetrics retries a transient failure within half the collection interval, so
// a receiver restart or a Kafka hiccup no longer costs a whole data point.
func sendMetrics(config *AgentConfig, metrics *AgentMetrics) error {
	jsonData, err := json.Marshal(metrics)
	if err != nil {
		return fmt.Errorf("%w: %w", errMetricsMarshal, err)
	}

	deadline := time.Now().Add(time.Duration(config.Interval) * time.Second / 2)
	backoff := time.Second
	for attempt := 1; ; attempt++ {
		err := sendMetricsOnce(config, jsonData)
		if err == nil || !retryableSend(err) || time.Now().Add(backoff).After(deadline) {
			return err
		}
		slog.Warn("send metrics retry", "attempt", attempt, "backoff", backoff, "err", err)
		time.Sleep(backoff)
		backoff *= 2
	}
}

// retryableSend is true for what a second try can fix: no answer, rate limiting,
// or a server-side failure. A 4xx other than 429 would fail the same way again.
func retryableSend(err error) bool {
	var status *sendStatusError
	if errors.As(err, &status) {
		return status.StatusCode == http.StatusTooManyRequests || status.StatusCode >= 500
	}
	return true
}

func sendMetricsOnce(config *AgentConfig, jsonData []byte) error {
	url := fmt.Sprintf("%s/api/v1/agents/metrics", config.API.URL)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("%w: %w", errRequestCreate, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if config.API.APIKey != "" {
		req.Header.Set("X-Install-Token", config.API.APIKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", errMetricsSend, err)
	}
	defer resp.Body.Close()

	// The backend answers 202 when it queues the metrics through Kafka, so accept
	// any 2xx rather than only 200/201.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return &sendStatusError{StatusCode: resp.StatusCode, Body: string(body)}
	}

	// The answer carries the organization's metric budget; an older platform
	// answers plain text, which simply leaves the intervals as configured.
	var answer struct {
		Ingest *ingestStatus `json:"ingest"`
	}
	if json.NewDecoder(resp.Body).Decode(&answer) == nil && answer.Ingest != nil {
		scrapeBudget.update(*answer.Ingest)
	}

	return nil
}

func main() {
	configFlag := flag.String("config", "", "path to config.yaml (defaults to $MIDDLE_MONITOR_CONFIG then "+defaultConfigPath+")")
	showVersion := flag.Bool("version", false, "print the agent version and exit")
	configCheck := flag.Bool("config-check", false, "validate the configuration and exit non-zero if it is broken")
	flag.Parse()

	if *showVersion {
		fmt.Println(Version)
		return
	}

	configPath := resolveConfigPath(*configFlag)

	// --config-check is what lets a deployment refuse to restart an agent on a
	// config it just broke, so it must not touch the network or the exporter.
	if *configCheck {
		if _, err := loadConfig(configPath); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", configPath, err)
			os.Exit(1)
		}
		fmt.Printf("%s: ok\n", configPath)
		return
	}

	config, err := loadConfig(configPath)
	if err != nil {
		slog.Error("load config failed", "path", configPath, "err", err)
		os.Exit(1)
	}
	slog.Info("config loaded", "path", configPath, "version", Version,
		"cpu", config.Metrics.CPU, "ram", config.Metrics.RAM, "disk", config.Metrics.Disk, "network", config.Metrics.Network)

	if config.API.URL == "" {
		slog.Error("api url is required in config", "path", configPath)
		os.Exit(1)
	}

	if err := initializeOTELMetrics(config); err != nil {
		slog.Error("init otel metrics failed", "err", err)
		os.Exit(1)
	}

	// Pull the configured exposition endpoints alongside the host metrics.
	scrapeCtx, stopScraping := context.WithCancel(context.Background())
	defer stopScraping()
	scraper := StartScraping(scrapeCtx, config.Scrape)
	StartRemoteConfig(scrapeCtx, scraper, config)
	StartExposition(scrapeCtx, config.Expose)
	StartCacheEviction(scrapeCtx, config.Expose)

	// Registration creates the host and its checks. Ingestion works without it,
	// so a failure here is not fatal.
	registration, err := registerAgent(config)
	if err != nil {
		slog.Warn("register agent failed, continuing", "err", err)
	} else {
		slog.Info("agent registered", "target_id", registration.TargetID, "check_ids", registration.CheckIDs)
	}

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// SIGHUP reloads the config in place: a restart would drop the running
	// scrape cycle, and the target list changes far more often than the binary.
	hupChan := make(chan os.Signal, 1)
	signal.Notify(hupChan, syscall.SIGHUP)

	ticker := time.NewTicker(time.Duration(config.Interval) * time.Second)
	defer ticker.Stop()

	slog.Info("collecting metrics", "interval_seconds", config.Interval)
	report(config)

	for {
		select {
		case <-ticker.C:
			report(config)

		case <-hupChan:
			reloaded, err := loadConfig(configPath)
			if err != nil {
				slog.Warn("reload failed, keeping the running configuration", "err", err)
				continue
			}
			scraper.Reload(scrapeCtx, reloaded.Scrape)
			if reloaded.Interval != config.Interval {
				ticker.Reset(time.Duration(reloaded.Interval) * time.Second)
			}
			if reloaded.Expose != config.Expose {
				slog.Warn("expose settings changed, restart the agent to apply them")
			}
			config = reloaded
			slog.Info("config reloaded", "path", configPath)

		case <-sigChan:
			slog.Info("shutting down")
			// Gracefully shutdown OpenTelemetry
			if meterProvider != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := meterProvider.Shutdown(ctx); err != nil {
					slog.Warn("meter provider shutdown failed", "err", err)
				}
			}
			return
		}
	}
}

// report collects one round of host metrics, sends it to the API (which feeds
// the checks) and exports it over OTLP.
func report(config *AgentConfig) {
	metrics, err := collectMetrics(config)
	if err != nil {
		slog.Warn("collect metrics failed", "err", err)
		return
	}
	if err := sendMetrics(config, metrics); err != nil {
		slog.Warn("send metrics failed", "err", err)
	}
	if err := exportMetricsOTLP(metrics); err != nil {
		slog.Warn("export metrics failed", "err", err)
		return
	}
	slog.Info("metrics reported", "metrics", metrics.Metrics)
}
