package services

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// The page shows the heaviest target first with its interval and bucket share:
// those three numbers are what the suggested change is computed from.
func TestParseHostIngestCost(t *testing.T) {
	raw := map[string]json.RawMessage{
		"targets": json.RawMessage(`{"buckets":[
			{"key":"scrape_target=node","doc_count":2000,"series":{"value":200},"scrapes":{"value":10},"first":{"value":0},"last":{"value":540000},"buckets":{"doc_count":0},"top":{"buckets":[]}},
			{"key":"scrape_target=dns_exporter","doc_count":74120,"series":{"value":1853},"scrapes":{"value":40},"first":{"value":0},"last":{"value":585000},"buckets":{"doc_count":68000},
			 "top":{"buckets":[{"key":"dnsexp_responsetime_seconds_bucket","doc_count":68000}]}}
		]}`),
		"unscraped": json.RawMessage(`{"doc_count":300,"series":{"value":30},"scrapes":{"value":10},"first":{"value":0},"last":{"value":540000},"buckets":{"doc_count":0},"top":{"buckets":[]}}`),
	}

	cost, err := parseHostIngestCost(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cost.PointsPerMinute != 7412+200+30 {
		t.Errorf("host total %d, want 7642", cost.PointsPerMinute)
	}
	dns := cost.Targets[0]
	if dns.Name != "dns_exporter" || dns.PointsPerMinute != 7412 || dns.ScrapeIntervalSeconds != 15 || dns.BucketPointsPerMinute != 6800 {
		t.Errorf("heaviest target: %+v", dns)
	}
	if len(dns.TopMetrics) != 1 || dns.TopMetrics[0].PointsPerMinute != 6800 {
		t.Errorf("top metrics: %+v", dns.TopMetrics)
	}
	if cost.Targets[1].Name != "node" || cost.Targets[1].ScrapeIntervalSeconds != 60 {
		t.Errorf("second target: %+v", cost.Targets[1])
	}
	// The agent's own system metrics carry no scrape_target and must still count.
	if last := cost.Targets[2]; last.Name != "" || last.PointsPerMinute != 30 {
		t.Errorf("system metrics: %+v", last)
	}
}

// A target added three minutes ago must show its real interval and rate, not
// be diluted over the ten-minute window: it is the one the user just changed.
func TestParseHostIngestCostRecentTarget(t *testing.T) {
	raw := map[string]json.RawMessage{
		"targets": json.RawMessage(`{"buckets":[
			{"key":"scrape_target=fresh","doc_count":1800,"series":{"value":150},"scrapes":{"value":12},"first":{"value":0},"last":{"value":165000},"buckets":{"doc_count":0},"top":{"buckets":[]}}
		]}`),
	}

	cost, err := parseHostIngestCost(raw)
	if err != nil {
		t.Fatal(err)
	}
	if fresh := cost.Targets[0]; fresh.ScrapeIntervalSeconds != 15 || fresh.PointsPerMinute != 600 {
		t.Errorf("recent target: %+v", fresh)
	}
}

func TestHostIngestCostLive(t *testing.T) {
	s := newLiveService(t)
	end := time.Now().UTC().Truncate(time.Second)
	host := fmt.Sprintf("it-cost-%d", end.UnixNano())
	for scrape := 0; scrape < 4; scrape++ {
		ts := end.Add(-time.Duration(scrape) * time.Minute)
		for i := 0; i < 50; i++ {
			name := "dns_responsetime_seconds_bucket"
			target := "dns_exporter"
			if i >= 40 {
				name, target = "node_load1", "node"
			}
			if err := s.IndexSeriesPoint(SeriesPoint{Timestamp: ts, OrganizationID: 1, MetricName: name, MetricType: "gauge",
				Value: 1, Labels: map[string]string{"scrape_target": target, "le": fmt.Sprint(i)}, Hostname: host}); err != nil {
				t.Fatal(err)
			}
		}
	}
	refreshSeries(t, s)

	cost, err := s.HostIngestCost(context.Background(), 1, host)
	if err != nil {
		t.Fatal(err)
	}
	if len(cost.Targets) != 2 || cost.Targets[0].Name != "dns_exporter" || cost.Targets[0].Series != 40 {
		t.Fatalf("got %+v", cost.Targets)
	}
	if cost.Targets[0].BucketPointsPerMinute != cost.Targets[0].PointsPerMinute {
		t.Errorf("every dns point is a bucket: %+v", cost.Targets[0])
	}
	// Four scrapes one minute apart cover four minutes, not the ten-minute window.
	if dns := cost.Targets[0]; dns.ScrapeIntervalSeconds != 60 || dns.PointsPerMinute != 40 {
		t.Errorf("dns interval/rate: %+v", dns)
	}
}
