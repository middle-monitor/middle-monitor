package api

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"middle-monitor/backend/services"
)

type rcaDossier struct {
	Symptom        string         `json:"symptom"`
	Scope          string         `json:"scope,omitempty"`
	StrongSignals  []string       `json:"strong_signals,omitempty"`
	MissingSignals []string       `json:"missing_signals,omitempty"`
	Candidates     []rcaCandidate `json:"candidate_causes,omitempty"`
}

type rcaCandidate struct {
	Label      string   `json:"label"`
	Confidence float64  `json:"confidence"`
	Evidence   []string `json:"evidence,omitempty"`
	Actions    []string `json:"actions,omitempty"`
	// kind identifies which signal family produced this candidate, so
	// reinforceCandidates can merge families that tell one story. Internal only.
	kind string
}

func analyzeErrorTraffic(ctx context.Context, analyzer *services.TrafficAnalyzer, orgID, errorID int64) *services.TrafficAnalysis {
	if analyzer == nil {
		return nil
	}
	analysis, err := analyzer.AnalyzeForError(ctx, orgID, errorID)
	if err != nil {
		slog.Error("traffic analysis failed", "error_id", errorID, "error", err)
		return nil
	}
	return analysis
}

func analyzeServiceResultTraffic(ctx context.Context, analyzer *services.TrafficAnalyzer, orgID, resultID int64) *services.TrafficAnalysis {
	if analyzer == nil {
		return nil
	}
	analysis, err := analyzer.AnalyzeForServiceResult(ctx, orgID, resultID)
	if err != nil {
		slog.Error("traffic analysis failed", "service_result_id", resultID, "error", err)
		return nil
	}
	return analysis
}

func buildRCADossier(subjectType string, rawContext any, locale string) *rcaDossier {
	locale = normalizeExplainLocale(locale)
	ctxMap, ok := rawContext.(map[string]any)
	if !ok {
		return nil
	}

	switch subjectType {
	case "error":
		errData, ok := ctxMap["error"].(slimError)
		if !ok {
			return nil
		}
		dossier := &rcaDossier{
			Symptom: fmt.Sprintf("%s: %s", errData.Name, errData.Message),
			Scope:   scopeText(errData.Service, errData.File, errData.Line),
		}
		addKnownErrorCandidate(dossier, errData, ctxMap, locale)
		addTraceCandidate(dossier, ctxMap, locale)
		addCorrelationCandidates(dossier, ctxMap, locale)
		addTrafficCandidates(dossier, ctxMap, locale)
		reinforceCandidates(dossier, locale)
		addMissingSignals(dossier, subjectType, ctxMap, locale)
		return nonEmptyDossier(dossier)
	case "service_result":
		result, ok := ctxMap["service_result"].(slimServiceResult)
		if !ok {
			return nil
		}
		dossier := &rcaDossier{
			Symptom: fmt.Sprintf("%s %s: %s", result.CheckType, result.CheckTarget, result.Message),
			Scope:   scopeText(result.Service, result.CheckTarget, 0),
		}
		addServiceResultCandidate(dossier, result, locale)
		addCorrelationCandidates(dossier, ctxMap, locale)
		addTrafficCandidates(dossier, ctxMap, locale)
		reinforceCandidates(dossier, locale)
		addMissingSignals(dossier, subjectType, ctxMap, locale)
		return nonEmptyDossier(dossier)
	default:
		return nil
	}
}

func shouldUseLLMForDossier(d *rcaDossier) bool {
	if d == nil || len(d.Candidates) == 0 {
		return false
	}
	top := d.Candidates[0]
	if top.Confidence >= 0.9 {
		return false
	}
	return len(d.StrongSignals) >= 2 || len(d.Candidates) >= 2
}

func nonEmptyDossier(d *rcaDossier) *rcaDossier {
	if d == nil || (len(d.StrongSignals) == 0 && len(d.Candidates) == 0) {
		return nil
	}
	sortCandidates(d.Candidates)
	return d
}

func addKnownErrorCandidate(d *rcaDossier, errData slimError, ctxMap map[string]any, locale string) {
	hint := knownErrorHint(errData, locale)
	if strings.TrimSpace(hint) != "" {
		d.StrongSignals = append(d.StrongSignals, hint)
		action := "Traiter directement la cause indiquée par le message d'erreur."
		if locale == "en" {
			action = "Address the cause indicated by the error message directly."
		}
		d.Candidates = append(d.Candidates, rcaCandidate{
			Label:      hint,
			Confidence: 0.95,
			Evidence:   []string{errData.Message, scopeText(errData.Service, errData.File, errData.Line)},
			Actions:    []string{action},
			kind:       "known",
		})
	}
}

func addServiceResultCandidate(d *rcaDossier, result slimServiceResult, locale string) {
	label, actions := knownServiceResultCause(result, locale)
	if label == "" {
		return
	}
	d.StrongSignals = append(d.StrongSignals, label)
	d.Candidates = append(d.Candidates, rcaCandidate{
		Label:      label,
		Confidence: 0.9,
		Evidence:   []string{result.Message},
		Actions:    actions,
		kind:       "known",
	})
}

// knownServiceResultCause maps well-known check failure messages to a concise
// human cause and concrete actions. An empty label means "not recognized".
func knownServiceResultCause(result slimServiceResult, locale string) (label string, actions []string) {
	msg := strings.ToLower(result.Message)
	target := result.CheckTarget
	if target == "" {
		target = result.Service
	}
	switch {
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "dns"):
		if locale == "en" {
			label = fmt.Sprintf("DNS cannot resolve %s", target)
			actions = []string{"Check the domain and DNS resolution from the host running the check."}
		} else {
			label = fmt.Sprintf("DNS introuvable pour %s", target)
			actions = []string{"Vérifier le domaine et la résolution DNS depuis le host qui exécute le check."}
		}
	case strings.Contains(msg, "connection refused"):
		if locale == "en" {
			label = fmt.Sprintf("Connection refused by %s", target)
			actions = []string{"Check that the service is listening on the target and that the port is open."}
		} else {
			label = fmt.Sprintf("Connexion refusée par %s", target)
			actions = []string{"Vérifier que le service écoute sur la cible et que le port est ouvert."}
		}
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
		if locale == "en" {
			label = fmt.Sprintf("%s does not respond in time", target)
			actions = []string{"Compare traffic volume and latency for dependent services in the same window."}
		} else {
			label = fmt.Sprintf("%s ne répond pas à temps", target)
			actions = []string{"Comparer le volume de trafic et la latence des services dépendants dans la même fenêtre."}
		}
	case strings.Contains(msg, "certificate") || strings.Contains(msg, "x509"):
		if locale == "en" {
			label = fmt.Sprintf("TLS/certificate issue on %s", target)
			actions = []string{"Check certificate expiration and chain."}
		} else {
			label = fmt.Sprintf("Problème TLS/certificat sur %s", target)
			actions = []string{"Vérifier l'expiration et la chaîne de certificat."}
		}
	// Latency-threshold warnings are not "host down": let them fall through so
	// the raw message (which is accurate) is used instead.
	case (strings.Contains(msg, "ping failed") || result.CheckType == "ping" || result.CheckType == "icmp") && !strings.Contains(msg, "latency"):
		if locale == "en" {
			label = fmt.Sprintf("%s does not answer ping (host down or ICMP blocked)", target)
			actions = []string{"Check that the host is up and reachable (power, network, firewall allowing ICMP)."}
		} else {
			label = fmt.Sprintf("%s ne répond pas au ping (host down ou ICMP bloqué)", target)
			actions = []string{"Vérifier que le host est démarré et joignable (alimentation, réseau, ICMP autorisé)."}
		}
	}
	return label, actions
}

// addTraceCandidate turns a distributed trace's failing span into the strongest
// possible candidate: it pinpoints the exact service/operation that broke.
func addTraceCandidate(d *rcaDossier, ctxMap map[string]any, locale string) {
	trace, _ := ctxMap["trace"].(*services.TraceSummary)
	if trace == nil || len(trace.ErrorSpans) == 0 {
		return
	}
	span := trace.ErrorSpans[0]
	where := span.ServiceName
	if span.Operation != "" {
		where = fmt.Sprintf("%s / %s", span.ServiceName, span.Operation)
	}
	var label, signal, action string
	if locale == "en" {
		label = fmt.Sprintf("Failure originates in %s", where)
		signal = fmt.Sprintf("Trace %s: failing span on %s (status=%s%s)", trace.TraceID, where, span.StatusCode, optStatusMsg(span.StatusMsg, " — "))
		action = fmt.Sprintf("Inspect the failing operation %s; it is where the request actually broke in the call chain.", where)
	} else {
		label = fmt.Sprintf("La défaillance provient de %s", where)
		signal = fmt.Sprintf("Trace %s : span en échec sur %s (status=%s%s)", trace.TraceID, where, span.StatusCode, optStatusMsg(span.StatusMsg, " — "))
		action = fmt.Sprintf("Inspecter l'opération en échec %s : c'est là que la requête a réellement cassé dans la chaîne d'appel.", where)
	}
	d.StrongSignals = append(d.StrongSignals, signal)
	d.Candidates = append(d.Candidates, rcaCandidate{
		Label:      label,
		Confidence: 0.92,
		Evidence:   []string{signal},
		Actions:    []string{action},
		kind:       "trace",
	})
}

func optStatusMsg(msg, sep string) string {
	if strings.TrimSpace(msg) == "" {
		return ""
	}
	return sep + msg
}

func addCorrelationCandidates(d *rcaDossier, ctxMap map[string]any, locale string) {
	correlation, _ := ctxMap["correlation"].(*slimCorrelation)
	if correlation == nil {
		return
	}
	// Recurrence is meaningful even without infra/service correlation: a brand
	// new error right after a deploy is a strong regression signal.
	if correlation.Recurrence != "" {
		d.StrongSignals = append(d.StrongSignals, correlation.Recurrence)
	}
	if !correlation.HasCorrelation {
		return
	}
	d.StrongSignals = append(d.StrongSignals, correlation.InfraSignals...)
	if len(correlation.UpstreamSuspects) > 0 {
		signal := fmt.Sprintf("Services ayant échoué AVANT l'incident (cause amont probable) : %s", strings.Join(correlation.UpstreamSuspects, ", "))
		label := fmt.Sprintf("Cause amont : %s", strings.Join(correlation.UpstreamSuspects, ", "))
		action := "Investiguer en priorité le service qui a défailli en premier."
		if locale == "en" {
			signal = fmt.Sprintf("Services that failed BEFORE the incident (likely upstream cause): %s", strings.Join(correlation.UpstreamSuspects, ", "))
			label = fmt.Sprintf("Upstream cause: %s", strings.Join(correlation.UpstreamSuspects, ", "))
			action = "Investigate the service that failed first as the priority."
		}
		d.StrongSignals = append(d.StrongSignals, signal)
		d.Candidates = append(d.Candidates, rcaCandidate{
			Label:      label,
			Confidence: evidenceConfidence(correlation.upstreamStrength, 0.8),
			Evidence:   []string{signal},
			Actions:    []string{action},
			kind:       "upstream",
		})
	}
	if len(correlation.FailingNeighbours) > 0 {
		signal := fmt.Sprintf("Services voisins en échec : %s", strings.Join(correlation.FailingNeighbours, ", "))
		label := "Incident partagé sur une dépendance ou un host commun"
		action := "Identifier la dépendance commune entre les services impactés."
		if locale == "en" {
			signal = fmt.Sprintf("Neighbouring services failing: %s", strings.Join(correlation.FailingNeighbours, ", "))
			label = "Shared incident on a common dependency or host"
			action = "Identify the common dependency between impacted services."
		}
		d.StrongSignals = append(d.StrongSignals, signal)
		d.Candidates = append(d.Candidates, rcaCandidate{
			Label:      label,
			Confidence: evidenceConfidence(correlation.neighbourStrength, 0.75),
			Evidence:   []string{signal},
			Actions:    []string{action},
			kind:       "neighbours",
		})
	}
	if len(correlation.UpstreamApps) > 0 {
		signal := fmt.Sprintf("Dépendances déclarées en erreur AVANT l'incident : %s", strings.Join(correlation.UpstreamApps, ", "))
		label := fmt.Sprintf("Cause amont applicative : %s", strings.Join(correlation.UpstreamApps, ", "))
		action := "Remonter la chaîne : la dépendance a cassé la première, l'erreur observée en est la conséquence."
		if locale == "en" {
			signal = fmt.Sprintf("Declared dependencies erroring BEFORE the incident: %s", strings.Join(correlation.UpstreamApps, ", "))
			label = fmt.Sprintf("Upstream application cause: %s", strings.Join(correlation.UpstreamApps, ", "))
			action = "Walk up the chain: the dependency broke first, the observed error is its consequence."
		}
		d.StrongSignals = append(d.StrongSignals, signal)
		d.Candidates = append(d.Candidates, rcaCandidate{
			Label:      label,
			Confidence: evidenceConfidence(correlation.upstreamAppStrength, 0.8),
			Evidence:   []string{signal},
			Actions:    []string{action},
			kind:       "upstream_app",
		})
	}
	if len(correlation.DegradedNeighbours) > 0 {
		signal := fmt.Sprintf("Services voisins ralentis sans échouer : %s", strings.Join(correlation.DegradedNeighbours, ", "))
		label := "Ralentissement partagé sur le host"
		action := "Chercher la ressource commune saturée : un voisin qui ralentit sans casser trahit une contention, pas une panne."
		if locale == "en" {
			signal = fmt.Sprintf("Neighbouring services slowed down without failing: %s", strings.Join(correlation.DegradedNeighbours, ", "))
			label = "Shared slowdown on the host"
			action = "Look for the saturated shared resource: a neighbour slowing without breaking points at contention, not a crash."
		}
		d.StrongSignals = append(d.StrongSignals, signal)
		d.Candidates = append(d.Candidates, rcaCandidate{
			Label:      label,
			Confidence: evidenceConfidence(correlation.degradedStrength, 0.65),
			Evidence:   []string{signal},
			Actions:    []string{action},
			kind:       "degraded",
		})
	}
	if len(correlation.InfraSignals) > 0 {
		label := "Saturation infrastructure dans la fenêtre de l'incident"
		action := "Comparer CPU/RAM/disque/latence avec les services actifs sur le même host."
		if locale == "en" {
			label = "Infrastructure saturation during the incident window"
			action = "Compare CPU/RAM/disk/latency with services active on the same host."
		}
		d.Candidates = append(d.Candidates, rcaCandidate{
			Label:      label,
			Confidence: evidenceConfidence(correlation.infraStrength, 0.7),
			Evidence:   correlation.InfraSignals,
			Actions:    []string{action},
			kind:       "infra",
		})
	}
}

func addTrafficCandidates(d *rcaDossier, ctxMap map[string]any, locale string) {
	traffic, _ := ctxMap["traffic_anomalies"].(*services.TrafficAnalysis)
	if traffic == nil || len(traffic.Signals) == 0 {
		return
	}
	top := traffic.Signals[0]
	summary := trafficSignalSummary(top, traffic.WindowMinutes, locale)
	d.StrongSignals = append(d.StrongSignals, summary)
	confidence := trafficStrength(top)
	label := fmt.Sprintf("Pic d'activité applicative sur %s", top.Service)
	action1 := fmt.Sprintf("Filtrer les logs : %s", logSearchForService(top.Service))
	action2 := "Identifier la source du pic (route, client/IP, job, retry loop ou trafic externe) avant de conclure."
	if locale == "en" {
		label = fmt.Sprintf("Application activity spike on %s", top.Service)
		action1 = fmt.Sprintf("Filter logs: %s", logSearchForService(top.Service))
		action2 = "Identify the spike source (route, client/IP, job, retry loop, or external traffic) before concluding."
	}
	d.Candidates = append(d.Candidates, rcaCandidate{
		Label:      label,
		Confidence: confidence,
		Evidence:   []string{summary},
		Actions:    []string{action1, action2},
		kind:       "traffic",
	})
}

func addMissingSignals(d *rcaDossier, subjectType string, ctxMap map[string]any, locale string) {
	if _, ok := ctxMap["traffic_anomalies"].(*services.TrafficAnalysis); !ok {
		if locale == "en" {
			d.MissingSignals = append(d.MissingSignals, "No exploitable trace/log spike in the analyzed window.")
		} else {
			d.MissingSignals = append(d.MissingSignals, "Pas de pic traces/logs exploitable dans la fenêtre analysée.")
		}
	}
	if subjectType == "service_result" {
		result, _ := ctxMap["service_result"].(slimServiceResult)
		if strings.HasPrefix(result.CheckType, "agent_") {
			if locale == "en" {
				d.MissingSignals = append(d.MissingSignals, "No process-level CPU/memory metric to attribute load to a precise PID.")
			} else {
				d.MissingSignals = append(d.MissingSignals, "Pas de métrique process-level CPU/mémoire pour attribuer la charge à un PID précis.")
			}
		}
	}
}

func scopeText(service, target string, line int) string {
	parts := []string{}
	if service != "" {
		parts = append(parts, service)
	}
	if target != "" && line > 0 {
		parts = append(parts, fmt.Sprintf("%s:%d", target, line))
	} else if target != "" {
		parts = append(parts, target)
	}
	return strings.Join(parts, " / ")
}

// correlationConfidenceCap bounds every correlation-derived candidate. Past 0.9,
// shouldUseLLMForDossier stops calling the LLM — a shortcut reserved for causes
// that state themselves (a known error message, a failing span in a trace) and
// never for a correlation, however strong its signal.
const correlationConfidenceCap = 0.88

// trafficSpikeMultiplier mirrors the ratio at which the traffic analyzer starts
// calling an activity level a spike; below it there is nothing to rank.
const trafficSpikeMultiplier = 3.0

// evidenceConfidence prefers the strength measured by the correlation service —
// how far past its threshold a metric went, whether a neighbour broke first —
// over the family's base rate, which only applies when nothing was measured.
func evidenceConfidence(measured, baseRate float64) float64 {
	if measured <= 0 {
		return baseRate
	}
	return round2(math.Min(measured, correlationConfidenceCap))
}

// degradedStrength scales with how far past its own baseline a neighbour went:
// x2 is noise-adjacent, x7 and beyond is unmistakable.
func degradedStrength(multiplier float64) float64 {
	if multiplier <= 1 {
		return 0.5
	}
	return round2(math.Min(0.5+0.05*(multiplier-1), 0.8))
}

// trafficStrength scales with how far above its own baseline a service went.
// Volume alone is not evidence — 10k requests is a quiet afternoon for some
// services — so the multiplier drives the score and raw counts only lift it.
func trafficStrength(s services.TrafficAnomalySignal) float64 {
	multiplier := math.Max(s.TraceMultiplier, s.LogMultiplier)
	if multiplier <= 0 {
		// No baseline to compare against: this may simply be normal activity.
		return 0.7
	}
	strength := 0.7 + 0.02*(multiplier-trafficSpikeMultiplier)
	if s.ErrorLogCount >= 20 {
		strength += 0.05
	}
	return round2(math.Min(math.Max(strength, 0.7), correlationConfidenceCap))
}

// reinforceCandidates merges signal families that describe one story instead of
// letting them compete. A host saturating WHILE its traffic spikes is a single
// cause — ranked separately, the two halves bury the causal link and whichever
// has the higher static confidence wins on its own.
//
// Everything is anchored on a traffic spike: load is what makes two otherwise
// independent observations one narrative. Only one merge is applied, strain
// before upstream, because a saturating host explains the symptom more directly
// than a dependency that broke under the same load.
func reinforceCandidates(d *rcaDossier, locale string) {
	load := indexOfKind(d.Candidates, "traffic")
	if load < 0 {
		return
	}
	if strain := indexOfKind(d.Candidates, "infra", "degraded"); strain >= 0 {
		label := "Saturation du host sous un pic de trafic applicatif"
		action := "Confronter la fenêtre du pic de trafic et celle de la saturation : si elles coïncident, traiter la charge, pas le host."
		if locale == "en" {
			label = "Host saturating under an application traffic spike"
			action = "Line up the traffic spike window with the saturation window: if they coincide, address the load, not the host."
		}
		mergeCandidates(d, strain, load, "saturation", label, action)
		return
	}
	if upstream := indexOfKind(d.Candidates, "upstream", "upstream_app"); upstream >= 0 {
		label := "Dépendance amont tombée sous un pic de trafic"
		action := "Vérifier si la dépendance a cédé sous la charge plutôt que d'avoir cassé d'elle-même : dimensionnement, pool de connexions, rate limit."
		if locale == "en" {
			label = "Upstream dependency giving way under a traffic spike"
			action = "Check whether the dependency gave way under load rather than breaking on its own: sizing, connection pool, rate limit."
		}
		mergeCandidates(d, upstream, load, "upstream_load", label, action)
	}
}

// mergeCandidates replaces two candidates with a single stronger one, keeping
// both sides' evidence so the operator can still check the claim.
func mergeCandidates(d *rcaDossier, a, b int, kind, label, action string) {
	confidence := math.Min(math.Max(d.Candidates[a].Confidence, d.Candidates[b].Confidence)+0.10, correlationConfidenceCap)
	merged := rcaCandidate{
		Label:      label,
		Confidence: round2(confidence),
		Evidence:   append(append([]string{}, d.Candidates[a].Evidence...), d.Candidates[b].Evidence...),
		Actions:    append([]string{action}, d.Candidates[b].Actions...),
		kind:       kind,
	}
	d.Candidates = append(dropKinds(d.Candidates, d.Candidates[a].kind, d.Candidates[b].kind), merged)
}

// indexOfKind returns the position of the first candidate matching any of the
// given kinds, in the order the kinds are listed.
func indexOfKind(items []rcaCandidate, kinds ...string) int {
	for _, kind := range kinds {
		for i, c := range items {
			if c.kind == kind {
				return i
			}
		}
	}
	return -1
}

func dropKinds(items []rcaCandidate, kinds ...string) []rcaCandidate {
	out := make([]rcaCandidate, 0, len(items))
	for _, c := range items {
		drop := false
		for _, k := range kinds {
			if c.kind == k {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, c)
		}
	}
	return out
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func sortCandidates(items []rcaCandidate) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].Confidence > items[i].Confidence {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}
