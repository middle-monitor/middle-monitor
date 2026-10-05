package api

import (
	"strings"
	"testing"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

// The rules report exists to give operators an immediate answer. It must state
// the cause in one bold sentence and add at most ONE correlation line — never
// "no clear cause" boilerplate, evidence dumps, or generic recommended actions.

func assertMinimalReport(t *testing.T, report string) {
	t.Helper()
	for _, forbidden := range []string{
		"No clear cause", "Aucune cause claire",
		"## ", "Recommended actions", "Actions recommandées",
		"Evidence", "Évidence",
	} {
		if strings.Contains(report, forbidden) {
			t.Errorf("report must not contain %q, got:\n%s", forbidden, report)
		}
	}
	if paragraphs := strings.Split(report, "\n\n"); len(paragraphs) > 2 {
		t.Errorf("report must be at most cause + one correlation line, got %d paragraphs:\n%s", len(paragraphs), report)
	}
}

func TestRenderRulesReportExpiredTokenStatesTheObviousCause(t *testing.T) {
	ctxMap := map[string]any{
		"error": slimError{
			Name:    "AuthError",
			Message: "JWT token expired",
			File:    "auth.rs",
			Line:    47,
			Service: "test-rust-sdk",
		},
		"correlation": &slimCorrelation{RecurrenceIsNew: true},
	}

	report := renderRulesReport("error", ctxMap, "en")

	if !strings.Contains(report, "**The authentication token is expired") {
		t.Errorf("expected the expired-token cause as the bold headline, got:\n%s", report)
	}
	if !strings.Contains(report, "test-rust-sdk / auth.rs:47") {
		t.Errorf("expected the scope after the cause, got:\n%s", report)
	}
	if !strings.Contains(report, "First occurrence") {
		t.Errorf("expected the new-error regression hint as the correlation line, got:\n%s", report)
	}
	assertMinimalReport(t, report)
}

func TestRenderRulesReportUnknownErrorFallsBackToTheMessage(t *testing.T) {
	ctxMap := map[string]any{
		"error": slimError{
			Name:    "WeirdError",
			Message: "something completely unrecognized happened",
			Service: "svc",
		},
	}

	report := renderRulesReport("error", ctxMap, "en")

	if !strings.Contains(report, "**WeirdError: something completely unrecognized happened.**") {
		t.Errorf("expected the raw message stated as the answer, got:\n%s", report)
	}
	assertMinimalReport(t, report)
}

func TestRenderRulesReportPingFailureNamesUpstreamCorrelation(t *testing.T) {
	ctxMap := map[string]any{
		"service_result": slimServiceResult{
			Service:     "nextcloud-server",
			Status:      "fail",
			CheckType:   "ping",
			CheckTarget: "files.example.com",
			Message:     "Ping failed: exit status 1",
		},
		"correlation": &slimCorrelation{
			HasCorrelation:    true,
			FailingNeighbours: []string{"api-health"},
			UpstreamSuspects:  []string{"api-health"},
		},
	}

	report := renderRulesReport("service_result", ctxMap, "fr")

	if !strings.Contains(report, "**files.example.com ne répond pas au ping") {
		t.Errorf("expected a human ping cause as the bold headline, got:\n%s", report)
	}
	if !strings.Contains(report, "Corrélation possible : api-health a échoué juste avant") {
		t.Errorf("expected exactly one upstream correlation line naming api-health, got:\n%s", report)
	}
	// The neighbour list duplicates the upstream suspect: it must NOT produce a
	// second correlation sentence.
	if strings.Count(report, "api-health") != 1 {
		t.Errorf("api-health must be mentioned exactly once, got:\n%s", report)
	}
	assertMinimalReport(t, report)
}

// A transfer saturating one host slows the checks probing its neighbours long
// before it breaks them. Reporting "no abnormal traffic" while a sibling check
// runs 30x slower sends the operator hunting on the wrong machine.
func TestAgentCPUSpikeNamesTheDegradedNeighbourRatherThanDenyingTraffic(t *testing.T) {
	ctxMap := map[string]any{
		"service_result": slimServiceResult{
			Service:     "cpu",
			Status:      "fail",
			CheckType:   "agent_cpu",
			CheckTarget: "feedly",
			Message:     "CPU usage is 94.20%",
		},
		"correlation": &slimCorrelation{
			HasCorrelation:     true,
			DegradedNeighbours: []string{"nextcloud-http (nextcloud) 2400ms vs 80ms baseline (x30.0)"},
		},
	}

	report := deterministicKnownExplanation("service_result", ctxMap, "fr")

	if strings.Contains(report, "aucun trafic anormal") {
		t.Errorf("must not deny abnormal traffic while a neighbour is degraded, got:\n%s", report)
	}
	if !strings.Contains(report, "nextcloud-http") {
		t.Errorf("expected the degraded neighbour named in the headline, got:\n%s", report)
	}
	if !strings.Contains(report, "94.20%") || !strings.Contains(report, "feedly") {
		t.Errorf("expected the CPU value and host to survive, got:\n%s", report)
	}
	assertMinimalReport(t, report)
}

// Without a degraded neighbour the original wording must stand: the fix adds a
// branch, it does not rewrite the no-signal answer.
func TestAgentCPUSpikeKeepsNoTrafficWordingWhenNeighboursAreHealthy(t *testing.T) {
	ctxMap := map[string]any{
		"service_result": slimServiceResult{
			Service:     "cpu",
			Status:      "fail",
			CheckType:   "agent_cpu",
			CheckTarget: "feedly",
			Message:     "CPU usage is 94.20%",
		},
		"correlation": &slimCorrelation{HasCorrelation: false},
	}

	report := deterministicKnownExplanation("service_result", ctxMap, "fr")

	if !strings.Contains(report, "aucun trafic anormal") {
		t.Errorf("expected the unchanged no-signal wording, got:\n%s", report)
	}
}

// The generic correlation line must surface latency degradation too, otherwise
// a slowdown with no outright failure produces a report with no correlation.
func TestCorrelationLineReportsDegradedNeighbours(t *testing.T) {
	ctxMap := map[string]any{
		"correlation": &slimCorrelation{
			HasCorrelation:     true,
			DegradedNeighbours: []string{"nextcloud-ping (nextcloud) 310ms vs 12ms baseline (x25.8)"},
		},
	}

	line := correlationLine(ctxMap, false)

	if !strings.Contains(line, "nextcloud-ping") || !strings.Contains(line, "a ralenti au même moment") {
		t.Errorf("expected a degradation correlation line naming the neighbour, got:\n%s", line)
	}
}

// A hard failure is stronger evidence than a slowdown: when both exist, the
// single correlation line must name the failure.
func TestCorrelationLineFavoursFailureOverDegradation(t *testing.T) {
	ctxMap := map[string]any{
		"correlation": &slimCorrelation{
			HasCorrelation:     true,
			FailingNeighbours:  []string{"api-health"},
			DegradedNeighbours: []string{"nextcloud-ping (nextcloud) 310ms vs 12ms baseline (x25.8)"},
		},
	}

	line := correlationLine(ctxMap, false)

	if !strings.Contains(line, "api-health") {
		t.Errorf("expected the failing neighbour to win the single correlation line, got:\n%s", line)
	}
	if strings.Contains(line, "nextcloud-ping") {
		t.Errorf("expected only one correlation signal in the line, got:\n%s", line)
	}
}

// Infra saturation and a traffic spike in the same window are one story, not two
// rival candidates. Ranked separately, the traffic half (0.78) simply outranks
// the infra half (0.70) and the causal link never gets stated.
func TestSaturationUnderLoadMergesInfraAndTraffic(t *testing.T) {
	ctxMap := map[string]any{
		"error": slimError{Name: "TimeoutError", Message: "upstream timed out", Service: "api"},
		"correlation": &slimCorrelation{
			HasCorrelation: true,
			InfraSignals:   []string{"Host CPU peaked at 97.0% during the incident window (threshold 90%)"},
		},
		"traffic_anomalies": &services.TrafficAnalysis{
			WindowMinutes: 20,
			Signals: []services.TrafficAnomalySignal{
				{Service: "api", TraceCount: 4200, TraceMultiplier: 6.1},
			},
		},
	}

	dossier := buildRCADossier("error", ctxMap, "en")
	if dossier == nil {
		t.Fatal("expected a dossier")
	}
	if indexOfKind(dossier.Candidates, "infra") >= 0 || indexOfKind(dossier.Candidates, "traffic") >= 0 {
		t.Fatalf("infra and traffic must not survive as separate candidates, got %+v", dossier.Candidates)
	}
	merged := indexOfKind(dossier.Candidates, "saturation")
	if merged < 0 {
		t.Fatalf("expected a merged saturation candidate, got %+v", dossier.Candidates)
	}
	if merged != 0 {
		t.Fatalf("the merged candidate must outrank the isolated ones, got rank %d", merged)
	}
	if got := dossier.Candidates[merged].Confidence; got <= 0.78 {
		t.Fatalf("merged confidence %v must beat either half alone (0.70 infra, 0.78 traffic)", got)
	}
	// Both halves must remain visible as evidence, otherwise the operator cannot
	// check the claim.
	evidence := strings.Join(dossier.Candidates[merged].Evidence, " | ")
	if !strings.Contains(evidence, "CPU peaked") || !strings.Contains(evidence, "api") {
		t.Fatalf("merged evidence must keep both the infra and the traffic proof, got %q", evidence)
	}
}

// A single signal is not a correlation: infra alone must stay an infra candidate
// at its own confidence, with no invented "under load" story.
func TestInfraAloneIsNotPromotedToSaturation(t *testing.T) {
	ctxMap := map[string]any{
		"error": slimError{Name: "TimeoutError", Message: "upstream timed out", Service: "api"},
		"correlation": &slimCorrelation{
			HasCorrelation: true,
			InfraSignals:   []string{"Host CPU peaked at 97.0% during the incident window (threshold 90%)"},
		},
	}

	dossier := buildRCADossier("error", ctxMap, "en")
	if dossier == nil {
		t.Fatal("expected a dossier")
	}
	if indexOfKind(dossier.Candidates, "saturation") >= 0 {
		t.Fatalf("no traffic signal means no saturation-under-load claim, got %+v", dossier.Candidates)
	}
	infra := indexOfKind(dossier.Candidates, "infra")
	if infra < 0 || dossier.Candidates[infra].Confidence != 0.7 {
		t.Fatalf("expected the infra candidate at its own 0.7 confidence, got %+v", dossier.Candidates)
	}
}

// Degraded neighbours were computed and dropped before reaching the dossier.
// A neighbour whose latency triples while traffic spikes is the same saturation
// story as a CPU peak, so it must merge the same way.
func TestDegradedNeighbourCountsAsStrainUnderLoad(t *testing.T) {
	ctxMap := map[string]any{
		"error": slimError{Name: "TimeoutError", Message: "upstream timed out", Service: "api"},
		"correlation": &slimCorrelation{
			HasCorrelation:     true,
			DegradedNeighbours: []string{"db-check (web-01) 900ms vs 300ms baseline (x3.0)"},
		},
		"traffic_anomalies": &services.TrafficAnalysis{
			WindowMinutes: 20,
			Signals:       []services.TrafficAnomalySignal{{Service: "api", TraceCount: 4200, TraceMultiplier: 6.1}},
		},
	}

	dossier := buildRCADossier("error", ctxMap, "en")
	if dossier == nil {
		t.Fatal("expected a dossier")
	}
	if indexOfKind(dossier.Candidates, "saturation") != 0 {
		t.Fatalf("expected the merged saturation candidate on top, got %+v", dossier.Candidates)
	}
	if indexOfKind(dossier.Candidates, "degraded") >= 0 || indexOfKind(dossier.Candidates, "traffic") >= 0 {
		t.Fatalf("both halves must be consumed by the merge, got %+v", dossier.Candidates)
	}
}

// A declared dependency that errored first, while traffic spiked, is one story:
// the dependency gave way under load. Not two rival candidates.
func TestUpstreamAppUnderLoadMerges(t *testing.T) {
	ctxMap := map[string]any{
		"error": slimError{Name: "TimeoutError", Message: "upstream timed out", Service: "api"},
		"correlation": &slimCorrelation{
			HasCorrelation: true,
			UpstreamApps:   []string{"billing (ConnectionPoolExhausted, x37)"},
		},
		"traffic_anomalies": &services.TrafficAnalysis{
			WindowMinutes: 20,
			Signals:       []services.TrafficAnomalySignal{{Service: "api", TraceCount: 4200, TraceMultiplier: 6.1}},
		},
	}

	dossier := buildRCADossier("error", ctxMap, "en")
	if dossier == nil {
		t.Fatal("expected a dossier")
	}
	merged := indexOfKind(dossier.Candidates, "upstream_load")
	if merged != 0 {
		t.Fatalf("expected the merged upstream-under-load candidate on top, got %+v", dossier.Candidates)
	}
	evidence := strings.Join(dossier.Candidates[merged].Evidence, " | ")
	if !strings.Contains(evidence, "billing") || !strings.Contains(evidence, "api") {
		t.Fatalf("merged evidence must keep the dependency and the traffic proof, got %q", evidence)
	}
}

// When the host is saturating AND a dependency broke, the saturation is the more
// direct explanation of the symptom: only one merge is applied, and it is that one.
func TestStrainWinsOverUpstreamWhenBothCorrelateWithLoad(t *testing.T) {
	ctxMap := map[string]any{
		"error": slimError{Name: "TimeoutError", Message: "upstream timed out", Service: "api"},
		"correlation": &slimCorrelation{
			HasCorrelation: true,
			InfraSignals:   []string{"web-01 CPU peaked at 97.0% during the incident window (threshold 90%)"},
			UpstreamApps:   []string{"billing (ConnectionPoolExhausted, x37)"},
		},
		"traffic_anomalies": &services.TrafficAnalysis{
			WindowMinutes: 20,
			Signals:       []services.TrafficAnomalySignal{{Service: "api", TraceCount: 4200, TraceMultiplier: 6.1}},
		},
	}

	dossier := buildRCADossier("error", ctxMap, "en")
	if dossier == nil {
		t.Fatal("expected a dossier")
	}
	if indexOfKind(dossier.Candidates, "saturation") != 0 {
		t.Fatalf("expected the saturation merge to win, got %+v", dossier.Candidates)
	}
	// The upstream app is not merged away — it stays available as its own lead.
	if indexOfKind(dossier.Candidates, "upstream_app") < 0 {
		t.Fatalf("the upstream dependency must survive as a separate candidate, got %+v", dossier.Candidates)
	}
}

// Only declared dependencies that broke FIRST are causes. A dependent app
// erroring after is downstream impact, and promoting it would invert the chain.
func TestTrimCorrelationKeepsOnlyPrecedingDependenciesAsUpstream(t *testing.T) {
	slim := trimCorrelation(&models.CorrelationResult{
		HasCorrelation: true,
		Apps: []models.AppCorrelation{
			{Service: "billing", ErrorName: "PoolExhausted", Count: 37, Relation: "dependency", PrecededIncident: true},
			{Service: "late-dep", ErrorName: "Boom", Count: 2, Relation: "dependency", PrecededIncident: false},
			{Service: "frontend", ErrorName: "Boom", Count: 9, Relation: "dependent", PrecededIncident: true},
		},
	})
	if len(slim.UpstreamApps) != 1 || !strings.Contains(slim.UpstreamApps[0], "billing") {
		t.Fatalf("expected only the preceding dependency, got %v", slim.UpstreamApps)
	}
}

// Ranking must follow the evidence, not the signal family. Two identical
// dossiers whose only difference is how hard the CPU was pushed past its own
// threshold must not come out with the same confidence.
func TestInfraConfidenceFollowsMeasuredStrength(t *testing.T) {
	build := func(strength float64) float64 {
		ctxMap := map[string]any{
			"error": slimError{Name: "TimeoutError", Message: "upstream timed out", Service: "api"},
			"correlation": &slimCorrelation{
				HasCorrelation: true,
				InfraSignals:   []string{"web-01 CPU peaked during the incident window"},
				infraStrength:  strength,
			},
		}
		dossier := buildRCADossier("error", ctxMap, "en")
		if dossier == nil {
			t.Fatal("expected a dossier")
		}
		i := indexOfKind(dossier.Candidates, "infra")
		if i < 0 {
			t.Fatalf("expected an infra candidate, got %+v", dossier.Candidates)
		}
		return dossier.Candidates[i].Confidence
	}

	barely, hard := build(0.62), build(0.94)
	if !(hard > barely) {
		t.Fatalf("a CPU far past its threshold (%v) must outrank one barely over it (%v)", hard, barely)
	}
	// Unmeasured strength keeps the family's base rate rather than collapsing to 0.
	if base := build(0); base != 0.7 {
		t.Fatalf("expected the 0.7 base rate when nothing was measured, got %v", base)
	}
}

// No correlation, however strong, may reach the confidence that makes the
// pipeline skip the LLM: that shortcut belongs to causes that state themselves.
func TestCorrelationCandidatesNeverReachTheDeterministicBand(t *testing.T) {
	ctxMap := map[string]any{
		"error": slimError{Name: "TimeoutError", Message: "upstream timed out", Service: "api"},
		"correlation": &slimCorrelation{
			HasCorrelation: true,
			InfraSignals:   []string{"web-01 CPU peaked at 99.9%"},
			infraStrength:  0.95,
		},
		"traffic_anomalies": &services.TrafficAnalysis{
			WindowMinutes: 20,
			Signals:       []services.TrafficAnomalySignal{{Service: "api", TraceCount: 90000, TraceMultiplier: 500, ErrorLogCount: 900}},
		},
	}

	dossier := buildRCADossier("error", ctxMap, "en")
	if dossier == nil {
		t.Fatal("expected a dossier")
	}
	for _, c := range dossier.Candidates {
		if c.Confidence > correlationConfidenceCap {
			t.Fatalf("candidate %q at %v exceeds the correlation cap %v", c.Label, c.Confidence, correlationConfidenceCap)
		}
	}
	if !shouldUseLLMForDossier(dossier) {
		t.Fatal("a composite correlation must still be narrated by the LLM")
	}
}

// Volume is not evidence: a service that always takes that much traffic must not
// outrank one that genuinely departed from its own baseline.
func TestTrafficStrengthRanksOnMultiplierNotVolume(t *testing.T) {
	busy := trafficStrength(services.TrafficAnomalySignal{TraceCount: 500000, TraceMultiplier: 3.1})
	spiking := trafficStrength(services.TrafficAnomalySignal{TraceCount: 900, TraceMultiplier: 40})
	if !(spiking > busy) {
		t.Fatalf("a x40 departure (%v) must outrank a busy-but-normal service (%v)", spiking, busy)
	}
	noBaseline := trafficStrength(services.TrafficAnomalySignal{TraceCount: 900})
	if noBaseline != 0.7 {
		t.Fatalf("with no baseline the score must stay at the neutral 0.7, got %v", noBaseline)
	}
}
