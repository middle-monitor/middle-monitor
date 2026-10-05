package services

import (
	"database/sql"
	"log/slog"
	"sync"
	"time"
)

// PointsPerMinutePerHost is the metric ingestion budget each plan host brings,
// pooled across the organization: Free (1 host) gets 250 points/min, Pro (10)
// 2 500, a custom plan follows its purchased hosts. 250 covers a host's system
// metrics plus one filtered exporter scraped every 60s, and bounds a 14-day Pro
// trial to about 8 GB of series at 166 bytes a point. One point is one value of
// one series, which is one document in the series store.
const PointsPerMinutePerHost = 250

// PointsPerMinute is the organization budget for a set of plan limits; -1 is unlimited.
func PointsPerMinute(limits PlanLimits) int {
	if limits.MaxHosts < 0 {
		return -1
	}
	return limits.MaxHosts * PointsPerMinutePerHost
}

// How long an organization's budget is trusted before its plan is read again:
// an upgrade applies within a minute without a query per request.
const budgetCacheTTL = time.Minute

type budgetWindow struct {
	minute   int64
	accepted int
	over     int
}

type cachedBudget struct {
	limit   int
	expires time.Time
}

// IngestBudget meters points per organization and calendar minute. Counts are
// per process: with several receiver replicas each enforces the full budget,
// which is acceptable while a single replica runs.
type IngestBudget struct {
	mu      sync.Mutex
	windows map[int64]*budgetWindow
	// last holds each organization's most recent finished minute, which is what
	// agents are told so they can slow down before points are rejected.
	last   map[int64]budgetWindow
	limits map[int64]cachedBudget
	lookup func(orgID int64) (int, error)
	flush  func(orgID, minute int64, accepted, over int)
	now    func() time.Time
}

// NewIngestBudget meters against plan limits read from db and records usage there.
func NewIngestBudget(db *sql.DB) *IngestBudget {
	plans := NewPlanLimitsService(db)
	return &IngestBudget{
		windows: map[int64]*budgetWindow{},
		last:    map[int64]budgetWindow{},
		limits:  map[int64]cachedBudget{},
		lookup: func(orgID int64) (int, error) {
			limits, err := plans.GetLimits(orgID)
			if err != nil {
				return 0, err
			}
			return PointsPerMinute(limits), nil
		},
		flush: func(orgID, minute int64, accepted, over int) {
			recordIngestUsage(db, orgID, minute, accepted, over)
		},
		now: time.Now,
	}
}

// NewTestIngestBudget meters every organization at limit points per minute,
// without a database; for tests of code that consumes the budget.
func NewTestIngestBudget(limit int) *IngestBudget {
	return &IngestBudget{
		windows: map[int64]*budgetWindow{},
		last:    map[int64]budgetWindow{},
		limits:  map[int64]cachedBudget{},
		lookup:  func(int64) (int, error) { return limit, nil },
		flush:   func(int64, int64, int, int) {},
		now:     time.Now,
	}
}

// Take reserves up to n points for orgID and returns how many fit the budget and
// the budget applied (-1 when unlimited); the rest is counted as over the limit. Points of an unknown organization are
// not metered: their storage is reclaimed by retention as orphans.
func (b *IngestBudget) Take(orgID int64, n int) (granted, limit int) {
	if orgID <= 0 || n <= 0 {
		return n, -1
	}
	limit = b.limitFor(orgID)
	minute := b.now().Unix() / 60

	b.mu.Lock()
	defer b.mu.Unlock()
	w := b.rollLocked(orgID, minute)
	granted = n
	if limit >= 0 {
		granted = max(0, min(n, limit-w.accepted))
	}
	w.accepted += granted
	w.over += n - granted
	return granted, limit
}

// Refund gives back points that were granted but never stored, so a publish
// failure retried by the client is not charged twice.
func (b *IngestBudget) Refund(orgID int64, n int) {
	if b == nil || orgID <= 0 || n <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if w, ok := b.windows[orgID]; ok && w.minute == b.now().Unix()/60 {
		w.accepted = max(0, w.accepted-n)
	}
}

// FlushIdle records the windows of minutes that are over. Run every minute so
// an organization that stops sending still has its last minute recorded.
func (b *IngestBudget) FlushIdle() {
	minute := b.now().Unix() / 60
	b.mu.Lock()
	var done []budgetWindow
	var orgs []int64
	for orgID, w := range b.windows {
		if w.minute < minute {
			done = append(done, *w)
			orgs = append(orgs, orgID)
			b.rememberLocked(orgID, *w)
			delete(b.windows, orgID)
		}
	}
	b.mu.Unlock()
	for i, w := range done {
		b.flush(orgs[i], w.minute, w.accepted, w.over)
	}
}

// StartFlusher runs FlushIdle every minute until stop is closed.
func (b *IngestBudget) StartFlusher(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				b.FlushIdle()
			case <-stop:
				return
			}
		}
	}()
}

// Status returns an organization's budget and the points it sent in the last
// finished minute; 0 when that minute saw none. limit is -1 when unmetered.
func (b *IngestBudget) Status(orgID int64) (limit, lastMinute int) {
	if b == nil || orgID <= 0 {
		return -1, 0
	}
	limit = b.limitFor(orgID)
	previous := b.now().Unix()/60 - 1
	b.mu.Lock()
	defer b.mu.Unlock()
	if w, ok := b.last[orgID]; ok && w.minute == previous {
		return limit, w.accepted + w.over
	}
	if w, ok := b.windows[orgID]; ok && w.minute == previous {
		return limit, w.accepted + w.over
	}
	return limit, 0
}

func (b *IngestBudget) rememberLocked(orgID int64, w budgetWindow) {
	if b.last == nil {
		b.last = map[int64]budgetWindow{}
	}
	b.last[orgID] = w
}

func (b *IngestBudget) rollLocked(orgID, minute int64) *budgetWindow {
	w, ok := b.windows[orgID]
	if ok && w.minute == minute {
		return w
	}
	if ok {
		old := *w
		b.rememberLocked(orgID, old)
		go b.flush(orgID, old.minute, old.accepted, old.over)
	}
	w = &budgetWindow{minute: minute}
	b.windows[orgID] = w
	return w
}

func (b *IngestBudget) limitFor(orgID int64) int {
	b.mu.Lock()
	cached, ok := b.limits[orgID]
	b.mu.Unlock()
	if ok && b.now().Before(cached.expires) {
		return cached.limit
	}
	limit, err := b.lookup(orgID)
	if err != nil {
		// A plan lookup failure must not turn into data loss: meter nothing until
		// the next attempt rather than rejecting a paying organization.
		slog.Warn("ingest budget lookup failed", "org_id", orgID, "error", err)
		return -1
	}
	b.mu.Lock()
	b.limits[orgID] = cachedBudget{limit: limit, expires: b.now().Add(budgetCacheTTL)}
	b.mu.Unlock()
	return limit
}

func recordIngestUsage(db *sql.DB, orgID, minute int64, accepted, over int) {
	if accepted == 0 && over == 0 {
		return
	}
	_, err := db.Exec(`
		INSERT INTO ingest_usage (organization_id, minute, accepted_points, over_limit_points)
		VALUES ($1, to_timestamp($2), $3, $4)
		ON CONFLICT (organization_id, minute) DO UPDATE SET
			accepted_points = ingest_usage.accepted_points + EXCLUDED.accepted_points,
			over_limit_points = ingest_usage.over_limit_points + EXCLUDED.over_limit_points
	`, orgID, minute*60, accepted, over)
	if err != nil {
		slog.Error("record ingest usage failed", "org_id", orgID, "error", err)
	}
}
