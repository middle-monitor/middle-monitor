package api

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"middle-monitor/backend/services"
)

// basename returns the last path segment for display/prompting. The UI already
// has enough context around the service; absolute repo paths add noise.
func basename(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	parts := strings.Split(p, "/")
	// drop empty segments produced by leading "/"
	cleaned := make([]string, 0, len(parts))
	for _, seg := range parts {
		if seg == "" {
			continue
		}
		cleaned = append(cleaned, seg)
	}
	switch len(cleaned) {
	case 0:
		return p
	case 1:
		return cleaned[0]
	default:
		return cleaned[len(cleaned)-1]
	}
}

// explainRulesOnly produces a root-cause answer WITHOUT any LLM call.
// It reuses the same preloaded context as single_shot and renders a minimal
// deterministic answer: one bold cause sentence plus at most one correlation
// line. Instant (<50ms), free, no infra, no hallucination.
func explainRulesOnly(ctx context.Context, db *sql.DB, orgID int64, subjectType string, subjectID int64, locale string) (content, model string, durationMS int, err error) {
	locale = normalizeExplainLocale(locale)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	start := time.Now()
	correlationSvc := services.NewCorrelationService(db)
	trafficAnalyzer := services.NewTrafficAnalyzer(db, opensearch)

	var ctxMap map[string]any
	switch subjectType {
	case "error":
		ctxMap, err = buildErrorContext(cctx, db, correlationSvc, trafficAnalyzer, orgID, subjectID)
	case "service_result":
		ctxMap, err = buildServiceResultContext(cctx, db, correlationSvc, trafficAnalyzer, orgID, subjectID)
	default:
		return "", "", 0, &SubjectTypeError{SubjectType: subjectType}
	}
	if err != nil {
		return "", "", 0, err
	}

	content = renderRulesReport(subjectType, ctxMap, locale)
	durationMS = int(time.Since(start).Milliseconds())
	if strings.TrimSpace(content) == "" {
		return "", "", durationMS, ErrRulesReportEmpty
	}
	return content, "rules", durationMS, nil
}

// renderRulesReport produces the minimal rule-based answer: ONE bold sentence
// stating the cause, plus at most ONE line naming a potential correlation.
// Evidence dumps and generic recommended actions proved to be noise.
func renderRulesReport(subjectType string, ctxMap map[string]any, locale string) string {
	locale = normalizeExplainLocale(locale)
	en := locale == "en"

	var cause, scope string
	switch subjectType {
	case "error":
		e, ok := ctxMap["error"].(slimError)
		if !ok {
			return ""
		}
		cause = knownErrorHint(e, locale)
		if cause == "" {
			// The raw error message is usually self-explanatory; stating it
			// plainly beats claiming "no clear cause".
			cause = strings.TrimSpace(strings.Trim(fmt.Sprintf("%s: %s", e.Name, e.Message), ": "))
		}
		scope = scopeText(e.Service, e.File, e.Line)
	case "service_result":
		r, ok := ctxMap["service_result"].(slimServiceResult)
		if !ok {
			return ""
		}
		cause, _ = knownServiceResultCause(r, locale)
		if cause == "" {
			cause = strings.TrimSpace(r.Message)
		}
		if cause == "" {
			if en {
				cause = fmt.Sprintf("%s check failed on %s", r.CheckType, r.CheckTarget)
			} else {
				cause = fmt.Sprintf("Check %s en échec sur %s", r.CheckType, r.CheckTarget)
			}
		}
		scope = r.Service
		if r.CheckTarget != "" && !strings.Contains(strings.ToLower(cause), strings.ToLower(r.CheckTarget)) {
			scope = scopeText(r.Service, r.CheckTarget, 0)
		}
	default:
		return ""
	}

	var b strings.Builder
	b.WriteString("**")
	b.WriteString(strings.TrimSuffix(strings.TrimSpace(cause), "."))
	b.WriteString(".**")
	if scope != "" {
		b.WriteString(" — " + scope + ".")
	}
	if line := correlationLine(ctxMap, en); line != "" {
		b.WriteString("\n\n")
		b.WriteString(line)
	}
	return b.String()
}

// correlationLine returns at most ONE short sentence naming what the incident
// correlates with, ordered by causal strength, or "" when there is no signal.
func correlationLine(ctxMap map[string]any, en bool) string {
	correlation, _ := ctxMap["correlation"].(*slimCorrelation)

	if correlation != nil && len(correlation.UpstreamSuspects) > 0 {
		list := strings.Join(correlation.UpstreamSuspects, ", ")
		if en {
			return fmt.Sprintf("Possible correlation: %s failed just before — likely upstream cause.", list)
		}
		return fmt.Sprintf("Corrélation possible : %s a échoué juste avant — cause amont probable.", list)
	}
	// A declared dependency erroring first is the same causal strength as a
	// neighbouring check failing first, so it ranks right after it.
	if correlation != nil && len(correlation.UpstreamApps) > 0 {
		list := strings.Join(correlation.UpstreamApps, ", ")
		if en {
			return fmt.Sprintf("Possible correlation: dependency %s errored just before — likely upstream cause.", list)
		}
		return fmt.Sprintf("Corrélation possible : la dépendance %s a erré juste avant — cause amont probable.", list)
	}
	if events, ok := ctxMap["recent_events"].([]slimEvent); ok {
		for _, ev := range events {
			t := strings.ToLower(ev.Type)
			if strings.Contains(t, "deploy") || strings.Contains(t, "restart") || strings.Contains(t, "release") {
				if en {
					return fmt.Sprintf("Possible correlation: %s event at %s, right before the incident.", ev.Type, ev.Timestamp.Format("15:04"))
				}
				return fmt.Sprintf("Corrélation possible : événement %s à %s, juste avant l'incident.", ev.Type, ev.Timestamp.Format("15:04"))
			}
		}
	}
	if traffic, ok := ctxMap["traffic_anomalies"].(*services.TrafficAnalysis); ok && traffic != nil && len(traffic.Signals) > 0 {
		locale := "fr"
		if en {
			locale = "en"
		}
		summary := trafficSignalSummary(traffic.Signals[0], traffic.WindowMinutes, locale)
		if en {
			return "Possible correlation: " + summary
		}
		return "Corrélation possible : " + summary
	}
	if correlation != nil && len(correlation.FailingNeighbours) > 0 {
		list := strings.Join(correlation.FailingNeighbours, ", ")
		if en {
			return fmt.Sprintf("Possible correlation: %s failing at the same time — shared dependency or host.", list)
		}
		return fmt.Sprintf("Corrélation possible : %s en échec au même moment — dépendance ou host commun.", list)
	}
	if correlation != nil && len(correlation.DegradedNeighbours) > 0 {
		list := strings.Join(correlation.DegradedNeighbours, ", ")
		if en {
			return fmt.Sprintf("Possible correlation: %s slowed down at the same time without failing — shared host or network.", list)
		}
		return fmt.Sprintf("Corrélation possible : %s a ralenti au même moment sans tomber en échec — host ou réseau commun.", list)
	}
	if correlation != nil && len(correlation.InfraSignals) > 0 {
		if en {
			return "Possible correlation: " + ensureSentence(correlation.InfraSignals[0])
		}
		return "Corrélation possible : " + ensureSentence(correlation.InfraSignals[0])
	}
	if correlation != nil && correlation.RecurrenceIsNew {
		if en {
			return "First occurrence of this error — possibly a recent regression."
		}
		return "Première occurrence de cette erreur — possible régression récente."
	}
	return ""
}

func ensureSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	last := s[len(s)-1]
	if last == '.' || last == '!' || last == '?' || last == ':' {
		return s
	}
	return s + "."
}

func deterministicKnownExplanation(subjectType string, rawContext any, locale string) string {
	locale = normalizeExplainLocale(locale)
	ctxMap, ok := rawContext.(map[string]any)
	if !ok {
		return ""
	}
	if subjectType == "service_result" {
		return deterministicServiceResultExplanation(ctxMap, locale)
	}
	if subjectType != "error" {
		return ""
	}
	errData, ok := ctxMap["error"].(slimError)
	if !ok {
		return ""
	}
	hint := knownErrorHint(errData, locale)
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return ""
	}

	location := errData.File
	if errData.Line > 0 {
		location = fmt.Sprintf("%s:%d", location, errData.Line)
	}
	suffix := location
	if errData.Service != "" {
		if locale == "en" {
			suffix += " in " + errData.Service
		} else {
			suffix += " dans " + errData.Service
		}
	}
	return fmt.Sprintf("**%s** — %s.", strings.TrimSuffix(hint, "."), suffix)
}

func deterministicServiceResultExplanation(ctxMap map[string]any, locale string) string {
	result, ok := ctxMap["service_result"].(slimServiceResult)
	if !ok {
		return ""
	}
	if content := deterministicKnownServiceResultExplanation(ctxMap, result, locale); content != "" {
		return content
	}
	traffic, _ := ctxMap["traffic_anomalies"].(*services.TrafficAnalysis)
	var signal services.TrafficAnomalySignal
	var windowMinutes int
	if traffic != nil && len(traffic.Signals) > 0 {
		signal = traffic.Signals[0]
		windowMinutes = traffic.WindowMinutes
	}

	target := result.CheckTarget
	if target == "" {
		if traffic != nil && traffic.HostName != "" {
			target = traffic.HostName
		} else {
			target = result.Service
		}
	}

	switch result.CheckType {
	case "agent_cpu":
		if signal.Service != "" {
			return deterministicAgentCPUExplanation(result, signal, target, windowMinutes, locale)
		}
		value := extractPercent(result.Message)
		if value == "" {
			value = "high"
			if locale != "en" {
				value = "haut"
			}
		}
		// Neighbours slowing down is traffic evidence too, even when OpenSearch
		// saw nothing: claiming "no abnormal traffic" here would be wrong.
		if neighbours := degradedNeighbourText(ctxMap); neighbours != "" {
			if locale == "en" {
				return fmt.Sprintf("**CPU %s on %s, and %s slowed down at the same time.**\n\n- Same host group: check whether the load on %s is what slowed these checks.\n- Run `top` on %s to identify the process consuming CPU.", value, target, neighbours, target, target)
			}
			return fmt.Sprintf("**CPU %s sur %s, et %s a ralenti au même moment.**\n\n- Même groupe de hosts : vérifiez si la charge sur %s explique ce ralentissement.\n- Lancez `top` sur %s pour identifier le processus responsable.", value, target, neighbours, target, target)
		}
		if locale == "en" {
			return fmt.Sprintf("**CPU %s on %s: no abnormal traffic detected.**\n\n- Filter logs for background jobs or scripts running on the host.\n- Run `top` or check the agent process list to identify the exact process consuming CPU.", value, target)
		}
		return fmt.Sprintf("**CPU %s sur %s : aucun trafic anormal détecté.**\n\n- Vérifiez les logs pour identifier un job ou script lourd en tâche de fond.\n- Lancez `top` ou vérifiez la liste des processus pour identifier l'origine de la surcharge.", value, target)
	case "agent_disk":
		if signal.Service != "" {
			return deterministicAgentDiskExplanation(result, signal, target, windowMinutes, locale)
		}
		value := extractPercent(result.Message)
		if value == "" {
			value = "full"
			if locale != "en" {
				value = "plein"
			}
		}
		if locale == "en" {
			return fmt.Sprintf("**Disk %s on %s.**\n\n- SSH into the host and run `df -h` and `du -sh /*` to find large directories.\n- Clear old logs, temporary files, or unused container images.", value, target)
		}
		return fmt.Sprintf("**Disque %s sur %s.**\n\n- Connectez-vous sur l'hôte et lancez `df -h` et `du -sh /*` pour trouver les gros dossiers.\n- Nettoyez les anciens logs, fichiers temporaires ou images de conteneurs inutilisées.", value, target)
	default:
		return ""
	}
}

// degradedNeighbourText lists host-group neighbours that slowed down, so a
// saturation report never claims nothing else moved when something did.
func degradedNeighbourText(ctxMap map[string]any) string {
	correlation, _ := ctxMap["correlation"].(*slimCorrelation)
	if correlation == nil || len(correlation.DegradedNeighbours) == 0 {
		return ""
	}
	return strings.Join(correlation.DegradedNeighbours, ", ")
}

func deterministicKnownServiceResultExplanation(ctxMap map[string]any, result slimServiceResult, locale string) string {
	msg := strings.ToLower(strings.TrimSpace(result.Message))
	if msg == "" {
		return ""
	}
	target := result.CheckTarget
	if target == "" {
		target = result.Service
	}
	impact := serviceImpactText(ctxMap, result.Service, locale)

	switch {
	case strings.Contains(msg, "connection refused"):
		if locale == "en" {
			return fmt.Sprintf(
				"**%s refuses the connection%s.**\n\n- Check that the target process is listening: `%s`.\n- Verify firewall or container/network rules between the checker and %s.",
				target,
				impact,
				listeningCommand(target),
				target,
			)
		}
		return fmt.Sprintf(
			"**%s refuse la connexion%s.**\n\n- Vérifier que le service écoute sur la cible : `%s`.\n- Vérifier le firewall ou les règles réseau/container entre le checker et %s.",
			target,
			impact,
			listeningCommand(target),
			target,
		)
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "dns"):
		if locale == "en" {
			return fmt.Sprintf(
				"**DNS cannot resolve %s%s.**\n\n- Resolve the name from the checker host: `getent hosts %s`.\n- Fix the DNS record or the service target configured for this check.",
				target,
				impact,
				targetHostOnly(target),
			)
		}
		return fmt.Sprintf(
			"**DNS ne résout pas %s%s.**\n\n- Tester la résolution depuis le host checker : `getent hosts %s`.\n- Corriger l'enregistrement DNS ou la cible configurée pour ce check.",
			target,
			impact,
			targetHostOnly(target),
		)
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
		if locale == "en" {
			return fmt.Sprintf(
				"**%s does not answer before timeout%s.**\n\n- Test reachability from the checker host: `nc -vz %s`.\n- Check latency/load on the target and network path.",
				target,
				impact,
				target,
			)
		}
		return fmt.Sprintf(
			"**%s ne répond pas avant le timeout%s.**\n\n- Tester l'accès depuis le host checker : `nc -vz %s`.\n- Vérifier la latence, la charge de la cible et le chemin réseau.",
			target,
			impact,
			target,
		)
	default:
		return ""
	}
}

func serviceImpactText(ctxMap map[string]any, currentService string, locale string) string {
	correlation, _ := ctxMap["correlation"].(*slimCorrelation)
	if correlation == nil || len(correlation.FailingNeighbours) == 0 {
		return ""
	}
	impacted := append([]string{}, correlation.FailingNeighbours...)
	if currentService != "" && !containsString(impacted, currentService) {
		impacted = append([]string{currentService}, impacted...)
	}
	if len(impacted) == 0 {
		return ""
	}
	if len(impacted) > 3 {
		impacted = impacted[:3]
	}
	if locale == "en" {
		return fmt.Sprintf(" — impact: %s also failing", strings.Join(impacted, ", "))
	}
	return fmt.Sprintf(" — impact : %s aussi KO", strings.Join(impacted, ", "))
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func targetHostOnly(target string) string {
	host := target
	if strings.Contains(host, "://") {
		if parsed, err := url.Parse(host); err == nil && parsed.Hostname() != "" {
			return parsed.Hostname()
		}
	}
	if h, _, err := net.SplitHostPort(host); err == nil && h != "" {
		return h
	}
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		return host[:idx]
	}
	return host
}

func listeningCommand(target string) string {
	_, port, err := net.SplitHostPort(target)
	if err != nil || port == "" {
		return "ss -ltnp"
	}
	return "ss -ltnp | grep " + port
}

func deterministicAgentCPUExplanation(result slimServiceResult, signal services.TrafficAnomalySignal, target string, windowMinutes int, locale string) string {
	value := extractPercent(result.Message)
	if value == "" {
		if locale == "en" {
			value = "high"
		} else {
			value = "haut"
		}
	}
	volume := trafficVolumeText(signal, windowMinutes, locale)
	if locale == "en" {
		return fmt.Sprintf(
			"**CPU %s on %s: the spike coincides with abnormal activity from %s (%s).**\n\n- Filter logs: `%s` to identify the routes, clients, or jobs behind the spike.\n- Limit or fix the abnormal traffic source (abusive client, job, retry loop, or external traffic), then confirm CPU usage drops.",
			value,
			target,
			signal.Service,
			volume,
			logSearchForService(signal.Service),
		)
	}
	return fmt.Sprintf(
		"**CPU %s sur %s : le pic coïncide avec une activité anormale de %s (%s).**\n\n- Filtrer les logs : `%s` pour identifier les routes, clients ou jobs responsables du pic.\n- Limiter ou corriger la source du flux anormal (client abusif, job, retry loop, trafic externe), puis vérifier que le CPU redescend.",
		value,
		target,
		signal.Service,
		volume,
		logSearchForService(signal.Service),
	)
}

func deterministicAgentDiskExplanation(result slimServiceResult, signal services.TrafficAnomalySignal, target string, windowMinutes int, locale string) string {
	value := extractPercent(result.Message)
	if value == "" {
		if locale == "en" {
			value = "high"
		} else {
			value = "haut"
		}
	}
	volume := trafficVolumeText(signal, windowMinutes, locale)
	if locale == "en" {
		return fmt.Sprintf(
			"**Disk %s on %s: the spike coincides with abnormal activity from %s (%s).**\n\n- Filter logs: `%s` to find the routes or errors generating volume.\n- Check whether %s writes too much data to `/var/log`, `/tmp`, or an application directory before increasing disk size.",
			value,
			target,
			signal.Service,
			volume,
			logSearchForService(signal.Service),
			signal.Service,
		)
	}
	return fmt.Sprintf(
		"**Disque %s sur %s : le pic coïncide avec une activité anormale de %s (%s).**\n\n- Filtrer les logs : `%s` pour trouver les routes ou erreurs qui génèrent du volume.\n- Vérifier si %s écrit trop dans `/var/log`, `/tmp` ou un répertoire applicatif avant d'augmenter le volume.",
		value,
		target,
		signal.Service,
		volume,
		logSearchForService(signal.Service),
		signal.Service,
	)
}

func trafficVolumeText(signal services.TrafficAnomalySignal, windowMinutes int, locale string) string {
	if windowMinutes <= 0 {
		windowMinutes = 20
	}
	switch {
	case signal.TraceCount > 0 && signal.LogCount > 0:
		if locale == "en" {
			return fmt.Sprintf("%d requests/traces and %d logs in %d min", signal.TraceCount, signal.LogCount, windowMinutes)
		}
		return fmt.Sprintf("%d requêtes/traces et %d logs en %d min", signal.TraceCount, signal.LogCount, windowMinutes)
	case signal.TraceCount > 0:
		if locale == "en" {
			return fmt.Sprintf("%d requests/traces in %d min", signal.TraceCount, windowMinutes)
		}
		return fmt.Sprintf("%d requêtes/traces en %d min", signal.TraceCount, windowMinutes)
	case signal.LogCount > 0:
		if locale == "en" {
			return fmt.Sprintf("%d logs/requests in %d min", signal.LogCount, windowMinutes)
		}
		return fmt.Sprintf("%d logs/requêtes en %d min", signal.LogCount, windowMinutes)
	default:
		if locale == "en" {
			return "abnormal traffic/log volume"
		}
		return "trafic/logs anormaux"
	}
}

func trafficSignalSummary(signal services.TrafficAnomalySignal, windowMinutes int, locale string) string {
	locale = normalizeExplainLocale(locale)
	volume := trafficVolumeText(signal, windowMinutes, locale)
	if signal.Service == "" {
		if locale == "en" {
			return "Abnormal application activity detected: " + volume + "."
		}
		return "Activité applicative anormale détectée : " + volume + "."
	}
	if locale == "en" {
		return fmt.Sprintf("%s shows abnormal activity: %s.", signal.Service, volume)
	}
	return fmt.Sprintf("%s montre une activité anormale : %s.", signal.Service, volume)
}

func logSearchForService(service string) string {
	if strings.TrimSpace(service) == "" {
		return "event.type:http_request"
	}
	return fmt.Sprintf("service_name:%s event.type:http_request", service)
}

func extractPercent(message string) string {
	for _, token := range strings.Fields(message) {
		token = strings.Trim(token, ".,;:()")
		if strings.HasSuffix(token, "%") {
			return token
		}
	}
	return ""
}

// knownErrorHint converts common low-level client/runtime errors into a concise
// human sentence. The LLM can still render it, but it no longer has to infer the
// obvious root cause from Go's raw error string.
func knownErrorHint(e slimError, locale string) string {
	locale = normalizeExplainLocale(locale)
	message := strings.TrimSpace(e.Message)
	if message == "" {
		return ""
	}
	lower := strings.ToLower(message)
	targetURL := quotedURL(message)
	host := hostFromURL(targetURL)

	if strings.Contains(lower, "must be used within") && strings.Contains(lower, "provider") {
		hook := firstToken(message)
		provider := providerNameFromMessage(message)
		if locale == "en" {
			if hook != "" && provider != "" {
				return fmt.Sprintf("%s is rendered outside %s in the React component tree.", hook, provider)
			}
			return "A React hook is used outside its required Provider."
		}
		if hook != "" && provider != "" {
			return fmt.Sprintf("%s est utilisé en dehors de %s dans l'arbre React.", hook, provider)
		}
		return "Un hook React est utilisé en dehors de son Provider requis."
	}
	if strings.Contains(lower, "invalid hook call") {
		if locale == "en" {
			return "Invalid React hook call: a hook is called outside a valid React component/render context."
		}
		return "Appel de hook React invalide : un hook est appelé hors d'un contexte de rendu/composant valide."
	}
	if strings.Contains(lower, "cannot read properties of undefined") || strings.Contains(lower, "cannot read property") {
		if locale == "en" {
			return "Frontend code reads a property on an undefined value."
		}
		return "Le frontend lit une propriété sur une valeur undefined."
	}
	if strings.Contains(lower, "no such host") {
		if host == "" {
			host = lookupHostFromMessage(message)
		}
		if targetURL != "" && host != "" {
			if locale == "en" {
				return fmt.Sprintf("Call to %s fails: domain %s cannot be resolved (DNS).", targetURL, host)
			}
			return fmt.Sprintf("L'appel à %s échoue : le domaine %s est introuvable (DNS).", targetURL, host)
		}
		if host != "" {
			if locale == "en" {
				return fmt.Sprintf("Domain %s cannot be resolved (DNS).", host)
			}
			return fmt.Sprintf("Le domaine %s est introuvable (DNS).", host)
		}
		if locale == "en" {
			return "The target cannot be resolved by DNS."
		}
		return "La cible appelée est introuvable côté DNS."
	}
	if strings.Contains(lower, "connection refused") {
		if targetURL != "" {
			if locale == "en" {
				return fmt.Sprintf("Call to %s fails: the target refuses the connection.", targetURL)
			}
			return fmt.Sprintf("L'appel à %s échoue : la cible refuse la connexion.", targetURL)
		}
		if locale == "en" {
			return "The target refuses the connection."
		}
		return "La cible refuse la connexion."
	}
	if strings.Contains(lower, "i/o timeout") || strings.Contains(lower, "context deadline exceeded") || strings.Contains(lower, "timeout") {
		if targetURL != "" {
			if locale == "en" {
				return fmt.Sprintf("Call to %s times out: the target does not respond fast enough.", targetURL)
			}
			return fmt.Sprintf("L'appel à %s expire : la cible ne répond pas à temps.", targetURL)
		}
		if locale == "en" {
			return "The target does not respond fast enough."
		}
		return "La cible ne répond pas à temps."
	}
	if strings.Contains(lower, "certificate") || strings.Contains(lower, "x509:") || strings.Contains(lower, "tls:") {
		if targetURL != "" {
			if locale == "en" {
				return fmt.Sprintf("Call to %s fails because of a TLS/certificate issue.", targetURL)
			}
			return fmt.Sprintf("L'appel à %s échoue à cause d'un problème TLS/certificat.", targetURL)
		}
		if locale == "en" {
			return "The call fails because of a TLS/certificate issue."
		}
		return "L'appel échoue à cause d'un problème TLS/certificat."
	}
	if strings.Contains(lower, "integer divide by zero") || strings.Contains(lower, "division by zero") || strings.Contains(lower, "divide by zero") {
		if locale == "en" {
			return "Division by zero in the code."
		}
		return "Division par zéro dans le code."
	}
	if strings.Contains(lower, "expired") && (strings.Contains(lower, "token") || strings.Contains(lower, "jwt") || strings.Contains(lower, "session")) {
		if locale == "en" {
			return "The authentication token is expired: the client must refresh it or re-authenticate."
		}
		return "Le token d'authentification est expiré : le client doit le rafraîchir ou se ré-authentifier."
	}
	return ""
}

func quotedURL(message string) string {
	start := strings.Index(message, "\"")
	if start == -1 {
		return ""
	}
	rest := message[start+1:]
	end := strings.Index(rest, "\"")
	if end == -1 {
		return ""
	}
	candidate := rest[:end]
	if strings.HasPrefix(candidate, "http://") || strings.HasPrefix(candidate, "https://") {
		return candidate
	}
	return ""
}

func hostFromURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func lookupHostFromMessage(message string) string {
	const marker = "lookup "
	idx := strings.Index(message, marker)
	if idx == -1 {
		return ""
	}
	rest := message[idx+len(marker):]
	end := strings.Index(rest, ":")
	if end == -1 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}

func firstToken(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	fields := strings.Fields(message)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], " :;,.()")
}

func providerNameFromMessage(message string) string {
	idx := strings.LastIndex(strings.ToLower(message), " within ")
	if idx == -1 {
		return ""
	}
	provider := strings.TrimSpace(message[idx+len(" within "):])
	provider = strings.Trim(provider, " :;,.()")
	return provider
}
