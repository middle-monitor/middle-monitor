package main

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// A target is slowed at most 8 times: past that it is better dropped, which
	// is the operator's call, not the agent's.
	maxIntervalMultiplier = 8
	// Aim a little under the share so one noisy minute does not undo the change.
	budgetTargetRatio = 0.95
	// Hysteresis: speed back up only well under the limit, and only if the step
	// back keeps the organization under 85% of it.
	budgetRelaxBelow   = 0.70
	budgetRelaxCeiling = 0.85
	// While the limit is only observed, the cost table is logged this often.
	observedLogEvery = 15 * time.Minute
)

// ingestStatus is the budget the platform answers on the metrics call.
type ingestStatus struct {
	PointsPerMinuteLimit int  `json:"points_per_minute_limit"` // -1 = unlimited
	OrgPointsLastMinute  int  `json:"org_points_last_minute"`
	PointsPerHost        int  `json:"points_per_host"`
	Enforced             bool `json:"enforced"`
}

type targetCost struct {
	base   time.Duration
	series int
	mult   int
}

// pointsPerMinute is what the target sends at its current interval.
func (t *targetCost) pointsPerMinute() float64 {
	if t.series == 0 || t.base <= 0 {
		return 0
	}
	return float64(t.series) * float64(time.Minute) / float64(t.base*time.Duration(t.mult))
}

// budgetController lengthens the heaviest scrape intervals when the
// organization goes over its metric budget, so points arrive less often instead
// of being rejected, and gives the intervals back once there is room.
type budgetController struct {
	mu        sync.Mutex
	targets   map[string]*targetCost
	status    *ingestStatus
	lastNotes time.Time
	now       func() time.Time
	logf      func(string, ...interface{})
}

func newBudgetController() *budgetController {
	return &budgetController{targets: map[string]*targetCost{}, now: time.Now, logf: logBudget}
}

var scrapeBudget = newBudgetController()

func targetKey(t ScrapeTarget) string {
	if t.Name != "" {
		return t.Name
	}
	return t.URL
}

func (b *budgetController) register(key string, base time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.targets[key] = &targetCost{base: base, mult: 1}
}

func (b *budgetController) unregister(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.targets, key)
}

// observe records how many series the target's last scrape exported.
func (b *budgetController) observe(key string, series int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t, ok := b.targets[key]; ok {
		t.series = series
	}
}

// interval is the target's current interval, its base one unless slowed.
func (b *budgetController) interval(key string, base time.Duration) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t, ok := b.targets[key]; ok {
		return base * time.Duration(t.mult)
	}
	return base
}

// update takes the platform's latest answer and rebalances the intervals.
func (b *budgetController) update(status ingestStatus) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status = &status
	b.rebalanceLocked()
}

func (b *budgetController) ownPointsLocked() float64 {
	total := 0.0
	for _, t := range b.targets {
		total += t.pointsPerMinute()
	}
	return total
}

func (b *budgetController) rebalanceLocked() {
	s := b.status
	if s == nil || s.PointsPerMinuteLimit < 0 {
		b.resetLocked("the organization has no metric limit")
		return
	}
	limit, org := float64(s.PointsPerMinuteLimit), float64(s.OrgPointsLastMinute)
	switch {
	case org > limit:
		b.slowDownLocked(limit, org, s.Enforced)
	case org < budgetRelaxBelow*limit:
		b.speedUpLocked(limit, org)
	}
}

// slowDownLocked brings this agent's points to its share of the limit: every
// agent of the organization scales by limit/org, so together they fit.
func (b *budgetController) slowDownLocked(limit, org float64, enforced bool) {
	own := b.ownPointsLocked()
	goal := own * limit / org * budgetTargetRatio
	plan := map[string]int{}
	for key, t := range b.targets {
		plan[key] = t.mult
	}
	cost := func() float64 {
		total := 0.0
		for key, t := range b.targets {
			total += float64(t.series) * float64(time.Minute) / float64(t.base*time.Duration(plan[key]))
		}
		return total
	}
	for cost() > goal {
		heaviest, best := "", 0.0
		for key, t := range b.targets {
			c := float64(t.series) * float64(time.Minute) / float64(t.base*time.Duration(plan[key]))
			if plan[key] < maxIntervalMultiplier && c > best {
				heaviest, best = key, c
			}
		}
		if heaviest == "" {
			break
		}
		plan[heaviest] *= 2
	}

	header := fmt.Sprintf("organization sent %.0f points/min, over its limit of %.0f;"+
		" this agent sends %.0f, its share is about %.0f", org, limit, own, goal/budgetTargetRatio)
	if !enforced {
		// Observation: say what would change, change nothing.
		if b.now().Sub(b.lastNotes) < observedLogEvery {
			return
		}
		b.lastNotes = b.now()
		b.logf("%s. The limit is not enforced yet; once it is, this agent will scrape less often:%s",
			header, b.describeLocked(plan))
		return
	}
	changed := false
	for key, t := range b.targets {
		if plan[key] != t.mult {
			changed = true
			t.mult = plan[key]
		}
	}
	if changed {
		b.logf("%s. Scraping less often to stay under it:%s", header, b.describeLocked(nil))
	}
}

// logBudget reports a budget decision; the detail names every target it touches.
func logBudget(format string, args ...interface{}) {
	slog.Info("ingest budget", "detail", fmt.Sprintf(format, args...))
}

// speedUpLocked gives one step back to the slowed target whose return costs
// least, provided the organization stays well under the limit after it.
func (b *budgetController) speedUpLocked(limit, org float64) {
	cheapest, added := "", 0.0
	for key, t := range b.targets {
		if t.mult <= 1 {
			continue
		}
		// Halving the interval doubles the target's points.
		if c := t.pointsPerMinute(); cheapest == "" || c < added {
			cheapest, added = key, c
		}
	}
	if cheapest == "" || org+added > budgetRelaxCeiling*limit {
		return
	}
	t := b.targets[cheapest]
	t.mult /= 2
	b.logf("organization at %.0f of %.0f points/min: %s back to every %s",
		org, limit, cheapest, t.base*time.Duration(t.mult))
}

func (b *budgetController) resetLocked(reason string) {
	restored := []string{}
	for key, t := range b.targets {
		if t.mult > 1 {
			t.mult = 1
			restored = append(restored, key)
		}
	}
	if len(restored) > 0 {
		sort.Strings(restored)
		b.logf("%s: %s back to their configured interval", reason, strings.Join(restored, ", "))
	}
}

// describeLocked lists each target's cost, heaviest first, with the interval
// it runs at (or would run at, given a plan).
func (b *budgetController) describeLocked(plan map[string]int) string {
	keys := make([]string, 0, len(b.targets))
	for key := range b.targets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return b.targets[keys[i]].pointsPerMinute() > b.targets[keys[j]].pointsPerMinute()
	})
	var sb strings.Builder
	for _, key := range keys {
		t := b.targets[key]
		mult := t.mult
		if plan != nil {
			mult = plan[key]
		}
		line := fmt.Sprintf("\n  %s: %d series, %.0f points/min at every %s", key, t.series, t.pointsPerMinute(), t.base*time.Duration(t.mult))
		if mult != t.mult {
			line += fmt.Sprintf(" -> every %s", t.base*time.Duration(mult))
		}
		sb.WriteString(line)
	}
	return sb.String()
}
