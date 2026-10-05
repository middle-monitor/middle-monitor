package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"middle-monitor/backend/services"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

var ingestBudget *services.IngestBudget

// limitsDocURL is where the rejection message sends the user.
const limitsDocURL = "https://middlemonitor.io/docs#limits"

// ingestBudgetEnforced reports whether points over the budget are rejected. Off,
// they are stored and reported, so organizations already over the limit can get
// under it before it bites.
func ingestBudgetEnforced() bool {
	return os.Getenv("INGEST_BUDGET_ENFORCE") == "true"
}

// budgetResult is what the budget did to one request.
type budgetResult struct {
	body     []byte
	granted  int // points charged to the budget, refunded if not stored
	over     int // points past the budget
	rejected int // points removed from the request: over, once enforced
	limit    int
}

// applyIngestBudget trims a metrics request to what the organization may still
// store this minute. Within budget the body is returned untouched.
func applyIngestBudget(body []byte, orgID int64) (budgetResult, error) {
	if ingestBudget == nil || orgID <= 0 {
		return budgetResult{body: body, limit: -1}, nil
	}
	req := &colmetricspb.ExportMetricsServiceRequest{}
	if err := proto.Unmarshal(body, req); err != nil {
		return budgetResult{}, services.ErrOTLPDecode
	}
	n := services.CountMetricPoints(req)
	granted, limit := ingestBudget.Take(orgID, n)
	if granted == n {
		return budgetResult{body: body, granted: granted, limit: limit}, nil
	}
	if !ingestBudgetEnforced() {
		warnOverBudget(orgID, n-granted, limit, false)
		return budgetResult{body: body, granted: granted, over: n - granted, limit: limit}, nil
	}
	services.TrimMetricPoints(req, granted)
	// A histogram point costing 2 is dropped whole when 1 point is left.
	kept := services.CountMetricPoints(req)
	ingestBudget.Refund(orgID, granted-kept)
	trimmed, err := proto.Marshal(req)
	if err != nil {
		ingestBudget.Refund(orgID, kept)
		return budgetResult{}, ErrOTLPEncode
	}
	warnOverBudget(orgID, n-kept, limit, true)
	return budgetResult{body: trimmed, granted: kept, over: n - kept, rejected: n - kept, limit: limit}, nil
}

// rejectionMessage is shown verbatim in the agent's and the SDKs' logs, so it
// names the limit, the cause and what to do.
func rejectionMessage(rejected, limit int) string {
	return fmt.Sprintf("%d points rejected: this organization is over its ingestion limit of %d points per minute "+
		"(%d per host of its plan). Drop unused series with drop_metrics, scrape less often, or upgrade the plan. See %s",
		rejected, limit, services.PointsPerMinutePerHost, limitsDocURL)
}

// overLimitWarning is the message while the limit is observed and not enforced:
// nothing is lost yet, and the user learns it before anything is.
func overLimitWarning(over, limit int) string {
	return fmt.Sprintf("%d points this minute are over this organization's ingestion limit of %d points per minute "+
		"(%d per host of its plan). They are still stored for now and will be rejected once the limit is enforced. "+
		"Drop unused series with drop_metrics, scrape less often, or upgrade the plan. See %s",
		over, limit, services.PointsPerMinutePerHost, limitsDocURL)
}

// writePartialSuccess answers 200 with the OTLP partial_success field: the
// accepted points are stored, and exporters log the message without retrying,
// which a 429 would have made them do for nothing. With nothing rejected the
// field carries a warning, which OTLP exporters log all the same.
func writePartialSuccess(w http.ResponseWriter, res budgetResult) {
	msg := rejectionMessage(res.rejected, res.limit)
	if res.rejected == 0 {
		msg = overLimitWarning(res.over, res.limit)
	}
	resp := &colmetricspb.ExportMetricsServiceResponse{PartialSuccess: &colmetricspb.ExportMetricsPartialSuccess{
		RejectedDataPoints: int64(res.rejected),
		ErrorMessage:       msg,
	}}
	out, err := proto.Marshal(resp)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	w.Write(out)
}

var (
	budgetWarnMu sync.Mutex
	budgetWarned = map[int64]int64{}
)

// warnOverBudget logs an organization going over budget once per minute, not
// once per rejected request.
func warnOverBudget(orgID int64, over, limit int, enforced bool) {
	minute := time.Now().Unix() / 60
	budgetWarnMu.Lock()
	defer budgetWarnMu.Unlock()
	if budgetWarned[orgID] == minute {
		return
	}
	budgetWarned[orgID] = minute
	slog.Warn("organization over ingestion budget", "org_id", orgID, "limit_per_minute", limit, "over", over, "enforced", enforced)
}

// agentIngestStatus tells an agent where its organization stands against the
// metric budget, so it can lengthen its heaviest scrapes before points are
// rejected instead of losing them.
type agentIngestStatus struct {
	PointsPerMinuteLimit int  `json:"points_per_minute_limit"` // -1 = unlimited
	OrgPointsLastMinute  int  `json:"org_points_last_minute"`
	PointsPerHost        int  `json:"points_per_host"`
	Enforced             bool `json:"enforced"`
}

// respondAgentAccepted answers an agent's metrics with its budget status. Agents
// before this change ignore a 2xx body, so it stays compatible.
func respondAgentAccepted(w http.ResponseWriter, status int, orgID int64) {
	limit, last := ingestBudget.Status(orgID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "accepted",
		"ingest": agentIngestStatus{
			PointsPerMinuteLimit: limit,
			OrgPointsLastMinute:  last,
			PointsPerHost:        services.PointsPerMinutePerHost,
			Enforced:             ingestBudgetEnforced(),
		},
	})
}
