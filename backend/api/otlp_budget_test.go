package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"middle-monitor/backend/services"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// The message is what the user reads in the agent's log: it must state the
// limit, the per-host rule, what to do and where the documentation is.
func TestPartialSuccessTellsTheUserWhatToDo(t *testing.T) {
	rec := httptest.NewRecorder()
	writePartialSuccess(rec, budgetResult{over: 1240, rejected: 1240, limit: 2500})

	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/x-protobuf" {
		t.Fatalf("status %d content-type %q, want a 200 protobuf answer", rec.Code, rec.Header().Get("Content-Type"))
	}
	resp := &colmetricspb.ExportMetricsServiceResponse{}
	if err := proto.Unmarshal(rec.Body.Bytes(), resp); err != nil {
		t.Fatal(err)
	}
	ps := resp.GetPartialSuccess()
	if ps.GetRejectedDataPoints() != 1240 {
		t.Errorf("rejected %d, want 1240", ps.GetRejectedDataPoints())
	}
	for _, want := range []string{"1240 points rejected", "2500 points per minute", "250 per host", "drop_metrics", "upgrade", limitsDocURL} {
		if !strings.Contains(ps.GetErrorMessage(), want) {
			t.Errorf("message lacks %q: %s", want, ps.GetErrorMessage())
		}
	}
}

// While the limit is observed nothing is lost: the answer must say so, reject
// no point, and still carry a message, since that is what exporters log.
func TestObservedLimitWarnsWithoutRejecting(t *testing.T) {
	rec := httptest.NewRecorder()
	writePartialSuccess(rec, budgetResult{over: 12000, limit: 10000})

	resp := &colmetricspb.ExportMetricsServiceResponse{}
	if err := proto.Unmarshal(rec.Body.Bytes(), resp); err != nil {
		t.Fatal(err)
	}
	ps := resp.GetPartialSuccess()
	if ps.GetRejectedDataPoints() != 0 {
		t.Errorf("rejected %d, want 0 while observing", ps.GetRejectedDataPoints())
	}
	for _, want := range []string{"12000 points this minute are over", "10000 points per minute", "still stored", "will be rejected once", limitsDocURL} {
		if !strings.Contains(ps.GetErrorMessage(), want) {
			t.Errorf("message lacks %q: %s", want, ps.GetErrorMessage())
		}
	}
}

// Observation keeps the request whole; enforcement trims it to the budget.
func TestApplyIngestBudgetOnlyTrimsWhenEnforced(t *testing.T) {
	previous := ingestBudget
	t.Cleanup(func() { ingestBudget = previous })
	body := gaugeBody(30)

	for _, enforced := range []bool{false, true} {
		ingestBudget = services.NewTestIngestBudget(10)
		value := ""
		if enforced {
			value = "true"
		}
		t.Setenv("INGEST_BUDGET_ENFORCE", value)

		res, err := applyIngestBudget(body, 7)
		if err != nil {
			t.Fatal(err)
		}
		req := &colmetricspb.ExportMetricsServiceRequest{}
		if err := proto.Unmarshal(res.body, req); err != nil {
			t.Fatal(err)
		}
		kept := services.CountMetricPoints(req)
		if res.over != 20 {
			t.Errorf("enforced=%v: over %d, want 20", enforced, res.over)
		}
		if enforced && (kept != 10 || res.rejected != 20) {
			t.Errorf("enforced: kept %d rejected %d, want 10 and 20", kept, res.rejected)
		}
		if !enforced && (kept != 30 || res.rejected != 0) {
			t.Errorf("observed: kept %d rejected %d, want all 30 kept", kept, res.rejected)
		}
	}
}

func gaugeBody(points int) []byte {
	gauge := &metricspb.Gauge{}
	for i := 0; i < points; i++ {
		gauge.DataPoints = append(gauge.DataPoints, &metricspb.NumberDataPoint{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)}})
	}
	b, _ := proto.Marshal(&colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{
		ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: "m", Data: &metricspb.Metric_Gauge{Gauge: gauge}}}}},
	}}})
	return b
}

// The agent reads this to decide whether to lengthen its heaviest scrapes.
func TestAgentAcceptedCarriesTheBudget(t *testing.T) {
	previous := ingestBudget
	t.Cleanup(func() { ingestBudget = previous })
	ingestBudget = services.NewTestIngestBudget(2500)
	t.Setenv("INGEST_BUDGET_ENFORCE", "true")

	rec := httptest.NewRecorder()
	respondAgentAccepted(rec, 202, 7)

	var body struct {
		Status string            `json:"status"`
		Ingest agentIngestStatus `json:"ingest"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	if rec.Code != 202 || body.Ingest.PointsPerMinuteLimit != 2500 || body.Ingest.PointsPerHost != 250 || !body.Ingest.Enforced {
		t.Errorf("got %d %+v", rec.Code, body.Ingest)
	}
}
