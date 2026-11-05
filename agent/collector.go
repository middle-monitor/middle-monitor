package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultCollectorConcurrency = 32
	defaultCollectorRefresh     = 30 * time.Second
	collectorFlushInterval      = 5 * time.Second
	collectorBatchSize          = 500
	// About ten minutes of a busy zone; past it the oldest results go first.
	collectorMaxPending = 20000
)

// collectorCheck is one check the platform assigned to this zone.
type collectorCheck struct {
	ID       int64               `json:"id"`
	Name     string              `json:"name"`
	Command  string              `json:"command"`
	Vars     map[string][]string `json:"vars"`
	Interval int                 `json:"interval"`
}

// fingerprint changes when the check must be restarted.
func (c collectorCheck) fingerprint() string {
	vars, _ := json.Marshal(c.Vars)
	return c.Command + "|" + string(vars) + "|" + strconv.Itoa(c.Interval)
}

type collectorSyncRequest struct {
	Zone     string   `json:"zone"`
	Hostname string   `json:"hostname"`
	Version  string   `json:"version"`
	Commands []string `json:"commands"`
}

type collectorSyncResponse struct {
	Checks         []collectorCheck `json:"checks"`
	RefreshSeconds int              `json:"refresh_seconds"`
}

type collectorResult struct {
	ServiceID int64 `json:"service_id"`
	PluginResult
}

type collectorResultBatch struct {
	Zone     string            `json:"zone"`
	Hostname string            `json:"hostname"`
	Results  []collectorResult `json:"results"`
}

type runningCheck struct {
	fingerprint string
	cancel      context.CancelFunc
}

// Collector runs the checks of a zone and posts their results.
type Collector struct {
	apiURL, apiKey, zone, hostname string
	commands                       map[string]Command
	client                         *http.Client
	slots                          chan struct{}
	results                        chan collectorResult
	flushEvery                     time.Duration

	mu      sync.Mutex
	running map[int64]runningCheck

	cancel context.CancelFunc
	done   sync.WaitGroup
}

// StartCollector starts the zone collector when it is enabled; Stop on the
// returned value is safe either way.
func StartCollector(config *AgentConfig) *Collector {
	c := newCollector(config)
	if !config.Collector.Enabled {
		return c
	}
	c.start()
	slog.Info("collector started", "zone", c.zone, "commands", len(c.commands))
	return c
}

func (c *Collector) start() {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.done.Add(2)
	go func() { defer c.done.Done(); c.sendLoop(ctx) }()
	go func() { defer c.done.Done(); c.syncLoop(ctx) }()
}

func newCollector(config *AgentConfig) *Collector {
	concurrency := config.Collector.MaxConcurrency
	if concurrency <= 0 {
		concurrency = defaultCollectorConcurrency
	}
	return &Collector{
		apiURL:     strings.TrimSuffix(config.API.URL, "/"),
		apiKey:     config.API.APIKey,
		zone:       config.Collector.Zone,
		hostname:   config.Host.Name,
		commands:   config.Collector.commands,
		client:     &http.Client{Timeout: 15 * time.Second},
		slots:      make(chan struct{}, concurrency),
		results:    make(chan collectorResult, 1000),
		flushEvery: collectorFlushInterval,
		running:    map[int64]runningCheck{},
	}
}

// Stop ends every check and sends what is still pending.
func (c *Collector) Stop() {
	if c.cancel == nil {
		return
	}
	c.cancel()
	c.done.Wait()
}

func (c *Collector) syncLoop(ctx context.Context) {
	for {
		refresh := defaultCollectorRefresh
		response, err := c.sync(ctx)
		if err != nil {
			slog.Warn("collector sync failed, keeping the running checks", "err", err)
		} else {
			c.reconcile(ctx, response.Checks)
			if response.RefreshSeconds > 0 {
				refresh = time.Duration(response.RefreshSeconds) * time.Second
			}
		}
		select {
		case <-ctx.Done():
			c.reconcile(ctx, nil)
			return
		case <-time.After(refresh):
		}
	}
}

func (c *Collector) sync(ctx context.Context) (collectorSyncResponse, error) {
	names := make([]string, 0, len(c.commands))
	for name := range c.commands {
		names = append(names, name)
	}
	sort.Strings(names)
	var response collectorSyncResponse
	err := c.post(ctx, "/api/v1/collectors/sync",
		collectorSyncRequest{Zone: c.zone, Hostname: c.hostname, Version: Version, Commands: names}, &response)
	return response, err
}

// reconcile starts the new checks, restarts the changed ones and stops those
// the platform no longer assigns.
func (c *Collector) reconcile(ctx context.Context, checks []collectorCheck) {
	c.mu.Lock()
	defer c.mu.Unlock()
	wanted := make(map[int64]collectorCheck, len(checks))
	for _, check := range checks {
		wanted[check.ID] = check
	}
	for id, run := range c.running {
		if check, ok := wanted[id]; !ok || check.fingerprint() != run.fingerprint {
			run.cancel()
			delete(c.running, id)
		}
	}
	if ctx.Err() != nil {
		return
	}
	for id, check := range wanted {
		if _, ok := c.running[id]; ok {
			continue
		}
		checkCtx, cancel := context.WithCancel(ctx)
		c.running[id] = runningCheck{fingerprint: check.fingerprint(), cancel: cancel}
		go c.runCheck(checkCtx, check)
	}
}

func (c *Collector) runCheck(ctx context.Context, check collectorCheck) {
	interval := time.Duration(check.Interval) * time.Second
	if interval <= 0 {
		interval = time.Minute
	}
	// Spread the first runs over the interval: a zone's checks starting in the
	// same second would hit the satellite and the NRPE daemons all at once.
	timer := time.NewTimer(time.Duration(rand.Int63n(int64(interval))))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		result, ok := c.execute(ctx, check)
		if !ok {
			return
		}
		select {
		case c.results <- collectorResult{ServiceID: check.ID, PluginResult: result}:
		case <-ctx.Done():
			return
		}
		timer.Reset(interval)
	}
}

// execute runs one check within the concurrency limit. A command this machine
// does not declare is reported as unknown, so the check shows why it is not
// running instead of going silent.
func (c *Collector) execute(ctx context.Context, check collectorCheck) (PluginResult, bool) {
	command, declared := c.commands[check.Command]
	if !declared {
		return PluginResult{
			State: stateUnknown, ExitCode: 3, ExecutedAt: time.Now().UTC(),
			Output: "UNKNOWN - command " + check.Command + " is not declared on collector " + c.hostname,
		}, true
	}
	select {
	case c.slots <- struct{}{}:
	case <-ctx.Done():
		return PluginResult{}, false
	}
	defer func() { <-c.slots }()
	return runPlugin(ctx, command, check.Vars), true
}

func (c *Collector) sendLoop(ctx context.Context) {
	ticker := time.NewTicker(c.flushEvery)
	defer ticker.Stop()
	var pending []collectorResult
	for {
		select {
		case result := <-c.results:
			pending = append(pending, result)
			if len(pending) >= collectorBatchSize {
				pending = c.flush(ctx, pending)
			}
		case <-ticker.C:
			pending = c.flush(ctx, pending)
		case <-ctx.Done():
			for drained := false; !drained; {
				select {
				case result := <-c.results:
					pending = append(pending, result)
				default:
					drained = true
				}
			}
			// One last try, on a fresh context: the collector's own is gone.
			final, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			c.flush(final, pending)
			cancel()
			return
		}
	}
}

// flush posts pending results in batches and returns what could not be sent,
// so a platform restart delays results instead of losing them.
func (c *Collector) flush(ctx context.Context, pending []collectorResult) []collectorResult {
	for len(pending) > 0 {
		n := min(len(pending), collectorBatchSize)
		batch := collectorResultBatch{Zone: c.zone, Hostname: c.hostname, Results: pending[:n]}
		if err := c.post(ctx, "/api/v1/collectors/results", batch, nil); err != nil {
			slog.Warn("collector results not sent, retrying later", "pending", len(pending), "err", err)
			break
		}
		pending = pending[n:]
	}
	if over := len(pending) - collectorMaxPending; over > 0 {
		slog.Warn("collector results dropped, platform unreachable too long", "dropped", over)
		pending = pending[over:]
	}
	return pending
}

func (c *Collector) post(ctx context.Context, path string, body, into any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.apiURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-Install-Token", c.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &collectorStatusError{Path: path, StatusCode: resp.StatusCode}
	}
	if into == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(into)
}
