package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultRemoteConfigInterval = 300 * time.Second

// RemoteConfigConfig lets the platform hand this agent extra scrape targets, so
// an operator can add one without touching the machine. Disabled by default: an
// agent must not start taking instructions from the network because it was
// upgraded.
type RemoteConfigConfig struct {
	Enabled  bool `yaml:"enabled"`
	Interval int  `yaml:"interval"` // seconds between refreshes, default 300
}

func (c RemoteConfigConfig) interval() time.Duration {
	if c.Interval > 0 {
		return time.Duration(c.Interval) * time.Second
	}
	return defaultRemoteConfigInterval
}

// remoteConfigDocument is what GET /api/v1/agents/config answers.
type remoteConfigDocument struct {
	ScrapeConfig string `json:"scrape_config"`
	UpdatedAt    string `json:"updated_at"`
}

// fetchRemoteTargets asks the platform for the fragment configured for this
// host. It returns the targets and the version stamp, so an unchanged config
// costs nothing to apply.
func fetchRemoteTargets(ctx context.Context, client *http.Client, config *AgentConfig) ([]ScrapeTarget, string, error) {
	endpoint := strings.TrimSuffix(config.API.URL, "/") + "/api/v1/agents/config?host=" + url.QueryEscape(config.Host.Name)
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	if config.API.APIKey != "" {
		req.Header.Set("X-Install-Token", config.API.APIKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", &remoteConfigError{StatusCode: resp.StatusCode}
	}

	var document remoteConfigDocument
	if err := json.NewDecoder(resp.Body).Decode(&document); err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(document.ScrapeConfig) == "" {
		return nil, document.UpdatedAt, nil
	}

	var fragment scrapeFragment
	if err := yaml.Unmarshal([]byte(document.ScrapeConfig), &fragment); err != nil {
		return nil, "", err
	}
	return fragment.Targets, document.UpdatedAt, nil
}

// StartRemoteConfig keeps the manager's target set in line with what the
// platform serves for this host. The locally configured targets always win on a
// name collision: what an operator wrote on the machine is not silently
// replaced from the network.
func StartRemoteConfig(ctx context.Context, manager *scrapeManager, config *AgentConfig) {
	if !config.RemoteConfig.Enabled {
		return
	}

	go func() {
		client := &http.Client{Timeout: 15 * time.Second}
		ticker := time.NewTicker(config.RemoteConfig.interval())
		defer ticker.Stop()

		local := append([]ScrapeTarget{}, config.Scrape.Targets...)
		applied := ""

		for {
			targets, version, err := fetchRemoteTargets(ctx, client, config)
			switch {
			case err != nil:
				slog.Warn("remote config fetch failed, keeping the running configuration", "err", err)
			case version != "" && version == applied:
				// Nothing changed: do not restart loops for an identical config.
			default:
				merged := config.Scrape
				merged.Targets = dedupByName(append(append([]ScrapeTarget{}, local...), targets...))
				if err := PrepareScrape(&merged); err != nil {
					slog.Warn("remote config rejected", "err", err)
					break
				}
				manager.Reload(ctx, merged)
				applied = version
				slog.Info("remote config applied", "targets", len(targets))
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	slog.Info("remote config started", "interval", config.RemoteConfig.interval())
}
