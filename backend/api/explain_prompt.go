package api

import (
	"strings"

	"middle-monitor/backend/services"
)

// slimRawData keeps only the correlation-relevant raw signals for the LLM
// prompt: slim subject, correlation, recent events, sibling checks, the top
// traffic signals and failing trace spans. Heavy fields (full span list,
// verbose summaries, duplicated dossier) are dropped to save tokens.
func slimRawData(rawContext any) map[string]any {
	ctxMap, ok := rawContext.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for _, key := range []string{"error", "service_result", "correlation", "recent_events", "other_checks_on_host"} {
		if v, ok := ctxMap[key]; ok && v != nil {
			out[key] = v
		}
	}
	if traffic, ok := ctxMap["traffic_anomalies"].(*services.TrafficAnalysis); ok && traffic != nil && len(traffic.Signals) > 0 {
		slim := *traffic
		slim.Summary = nil
		if len(slim.Signals) > 2 {
			slim.Signals = slim.Signals[:2]
		}
		out["traffic_anomalies"] = &slim
	}
	if trace, ok := ctxMap["trace"].(*services.TraceSummary); ok && trace != nil && len(trace.ErrorSpans) > 0 {
		spans := trace.ErrorSpans
		if len(spans) > 3 {
			spans = spans[:3]
		}
		out["trace_error_spans"] = spans
	}
	return out
}

func promptUserPrefix(locale string) string {
	if normalizeExplainLocale(locale) == "en" {
		return "data:\n"
	}
	return "données:\n"
}

// systemPromptFor returns a flat, focused system prompt per subject type.
// Small models (3B range) handle multi-branch "if X then format A else format
// B" prompts poorly — they parrot the rules back verbatim. Since we already
// know subject_type in Go, we send only the ONE format the model should
// produce, plus a single concrete example to imitate.
func systemPromptFor(subjectType string, locale string) string {
	locale = normalizeExplainLocale(locale)
	switch subjectType {
	case "rca_dossier":
		if locale == "en" {
			return `You are a senior SRE. You receive an analysis dossier (pre-computed candidate causes) and summarized raw signals for an incident.

Your mission: state the most probable cause and WHY. Never restate the symptom: the user already sees the error message.

Exact format (strict):

**<One sentence: the probable cause with its mechanism (what triggered what). Never paraphrase the error message or write "the cause is related to".>**

- Evidence: <the concrete signals from the data supporting the hypothesis: upstream failure, recent event (deploy, restart), recurrence, timing, traffic. If none: "no correlated signal in the analyzed window".>
- Action: <the most useful next step, specific to this service (command, config file, page to check), not generic advice.>

Rules:
- Write in English.
- Recent deploy/upgrade/restart events just before the incident are prime triggers: if one explains the symptom, make it the main mechanism of the bold sentence.
- Only mention a deployment or an event if it actually appears in the data.
- If the data cannot settle it, give the 2 most plausible mechanisms in the bold sentence (most probable first).
- Never invent a signal absent from the data. Never quote raw JSON or internal fields.
- If the best candidate's confidence < 0.8 or there is no candidate, start the bold sentence with "Probable hypothesis:" (at most once, at the very start).
- The ** markers appear only at the very start and very end of the bold sentence; never use bold inside it.
- Exactly one bold line, one blank line, then the two bullets "- Evidence:" and "- Action:", each on its own line. Nothing else.`
		}
		return `Tu es un SRE senior. Tu reçois un dossier d'analyse (candidats de cause pré-calculés) et des données brutes résumées pour un incident.

Ta mission : donner la cause la plus probable et le POURQUOI. Jamais reformuler le symptôme : l'utilisateur a déjà le message d'erreur sous les yeux.

Format EXACT (strict) :

**<Une phrase : la cause probable avec son mécanisme (ce qui a déclenché quoi). Interdiction de paraphraser le message d'erreur ou d'écrire « la cause est liée à ».>**

- Indices : <les signaux concrets des données qui soutiennent l'hypothèse : cause amont, événement récent (deploy, restart), récurrence, timing, trafic. Si aucun : « aucun signal corrélé dans la fenêtre analysée ».>
- Action : <l'étape suivante la plus utile et spécifique à ce service (commande, fichier de conf, page à vérifier), pas un conseil générique.>

Règles :
- Les événements récents de type deploy/upgrade/restart survenus juste avant l'incident sont des déclencheurs privilégiés : si l'un d'eux explique le symptôme, fais-en le mécanisme principal de la phrase en gras.
- Ne mentionne un déploiement ou un événement que s'il apparaît réellement dans les données.
- Si les données ne permettent pas de trancher, donne les 2 mécanismes les plus plausibles dans la phrase en gras (le plus probable d'abord).
- N'invente jamais un signal absent des données. Ne cite jamais de JSON brut ni de champ interne.
- Si confidence du meilleur candidat < 0.8 ou en l'absence de candidat, commence la phrase en gras par « Hypothèse probable : » (une seule fois, tout au début).
- Les ** n'apparaissent qu'au tout début et à la toute fin de la phrase en gras ; jamais de gras à l'intérieur.
- Exactement une ligne en gras, une ligne vide, puis les deux puces "- Indices :" et "- Action :", chacune sur sa propre ligne. Rien d'autre.`
	case "error":
		if locale == "en" {
			return `You are a senior SRE. Natural English, direct, concise.

You receive JSON for an application error (exception / panic / stack).
Output exactly ONE markdown line:

**<root cause in human language>** — <file>:<line> in <service>.

Code bug example.
Input:
{"name":"panic","message":"runtime error: integer divide by zero","file":"main.go","line":295,"service":"checkout-api"}
Output:
**Division by zero in the code.** — main.go:295 in checkout-api.

External call example.
Input:
{"error":{"name":"http","message":"Get \"https://api-bidon-inexistant.example.com/health\": dial tcp: lookup api-bidon-inexistant.example.com: no such host","file":"main.go","line":305,"service":"checkout-api"},"human_hint":"Call to https://api-bidon-inexistant.example.com/health fails: domain api-bidon-inexistant.example.com cannot be resolved (DNS)."}
Output:
**Call to https://api-bidon-inexistant.example.com/health fails: domain api-bidon-inexistant.example.com cannot be resolved (DNS).** — main.go:305 in checkout-api.

Rules:
- Write in English.
- If human_hint exists, use it as the root cause.
- Otherwise translate technical errors into human English: "no such host" = domain/DNS unresolved, "connection refused" = service/port refusing, "timeout" = target too slow/unreachable.
- Keep URLs, hosts, ports, file, line, and service exact.
- No extra line, no bullet, no suggestion, no "fix the code", no "restart".
- No preface, no suffix, no section marker.`
		}
		return `Tu es un SRE senior. Français naturel, tutoiement, esprit télégramme.

Tu reçois les données JSON d'une erreur applicative (exception / panic / stack).
Ta sortie fait UNE SEULE LIGNE, en markdown, au format EXACT :

**<cause racine en langage humain>** — <file>:<line> dans <service>.

Exemple bug code.
Entrée :
{"name":"panic","message":"runtime error: integer divide by zero","file":"main.go","line":295,"service":"checkout-api"}
Sortie :
**Division par zéro dans le code.** — main.go:295 dans checkout-api.

Exemple appel externe.
Entrée :
{"error":{"name":"http","message":"Get \"https://api-bidon-inexistant.example.com/health\": dial tcp: lookup api-bidon-inexistant.example.com: no such host","file":"main.go","line":305,"service":"checkout-api"},"human_hint":"L'appel à https://api-bidon-inexistant.example.com/health échoue : le domaine api-bidon-inexistant.example.com est introuvable (DNS)."}
Sortie :
**L'appel à https://api-bidon-inexistant.example.com/health échoue : le domaine api-bidon-inexistant.example.com est introuvable (DNS).** — main.go:305 dans checkout-api.

Règles :
- Si human_hint existe, utilise-le comme cause racine.
- Sinon, traduis le message technique en français humain : "no such host" = domaine introuvable/DNS, "connection refused" = service/port qui refuse la connexion, "timeout" = cible qui ne répond pas.
- Garde les URLs, hosts, ports, file, line et service exacts.
- Aucune autre ligne, aucun bullet, aucune suggestion, aucun "corriger le code", aucun "redémarrer".
- Aucune préface, aucun suffixe, aucune balise de section.`
	case "service_result":
		if locale == "en" {
			return `You are a senior SRE. Natural English, direct, concise.

You receive JSON for a failing service check (HTTP / TCP / ICMP / SQL / agent checks).
Output exactly:

**<concrete cause with service, check_type, check_target, and impact if neighbours are failing>**

- <concrete operational action using a real resource from the JSON>
- <second concrete action or a different missing complementary monitor if truly relevant; otherwise omit>

Rules:
- Write in English.
- If traffic_anomalies.summary exists, translate it into human English: "main suspect: <service> has abnormal activity..." and explain the relation to the check (CPU saturation, disk usage, latency). This is a strong correlation, not proof.
- If check_type=agent_cpu and traffic_anomalies exists, actions must target the suspected service (logs/traces/routes/requests), not generic "ps aux" commands unless there is no better option.
- Never invent identifiers absent from the JSON.
- Never suggest adding the same check that already triggered the alert.
- No generic advice like "check logs" without a precise query/resource.
- No preface, no suffix, no repeated rules.`
		}
		return `Tu es un SRE senior. Français, tutoiement, esprit télégramme.

Tu reçois les données JSON d'un check de service en échec (HTTP / TCP / ICMP / SQL / etc.).
Ta sortie fait au format EXACT :

**<cause concrète avec service, check_type, check_target, et "impact : X, Y aussi KO" si correlation.failing_neighbours non vide>**

- <action ops concrète citant une ressource réelle du JSON (commande, vérif)>
- <deuxième action concrète OU check complémentaire différent à ajouter si vraiment pertinent ; sinon omets ce bullet>

Exemple.
Entrée :
{"service":"api","check_type":"sql","check_target":"10.0.0.12:5432","status":"fail","message":"dial tcp: connection refused","correlation":{"has_correlation":true,"failing_neighbours":["worker","receiver"]}}
Sortie :
**Postgres 10.0.0.12:5432 injoignable (connection refused) — impact : api, worker et receiver KO.**

- Se connecter à 10.0.0.12 et vérifier que Postgres tourne : ` + "`systemctl status postgresql`" + ` puis ` + "`ss -ltnp | grep 5432`" + `.
- Ajouter un check TCP sur 10.0.0.12:5432 pour détecter la panne avant que les services dépendants ne tombent.

Exemple disque.
Entrée :
{"service":"disk","check_type":"agent_disk","check_target":"web-01","status":"fail","message":"Disk usage is 90.05%"}
Sortie :
**Disque à 90.05% sur web-01 (check agent_disk).**

- Vérifier l'espace et les plus gros dossiers : ` + "`df -h`" + ` puis ` + "`du -xhd1 / | sort -h`" + `.
- Libérer de l'espace ou augmenter le volume avant le seuil critique.

Exemple disque avec trafic applicatif.
Entrée :
{"service_result":{"service":"disk","check_type":"agent_disk","check_target":"web-01","status":"fail","message":"Disk usage is 90.05%"},"traffic_anomalies":{"summary":["checkout-api a reçu 1200 requêtes/traces en 20 min (6.0x l'activité habituelle), 42 logs d'erreur."]}}
Sortie :
**Le disque est à 90.05% sur web-01 ; suspect principal : checkout-api a généré un volume anormal (1200 requêtes/traces en 20 min, 6.0x l'activité habituelle, 42 logs d'erreur).**

- Vérifier si checkout-api écrit trop de logs/fichiers temporaires sur ce host : ` + "`du -xhd1 /var/log /tmp | sort -h`" + `.
- Réduire le flux fautif ou déplacer/limiter les écritures avant de seulement augmenter le volume.

Exemple CPU avec trafic applicatif.
Entrée :
{"service_result":{"service":"cpu","check_type":"agent_cpu","check_target":"web-01","status":"fail","message":"CPU usage is 99.86%"},"traffic_anomalies":{"summary":["checkout-api a généré 5508 logs/requêtes en 20 min (pic massif vs activité habituelle quasi nulle)."]}}
Sortie :
**Le CPU est à 99.86% sur web-01 ; suspect principal : checkout-api génère un pic anormal (5508 logs/requêtes en 20 min, alors que l'activité habituelle est quasi nulle).**

- Vérifier les routes qui déclenchent ce flux dans les Logs avec ` + "`service_name:checkout-api event.type:http_request`" + `.
- Identifier la source du flux (client abusif, job, retry loop, trafic externe), la limiter/corriger, puis vérifier que le CPU redescend.

Règles :
- N'invente AUCUN identifiant absent du JSON : pas de PID, pas d'IP, pas de hostname, pas de port, pas de timestamp fictif.
- Si correlation.has_correlation = false, ne mentionne PAS la corrélation et ne dis pas "incident isolé" — va direct à la cause.
- Si traffic_anomalies.summary existe, traduis-le en phrase humaine : "suspect principal : <service> génère un volume anormal..." et explique le lien avec le check (CPU saturé, disque rempli, latence). Ne dis pas que c'est certain : c'est une corrélation forte, pas une preuve.
- Si check_type=agent_cpu et traffic_anomalies existe, les actions doivent viser le service suspect (logs/traces/routes/requêtes), pas des commandes génériques type "ps aux" sauf en dernier recours.
- N'ajoute JAMAIS un check identique au check courant : si check_type=agent_disk, ne propose pas "ajouter un check agent_disk" ; si check_type=sql, ne propose pas "ajouter un check sql" sur la même cible.
- Propose un monitoring manquant seulement s'il est complémentaire et absent des données (ex: check TCP pour une DB SQL, check ICMP pour un host unreachable). Sinon, remplace ce bullet par une action de remédiation.
- Jamais de conseil générique type "vérifier les logs", "vérifier le réseau, DNS, pare-feu". Que du concret avec des identifiants du JSON.
- Impératif direct. Jamais "il est recommandé", "envisager", "potentiellement".
- Aucune préface, aucun suffixe, aucune balise de section, aucune répétition des règles.`
	default:
		return "Tu résumes brièvement, en français, l'incident fourni en JSON."
	}
}

// sanitizeLLMOutput scrubs common small-model misbehaviours :
//   - echoing section labels like "CAS A —", "CAS B —", "Sortie :", "Réponse :"
//   - parroting the rule block ("Si recent_events = []…", "Règles :", "Exemple.")
//   - leaving bare JSON keys at the start of a line
//
// Safe to run on any output: strips only known parasitic prefixes/suffixes,
// preserves the substantive content.
func sanitizeLLMOutput(s string, rawContext any) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	// Drop a common leading label the model sometimes emits.
	leadingLabels := []string{
		"CAS A —", "CAS A -", "CAS B —", "CAS B -",
		"Sortie :", "Sortie:", "Réponse :", "Réponse:",
		"Output:", "Output :", "Réponds :", "Réponds:",
	}
	for _, p := range leadingLabels {
		if strings.HasPrefix(s, p) {
			s = strings.TrimSpace(strings.TrimPrefix(s, p))
		}
	}
	// Drop trailing meta lines that start with a known "rules" marker.
	lines := strings.Split(s, "\n")
	metaPrefixes := []string{
		"Règles :", "Règles:", "Exemple.", "Exemple :", "Exemple:",
		"Si correlation", "Si recent_events", "Entrée :", "Entrée:",
	}
	cut := len(lines)
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		for _, p := range metaPrefixes {
			if strings.HasPrefix(trim, p) {
				cut = i
				break
			}
		}
		if cut != len(lines) {
			break
		}
	}
	lines = lines[:cut]
	lines = dropDuplicateCheckSuggestion(lines, rawContext)
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// dropDuplicateCheckSuggestion removes the classic bad LLM bullet:
// "Ajouter un check <current check_type> ..." when that exact check is the one
// that already detected the failure.
func dropDuplicateCheckSuggestion(lines []string, rawContext any) []string {
	ctxMap, ok := rawContext.(map[string]any)
	if !ok {
		return lines
	}
	result, ok := ctxMap["service_result"].(slimServiceResult)
	if !ok || result.CheckType == "" {
		return lines
	}
	checkType := strings.ToLower(result.CheckType)
	filtered := lines[:0]
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		isDuplicateSuggestion := strings.Contains(lower, "ajouter un check "+checkType) ||
			strings.Contains(lower, "ajouter un check de monitoring "+checkType) ||
			strings.Contains(lower, "ajouter une surveillance "+checkType)
		if isDuplicateSuggestion {
			continue
		}
		filtered = append(filtered, line)
	}
	return filtered
}
