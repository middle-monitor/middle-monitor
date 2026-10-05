package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// org7Agent is the shape seen in prod: one heavy exporter, one light one.
func org7Agent() (*budgetController, *[]string) {
	b := newBudgetController()
	var logs []string
	b.logf = func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) }
	b.register("dns_exporter", 15*time.Second)
	b.observe("dns_exporter", 1853) // 7412 points/min
	b.register("node", 15*time.Second)
	b.observe("node", 200) // 800 points/min
	return b, &logs
}

// Over the limit, the agent must slow its heaviest target until its share fits,
// and leave the light one at its configured rate.
func TestBudgetSlowsTheHeaviestTargetFirst(t *testing.T) {
	b, logs := org7Agent()
	b.update(ingestStatus{PointsPerMinuteLimit: 10000, OrgPointsLastMinute: 22000, Enforced: true})

	if got := b.interval("dns_exporter", 15*time.Second); got != time.Minute {
		t.Errorf("dns_exporter every %s, want 1m0s (4 times slower)", got)
	}
	if got := b.interval("node", 15*time.Second); got != 15*time.Second {
		t.Errorf("node every %s, want its configured 15s", got)
	}
	// 8212 points/min scaled by 10000/22000 is about 3733: what is left must fit.
	if own := b.ownPointsLocked(); own > 3733 {
		t.Errorf("agent still sends %.0f points/min, over its share", own)
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "Scraping less often") || !strings.Contains((*logs)[0], "dns_exporter") {
		t.Errorf("the change must be logged with the target named: %v", *logs)
	}
}

// While the limit is only observed, nothing may change: the points are still
// stored. The agent says what it would do instead.
func TestBudgetOnlyDescribesWhileObserved(t *testing.T) {
	b, logs := org7Agent()
	b.update(ingestStatus{PointsPerMinuteLimit: 10000, OrgPointsLastMinute: 22000})

	if got := b.interval("dns_exporter", 15*time.Second); got != 15*time.Second {
		t.Errorf("dns_exporter every %s, want unchanged while observed", got)
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "not enforced yet") || !strings.Contains((*logs)[0], "-> every 1m0s") {
		t.Errorf("want the would-be change logged: %v", *logs)
	}
	b.update(ingestStatus{PointsPerMinuteLimit: 10000, OrgPointsLastMinute: 22000})
	if len(*logs) != 1 {
		t.Errorf("the observed note is logged at most every %s, got %d lines", observedLogEvery, len(*logs))
	}
}

// Back under the limit the interval returns one step at a time, and not while
// the organization is close to the limit, which would undo the change next minute.
func TestBudgetSpeedsBackUpWithHysteresis(t *testing.T) {
	b, _ := org7Agent()
	b.update(ingestStatus{PointsPerMinuteLimit: 10000, OrgPointsLastMinute: 22000, Enforced: true})

	b.update(ingestStatus{PointsPerMinuteLimit: 10000, OrgPointsLastMinute: 9000, Enforced: true})
	if got := b.interval("dns_exporter", 15*time.Second); got != time.Minute {
		t.Errorf("at 90%% of the limit nothing changes, dns_exporter every %s", got)
	}
	b.update(ingestStatus{PointsPerMinuteLimit: 10000, OrgPointsLastMinute: 5000, Enforced: true})
	if got := b.interval("dns_exporter", 15*time.Second); got != 30*time.Second {
		t.Errorf("one step back, dns_exporter every %s, want 30s", got)
	}
}

func TestBudgetNeverSlowsATargetMoreThanEightTimes(t *testing.T) {
	b, _ := org7Agent()
	b.update(ingestStatus{PointsPerMinuteLimit: 100, OrgPointsLastMinute: 1000000, Enforced: true})
	if got := b.interval("dns_exporter", 15*time.Second); got != 2*time.Minute {
		t.Errorf("dns_exporter every %s, want the 8x cap (2m0s)", got)
	}
}

func TestBudgetRestoresIntervalsWhenUnlimited(t *testing.T) {
	b, logs := org7Agent()
	b.update(ingestStatus{PointsPerMinuteLimit: 10000, OrgPointsLastMinute: 22000, Enforced: true})
	b.update(ingestStatus{PointsPerMinuteLimit: -1})
	if got := b.interval("dns_exporter", 15*time.Second); got != 15*time.Second {
		t.Errorf("dns_exporter every %s, want back to 15s", got)
	}
	if last := (*logs)[len(*logs)-1]; !strings.Contains(last, "back to their configured interval") {
		t.Errorf("restoring must be logged: %s", last)
	}
}

// The platform's answer to the metrics call is the agent's only view of the
// organization's usage; it must reach the controller.
func TestSendMetricsFeedsTheBudget(t *testing.T) {
	previous := scrapeBudget
	t.Cleanup(func() { scrapeBudget = previous })
	scrapeBudget, _ = org7Agent()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"status":"accepted","ingest":{"points_per_minute_limit":10000,"org_points_last_minute":22000,"points_per_host":1000,"enforced":true}}`))
	}))
	defer srv.Close()
	cfg := &AgentConfig{Interval: 60}
	cfg.API.URL = srv.URL

	if err := sendMetrics(cfg, &AgentMetrics{Hostname: "web-01"}); err != nil {
		t.Fatal(err)
	}
	if got := scrapeBudget.interval("dns_exporter", 15*time.Second); got != time.Minute {
		t.Errorf("dns_exporter every %s after the answer, want 1m0s", got)
	}
}
