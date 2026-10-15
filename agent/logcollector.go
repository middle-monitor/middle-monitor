package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	logScanInterval = time.Second
	logSaveInterval = 5 * time.Second
	// Records waiting for the exporter. A full queue blocks the tailers, which
	// then simply read later: the files are the buffer.
	logQueueSize = 2000
)

// logCollector finds the files the sources match, runs one tailer per file and
// ships what they read.
type logCollector struct {
	cfg         LogsConfig
	hostname    string
	hostService string
	exporter    *logExporter
	positions   *logPositions
	records     chan logRecord

	tailers map[string]*tailer
	skipped map[string]bool
	// Position keys of the journald sources, saved like the tailed files.
	journals map[string]bool
	done     chan string
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// StartLogCollection runs the logs block, or returns nil when it is off.
func StartLogCollection(config *AgentConfig) *logCollector {
	if !config.Logs.Enabled {
		return nil
	}
	exporter, err := newLogExporter(config.API.URL, config.API.APIKey, config.Logs.MaxLinesPerSecond)
	if err != nil {
		slog.Error("log collection disabled", "err", err)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &logCollector{
		cfg:         config.Logs,
		hostname:    config.Host.Name,
		hostService: config.Host.Service,
		exporter:    exporter,
		positions:   loadLogPositions(config.Logs.StateFile),
		records:     make(chan logRecord, logQueueSize),
		tailers:     map[string]*tailer{},
		skipped:     map[string]bool{},
		journals:    map[string]bool{},
		done:        make(chan string, 64),
		cancel:      cancel,
	}
	exported := make(chan struct{})
	go func() {
		c.exporter.run(ctx, c.records, c.positions)
		close(exported)
	}()
	// Synchronous, so "already there at startup" means exactly that.
	c.scan(ctx, true)
	c.startJournals(ctx)
	c.wg.Add(1)
	go c.loop(ctx, exported)
	slog.Info("log collection started", "sources", len(c.cfg.Sources), "state_file", c.cfg.StateFile)
	return c
}

// Stop flushes what was read and saves the positions.
func (c *logCollector) Stop() {
	if c == nil {
		return
	}
	c.cancel()
	c.wg.Wait()
}

func (c *logCollector) loop(ctx context.Context, exported <-chan struct{}) {
	defer c.wg.Done()
	scan := time.NewTicker(logScanInterval)
	save := time.NewTicker(logSaveInterval)
	defer scan.Stop()
	defer save.Stop()
	for {
		select {
		case <-ctx.Done():
			// The exporter sends its last batch on cancel; positions are saved
			// after it so they include that batch.
			<-exported
			c.positions.save(c.activeKeys())
			return
		case key := <-c.done:
			delete(c.tailers, key)
		case <-scan.C:
			c.scan(ctx, false)
		case <-save.C:
			c.positions.save(c.activeKeys())
		}
	}
}

func (c *logCollector) activeKeys() map[string]bool {
	keys := make(map[string]bool, len(c.tailers)+len(c.journals))
	for key := range c.tailers {
		keys[key] = true
	}
	for key := range c.journals {
		keys[key] = true
	}
	return keys
}

// scan starts a tailer for each new file and marks the ones that no longer
// match as gone. At startup a file with no saved position is read from its end,
// unless the source asks for the beginning; a file that appears later is new
// content and is read whole.
func (c *logCollector) scan(ctx context.Context, startup bool) {
	seen := map[string]bool{}
	for i := range c.cfg.Sources {
		src := &c.cfg.Sources[i]
		if src.Type == logSourceJournald {
			continue
		}
		for _, path := range globAll(src.Path) {
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			key, ok := fileKey(info)
			if !ok || seen[key] {
				continue
			}
			seen[key] = true
			if c.tailers[key] != nil || c.skipped[key] {
				continue
			}
			origin := resolveOrigin(src, path, c.hostname, c.hostService)
			if origin.skip {
				c.skipped[key] = true
				continue
			}
			c.startTailer(ctx, src, origin, key, path, c.startOffset(src, key, info, startup))
		}
	}
	for key, t := range c.tailers {
		if !seen[key] {
			t.gone.Store(true)
		}
	}
	for key := range c.skipped {
		if !seen[key] {
			delete(c.skipped, key)
		}
	}
}

func (c *logCollector) startOffset(src *LogSource, key string, info os.FileInfo, startup bool) int64 {
	// A saved offset past the end belongs to another file that reused the inode.
	if pos, ok := c.positions.get(key); ok && pos.Offset <= info.Size() {
		return pos.Offset
	}
	if startup && src.StartAt != "beginning" {
		return info.Size()
	}
	return 0
}

func (c *logCollector) startTailer(ctx context.Context, src *LogSource, origin *logOrigin, key, path string, offset int64) {
	f, err := os.Open(path)
	if err != nil {
		// Usually an agent not running as root in front of /var/lib/docker. The
		// file is left alone until it disappears instead of failing every scan.
		slog.Warn("cannot read log file", "path", path, "err", err)
		c.skipped[key] = true
		return
	}
	t := &tailer{key: key, path: path, src: src, origin: origin, offset: offset}
	t.emit = c.emitter(src)
	c.tailers[key] = t
	go func() {
		t.run(ctx, f)
		select {
		case c.done <- key:
		case <-ctx.Done():
		}
	}()
	slog.Debug("tailing log file", "path", path, "service", origin.service, "offset", offset)
}

// startJournals runs one journalctl per journald source, resuming after the
// cursor the backend last accepted.
func (c *logCollector) startJournals(ctx context.Context) {
	n := 0
	for i := range c.cfg.Sources {
		src := &c.cfg.Sources[i]
		if src.Type != logSourceJournald {
			continue
		}
		key := "journald:" + strconv.Itoa(n)
		n++
		c.journals[key] = true
		pos, _ := c.positions.get(key)
		j := &journalReader{
			key: key, src: src, hostname: c.hostname, cursor: pos.Cursor,
			origins: map[string]*logOrigin{}, emit: c.emitter(src),
		}
		go j.run(ctx)
	}
}

// emitter queues a source's records for export. False means the collector
// stopped.
func (c *logCollector) emitter(src *LogSource) func(context.Context, logRecord) bool {
	return func(ctx context.Context, rec logRecord) bool {
		if !c.prepare(src, &rec) {
			// A dropped record still moves the position, or it is read again.
			c.positions.ack(rec)
			return true
		}
		select {
		case c.records <- rec:
			return true
		case <-ctx.Done():
			return false
		}
	}
}

// prepare runs the processing rules, then reads the level and the timestamp the
// line carries. False means a rule dropped it.
func (c *logCollector) prepare(src *LogSource, rec *logRecord) bool {
	// OTLP refuses to encode invalid UTF-8, which would drop the whole batch:
	// a Latin-1 line or a cut multibyte character must not cost its neighbours.
	body, keep := applyLogRules(c.cfg.Rules, strings.ToValidUTF8(rec.body, "\uFFFD"))
	if keep {
		body, keep = applyLogRules(src.Rules, body)
	}
	if !keep {
		return false
	}
	fields := jsonFields(body)
	rec.body = body
	// A level the line declares wins over the one its source reports: a service
	// writing "level=error" to stdout reaches the journal as info.
	fallback := rec.severity
	if fallback == "" {
		fallback = "INFO"
	}
	rec.severity = detectSeverity(fields, body, fallback)
	if t, ok := jsonTime(fields); ok {
		rec.time = t
	}
	if rec.time.IsZero() {
		rec.time = rec.observed
	}
	return true
}

func globAll(patterns []string) []string {
	var paths []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		paths = append(paths, matches...)
	}
	sort.Strings(paths)
	return paths
}
