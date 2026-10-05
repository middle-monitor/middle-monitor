package api

import (
	"strings"
	"testing"
	"time"

	"middle-monitor/backend/services"
)

// The UI already shows the service, so a prompt carrying an absolute repo path
// would only spend tokens on noise.
func TestBasenameKeepsOnlyTheLastSegment(t *testing.T) {
	cases := map[string]string{
		"/srv/app/main.go":       "main.go",
		"main.go":                "main.go",
		`C:\work\src\handler.go`: "handler.go",
		"/":                      "/",
		"  /a/b/c.ts  ":          "c.ts",
		"":                       "",
	}
	for input, want := range cases {
		if got := basename(input); got != want {
			t.Fatalf("basename(%q) = %q, want %q", input, got, want)
		}
	}
}

// Anything not explicitly English falls back to French: the product is French
// first, and an unknown Accept-Language must not silently switch the report.
func TestNormalizeExplainLocaleDefaultsToFrench(t *testing.T) {
	for _, locale := range []string{"en", "EN", "en-US", " en-GB "} {
		if got := normalizeExplainLocale(locale); got != "en" {
			t.Fatalf("normalizeExplainLocale(%q) = %q, want en", locale, got)
		}
	}
	for _, locale := range []string{"", "fr", "fr-FR", "de", "es-ES"} {
		if got := normalizeExplainLocale(locale); got != "fr" {
			t.Fatalf("normalizeExplainLocale(%q) = %q, want fr", locale, got)
		}
	}
}

// The report concatenates this into a sentence, so a signal that already ends
// on punctuation must not gain a second full stop.
func TestEnsureSentenceAddsOnlyAMissingFullStop(t *testing.T) {
	cases := map[string]string{
		"CPU saturated":  "CPU saturated.",
		"CPU saturated.": "CPU saturated.",
		"Really?":        "Really?",
		"Wait!":          "Wait!",
		"Listing:":       "Listing:",
		"   ":            "",
	}
	for input, want := range cases {
		if got := ensureSentence(input); got != want {
			t.Fatalf("ensureSentence(%q) = %q, want %q", input, got, want)
		}
	}
}

// scopeText is what tells the operator where to look; a line number is only
// appended when there is a file to attach it to.
func TestScopeTextJoinsWhatIsKnown(t *testing.T) {
	cases := []struct {
		service, target string
		line            int
		want            string
	}{
		{"api", "main.go", 42, "api / main.go:42"},
		{"api", "main.go", 0, "api / main.go"},
		{"api", "", 42, "api"},
		{"", "main.go", 12, "main.go:12"},
		{"", "", 0, ""},
	}
	for _, c := range cases {
		if got := scopeText(c.service, c.target, c.line); got != c.want {
			t.Fatalf("scopeText(%q,%q,%d) = %q, want %q", c.service, c.target, c.line, got, c.want)
		}
	}
}

// A saturation report quotes the measured percentage back; losing it would turn
// "CPU 99.8%" into a vague "CPU high".
func TestExtractPercentFindsTheMeasuredValue(t *testing.T) {
	cases := map[string]string{
		"CPU usage is 99.86%":       "99.86%",
		"Disk usage is 90.05%.":     "90.05%",
		"usage (87%) above the cap": "87%",
		"connection refused":        "",
		"":                          "",
	}
	for message, want := range cases {
		if got := extractPercent(message); got != want {
			t.Fatalf("extractPercent(%q) = %q, want %q", message, got, want)
		}
	}
}

// The action bullet hands the operator a query they can paste; without a
// service it must still be a valid filter rather than a broken one.
func TestLogSearchForServiceAlwaysProducesAUsableQuery(t *testing.T) {
	if got := logSearchForService("checkout-api"); got != "service_name:checkout-api event.type:http_request" {
		t.Fatalf("unexpected query: %q", got)
	}
	if got := logSearchForService("   "); got != "event.type:http_request" {
		t.Fatalf("blank service must not produce service_name: %q", got)
	}
}

// The command must grep the actual port when the check target carries one,
// otherwise the operator gets an unfiltered listing on a busy host.
func TestListeningCommandNarrowsToTheCheckedPort(t *testing.T) {
	if got := listeningCommand("10.0.0.4:5432"); got != "ss -ltnp | grep 5432" {
		t.Fatalf("got %q", got)
	}
	if got := listeningCommand("db.internal"); got != "ss -ltnp" {
		t.Fatalf("got %q", got)
	}
}

// getent takes a hostname, not a URL or a host:port pair, so the target has to
// be reduced before it lands in a copy-pasteable command.
func TestTargetHostOnlyStripsSchemeAndPort(t *testing.T) {
	cases := map[string]string{
		"https://api.example.com/health": "api.example.com",
		"db.internal:5432":               "db.internal",
		"db.internal":                    "db.internal",
		"[::1]:5432":                     "::1",
	}
	for target, want := range cases {
		if got := targetHostOnly(target); got != want {
			t.Fatalf("targetHostOnly(%q) = %q, want %q", target, got, want)
		}
	}
}

func TestContainsString(t *testing.T) {
	if !containsString([]string{"a", "b"}, "b") {
		t.Fatal("b should be found")
	}
	if containsString([]string{"a", "b"}, "c") {
		t.Fatal("c should not be found")
	}
}

// The hint quotes the URL Go put in the error string; only real URLs qualify so
// a quoted word elsewhere in the message is never presented as a call target.
func TestQuotedURLOnlyAcceptsAnHTTPTarget(t *testing.T) {
	cases := map[string]string{
		`Get "https://api.example.com/health": dial tcp`: "https://api.example.com/health",
		`Get "http://x.local/": refused`:                 "http://x.local/",
		`unknown field "name"`:                           "",
		`missing closing quote "oops`:                    "",
		`no quotes at all`:                               "",
	}
	for message, want := range cases {
		if got := quotedURL(message); got != want {
			t.Fatalf("quotedURL(%q) = %q, want %q", message, got, want)
		}
	}
}

func TestHostFromURL(t *testing.T) {
	if got := hostFromURL("https://api.example.com:8443/health"); got != "api.example.com" {
		t.Fatalf("got %q", got)
	}
	if got := hostFromURL(""); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := hostFromURL("://nope"); got != "" {
		t.Fatalf("got %q", got)
	}
}

// A DNS failure without a URL still names the domain: Go puts it after
// "lookup", and that is the one identifier the operator needs.
func TestLookupHostFromMessage(t *testing.T) {
	cases := map[string]string{
		"dial tcp: lookup api.example.com: no such host": "api.example.com",
		"lookup api.example.com":                         "api.example.com",
		"connection refused":                             "",
	}
	for message, want := range cases {
		if got := lookupHostFromMessage(message); got != want {
			t.Fatalf("lookupHostFromMessage(%q) = %q, want %q", message, got, want)
		}
	}
}

func TestFirstTokenStripsPunctuation(t *testing.T) {
	cases := map[string]string{
		"useAuth must be used within an AuthProvider": "useAuth",
		"  useAuth: broken":                           "useAuth",
		"":                                            "",
	}
	for message, want := range cases {
		if got := firstToken(message); got != want {
			t.Fatalf("firstToken(%q) = %q, want %q", message, got, want)
		}
	}
}

func TestProviderNameFromMessage(t *testing.T) {
	if got := providerNameFromMessage("useAuth must be used within an AuthProvider."); got != "an AuthProvider" {
		t.Fatalf("got %q", got)
	}
	if got := providerNameFromMessage("nothing here"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestOptStatusMsg(t *testing.T) {
	if got := optStatusMsg("boom", " — "); got != " — boom" {
		t.Fatalf("got %q", got)
	}
	if got := optStatusMsg("  ", " — "); got != "" {
		t.Fatalf("blank status must add nothing, got %q", got)
	}
}

// A raw client error is not an explanation. Each recognized family must produce
// a cause sentence in both locales, and an unknown one must stay empty so the
// caller falls back to the raw message rather than inventing a cause.
func TestKnownErrorHintRecognizesTheCommonFamilies(t *testing.T) {
	cases := []struct {
		name    string
		message string
		frMatch string
		enMatch string
	}{
		{"dns with url", `Get "https://api.example.com/health": dial tcp: lookup api.example.com: no such host`, "introuvable (DNS)", "cannot be resolved (DNS)"},
		{"dns without url", "dial tcp: lookup api.example.com: no such host", "api.example.com", "api.example.com"},
		{"dns bare", "no such host", "DNS", "DNS"},
		{"refused with url", `Get "https://api.example.com/": dial tcp: connection refused`, "refuse la connexion", "refuses the connection"},
		{"refused bare", "dial tcp: connection refused", "refuse la connexion", "refuses the connection"},
		{"timeout with url", `Get "https://api.example.com/": context deadline exceeded`, "expire", "times out"},
		{"timeout bare", "i/o timeout", "ne répond pas à temps", "does not respond fast enough"},
		{"tls with url", `Get "https://api.example.com/": x509: certificate has expired`, "TLS/certificat", "TLS/certificate"},
		{"tls bare", "tls: handshake failure", "TLS/certificat", "TLS/certificate"},
		{"divide by zero", "runtime error: integer divide by zero", "Division par zéro", "Division by zero"},
		{"expired token", "jwt token is expired", "token d'authentification est expiré", "authentication token is expired"},
		{"provider", "useAuth must be used within an AuthProvider", "useAuth est utilisé en dehors", "useAuth is rendered outside"},
		{"provider unnamed", "Provider missing: must be used within ", "hook React", "React hook"},
		{"invalid hook", "Invalid hook call. Hooks can only be called inside", "hook React invalide", "Invalid React hook call"},
		{"undefined property", "Cannot read properties of undefined (reading 'id')", "valeur undefined", "undefined value"},
	}
	for _, c := range cases {
		e := slimError{Message: c.message}
		if got := knownErrorHint(e, "fr"); !strings.Contains(got, c.frMatch) {
			t.Fatalf("%s: fr hint %q does not contain %q", c.name, got, c.frMatch)
		}
		if got := knownErrorHint(e, "en"); !strings.Contains(got, c.enMatch) {
			t.Fatalf("%s: en hint %q does not contain %q", c.name, got, c.enMatch)
		}
	}

	// An unrecognized message must stay empty: renderRulesReport relies on that
	// to fall back to the raw message instead of asserting a wrong cause.
	if got := knownErrorHint(slimError{Message: "some domain-specific failure"}, "fr"); got != "" {
		t.Fatalf("unknown message produced a hint: %q", got)
	}
	if got := knownErrorHint(slimError{Message: "  "}, "fr"); got != "" {
		t.Fatalf("empty message produced a hint: %q", got)
	}
}

// Every check failure family the rules engine claims to know must resolve to a
// cause naming the target; anything else must return empty so the raw message
// is used instead.
func TestKnownServiceResultCauseCoversEachFamily(t *testing.T) {
	cases := []struct {
		name    string
		result  slimServiceResult
		frMatch string
		enMatch string
	}{
		{"dns", slimServiceResult{Message: "no such host", CheckTarget: "db.internal"}, "DNS introuvable pour db.internal", "DNS cannot resolve db.internal"},
		{"refused", slimServiceResult{Message: "connection refused", CheckTarget: "db:5432"}, "Connexion refusée par db:5432", "Connection refused by db:5432"},
		{"timeout", slimServiceResult{Message: "context deadline exceeded", CheckTarget: "api"}, "ne répond pas à temps", "does not respond in time"},
		{"tls", slimServiceResult{Message: "x509: certificate expired", CheckTarget: "api"}, "TLS/certificat", "TLS/certificate"},
		{"ping", slimServiceResult{Message: "ping failed", CheckType: "ping", CheckTarget: "host-1"}, "ne répond pas au ping", "does not answer ping"},
		{"icmp", slimServiceResult{Message: "unreachable", CheckType: "icmp", CheckTarget: "host-1"}, "ne répond pas au ping", "does not answer ping"},
	}
	for _, c := range cases {
		label, actions := knownServiceResultCause(c.result, "fr")
		if !strings.Contains(label, c.frMatch) {
			t.Fatalf("%s: fr label %q does not contain %q", c.name, label, c.frMatch)
		}
		if len(actions) == 0 {
			t.Fatalf("%s: a recognized cause must carry an action", c.name)
		}
		label, _ = knownServiceResultCause(c.result, "en")
		if !strings.Contains(label, c.enMatch) {
			t.Fatalf("%s: en label %q does not contain %q", c.name, label, c.enMatch)
		}
	}

	// A latency threshold breach is not a dead host: reporting "does not answer
	// ping" for a host that answers in 300ms would send the operator to the
	// wrong problem entirely.
	label, _ := knownServiceResultCause(slimServiceResult{
		Message: "latency 320ms above threshold", CheckType: "ping", CheckTarget: "host-1",
	}, "fr")
	if label != "" {
		t.Fatalf("latency warning was reported as a down host: %q", label)
	}

	// With no target, the service name is what the operator can act on.
	label, _ = knownServiceResultCause(slimServiceResult{Message: "connection refused", Service: "billing"}, "fr")
	if !strings.Contains(label, "billing") {
		t.Fatalf("service name must stand in for a missing target: %q", label)
	}
}

// The volume sentence is the evidence behind a spike claim: it has to state
// what was actually counted rather than assert a spike with no numbers.
func TestTrafficVolumeTextReportsWhatWasCounted(t *testing.T) {
	both := services.TrafficAnomalySignal{TraceCount: 1200, LogCount: 42}
	if got := trafficVolumeText(both, 20, "fr"); !strings.Contains(got, "1200 requêtes/traces et 42 logs en 20 min") {
		t.Fatalf("got %q", got)
	}
	if got := trafficVolumeText(both, 20, "en"); !strings.Contains(got, "1200 requests/traces and 42 logs in 20 min") {
		t.Fatalf("got %q", got)
	}

	traces := services.TrafficAnomalySignal{TraceCount: 900}
	if got := trafficVolumeText(traces, 20, "fr"); !strings.Contains(got, "900 requêtes/traces") {
		t.Fatalf("got %q", got)
	}
	if got := trafficVolumeText(traces, 20, "en"); !strings.Contains(got, "900 requests/traces") {
		t.Fatalf("got %q", got)
	}

	logs := services.TrafficAnomalySignal{LogCount: 5508}
	if got := trafficVolumeText(logs, 0, "fr"); !strings.Contains(got, "5508 logs/requêtes en 20 min") {
		t.Fatalf("a missing window must fall back to the default 20 min: %q", got)
	}
	if got := trafficVolumeText(logs, 30, "en"); !strings.Contains(got, "5508 logs/requests in 30 min") {
		t.Fatalf("got %q", got)
	}

	if got := trafficVolumeText(services.TrafficAnomalySignal{}, 20, "fr"); got != "trafic/logs anormaux" {
		t.Fatalf("got %q", got)
	}
	if got := trafficVolumeText(services.TrafficAnomalySignal{}, 20, "en"); got != "abnormal traffic/log volume" {
		t.Fatalf("got %q", got)
	}
}

func TestTrafficSignalSummaryNamesTheServiceWhenKnown(t *testing.T) {
	signal := services.TrafficAnomalySignal{Service: "checkout-api", TraceCount: 1200}
	if got := trafficSignalSummary(signal, 20, "fr"); !strings.HasPrefix(got, "checkout-api montre une activité anormale") {
		t.Fatalf("got %q", got)
	}
	if got := trafficSignalSummary(signal, 20, "en"); !strings.HasPrefix(got, "checkout-api shows abnormal activity") {
		t.Fatalf("got %q", got)
	}
	anonymous := services.TrafficAnomalySignal{TraceCount: 1200}
	if got := trafficSignalSummary(anonymous, 20, "fr"); !strings.HasPrefix(got, "Activité applicative anormale") {
		t.Fatalf("got %q", got)
	}
	if got := trafficSignalSummary(anonymous, 20, "en"); !strings.HasPrefix(got, "Abnormal application activity") {
		t.Fatalf("got %q", got)
	}
}

// A neighbour barely past its baseline is noise; the score has to grow with the
// multiplier and stay capped, or a x2 blip would rank like a x10 collapse.
func TestDegradedStrengthGrowsWithTheMultiplierAndStaysCapped(t *testing.T) {
	if got := degradedStrength(0.5); got != 0.5 {
		t.Fatalf("a multiplier at or below 1 is not evidence: %v", got)
	}
	weak, strong := degradedStrength(2), degradedStrength(7)
	if !(weak < strong) {
		t.Fatalf("x7 (%v) must outrank x2 (%v)", strong, weak)
	}
	if got := degradedStrength(100); got != 0.8 {
		t.Fatalf("degraded strength must stay capped at 0.8, got %v", got)
	}
}

// The CPU/disk saturation reports are the ones an operator acts on directly:
// they must name the suspect service, the measured value and a usable query.
func TestDeterministicAgentExplanationsNameSuspectAndCommand(t *testing.T) {
	result := slimServiceResult{Message: "CPU usage is 99.86%", CheckType: "agent_cpu"}
	signal := services.TrafficAnomalySignal{Service: "checkout-api", TraceCount: 1200, LogCount: 42}

	for _, locale := range []string{"fr", "en"} {
		got := deterministicAgentCPUExplanation(result, signal, "host-1", 20, locale)
		for _, want := range []string{"99.86%", "host-1", "checkout-api", "service_name:checkout-api event.type:http_request"} {
			if !strings.Contains(got, want) {
				t.Fatalf("cpu/%s: %q missing from %q", locale, want, got)
			}
		}
	}

	disk := slimServiceResult{Message: "Disk usage is 90.05%", CheckType: "agent_disk"}
	for _, locale := range []string{"fr", "en"} {
		got := deterministicAgentDiskExplanation(disk, signal, "host-1", 20, locale)
		for _, want := range []string{"90.05%", "host-1", "checkout-api"} {
			if !strings.Contains(got, want) {
				t.Fatalf("disk/%s: %q missing from %q", locale, want, got)
			}
		}
	}

	// Without a percentage in the message the sentence still has to read as a
	// sentence rather than expose an empty placeholder.
	noValue := deterministicAgentCPUExplanation(slimServiceResult{Message: "CPU saturated"}, signal, "host-1", 20, "en")
	if !strings.Contains(noValue, "CPU high on host-1") {
		t.Fatalf("got %q", noValue)
	}
	noValueFR := deterministicAgentDiskExplanation(slimServiceResult{Message: "disk saturated"}, signal, "host-1", 20, "fr")
	if !strings.Contains(noValueFR, "Disque haut sur host-1") {
		t.Fatalf("got %q", noValueFR)
	}
}

// Small models echo their own instructions back. Whatever leaks through, the
// operator must never see the prompt scaffolding in the answer.
func TestSanitizeLLMOutputStripsPromptScaffolding(t *testing.T) {
	for _, label := range []string{"Sortie :", "Output:", "CAS A —", "Réponse :", "Réponds :"} {
		got := sanitizeLLMOutput(label+" **Cause.**", nil)
		if got != "**Cause.**" {
			t.Fatalf("label %q survived: %q", label, got)
		}
	}

	withRules := "**Cause.**\n\n- Action : redémarrer.\nRègles :\n- ne jamais inventer"
	if got := sanitizeLLMOutput(withRules, nil); strings.Contains(got, "Règles") {
		t.Fatalf("rule block survived: %q", got)
	}

	if got := sanitizeLLMOutput("   ", nil); got != "" {
		t.Fatalf("got %q", got)
	}
}

// Suggesting the very check that raised the alert is the classic useless
// bullet: the operator already has that check.
func TestSanitizeLLMOutputDropsTheCheckThatAlreadyFired(t *testing.T) {
	raw := map[string]any{
		"service_result": slimServiceResult{CheckType: "agent_disk", Service: "disk"},
	}
	out := sanitizeLLMOutput("**Disque plein.**\n\n- Ajouter un check agent_disk sur ce host.\n- Nettoyer /var/log.", raw)
	if strings.Contains(out, "Ajouter un check agent_disk") {
		t.Fatalf("duplicate check suggestion survived: %q", out)
	}
	if !strings.Contains(out, "Nettoyer /var/log") {
		t.Fatalf("the useful action was dropped: %q", out)
	}

	// Without a service_result in context there is nothing to compare against.
	lines := []string{"- Ajouter un check agent_disk"}
	if got := dropDuplicateCheckSuggestion(lines, nil); len(got) != 1 {
		t.Fatalf("no context must leave the lines untouched: %v", got)
	}
	if got := dropDuplicateCheckSuggestion(lines, map[string]any{}); len(got) != 1 {
		t.Fatalf("no service_result must leave the lines untouched: %v", got)
	}
}

// Each subject type gets its own flat prompt; a shared multi-branch prompt is
// what small models parrot back, so the branches must not collapse.
func TestSystemPromptForIsSpecificPerSubjectAndLocale(t *testing.T) {
	seen := map[string]bool{}
	for _, subject := range []string{"rca_dossier", "error", "service_result", "unknown"} {
		for _, locale := range []string{"fr", "en"} {
			prompt := systemPromptFor(subject, locale)
			if strings.TrimSpace(prompt) == "" {
				t.Fatalf("%s/%s produced an empty prompt", subject, locale)
			}
			seen[prompt] = true
		}
	}
	// fr and en differ for the three real subjects, and the default is shared.
	if len(seen) != 7 {
		t.Fatalf("expected 7 distinct prompts, got %d", len(seen))
	}
}

func TestPromptUserPrefixFollowsTheLocale(t *testing.T) {
	if got := promptUserPrefix("en-US"); got != "data:\n" {
		t.Fatalf("got %q", got)
	}
	if got := promptUserPrefix(""); got != "données:\n" {
		t.Fatalf("got %q", got)
	}
}

// The prompt payload pays per token: heavy fields are dropped and the long
// lists truncated, but every correlation-relevant key has to survive.
func TestSlimRawDataKeepsSignalsAndTruncatesTheRest(t *testing.T) {
	if got := slimRawData("not a map"); got != nil {
		t.Fatalf("a non-map context must slim to nil, got %v", got)
	}

	traffic := &services.TrafficAnalysis{
		WindowMinutes: 20,
		Summary:       []string{"heavy summary"},
		Signals: []services.TrafficAnomalySignal{
			{Service: "a"}, {Service: "b"}, {Service: "c"},
		},
	}
	trace := &services.TraceSummary{
		TraceID: "t1",
		ErrorSpans: []services.TraceSpanSummary{
			{ServiceName: "a"}, {ServiceName: "b"}, {ServiceName: "c"}, {ServiceName: "d"},
		},
	}
	raw := map[string]any{
		"error":                slimError{Name: "panic"},
		"correlation":          &slimCorrelation{HasCorrelation: true},
		"recent_events":        []slimEvent{{Type: "deploy"}},
		"other_checks_on_host": []slimCheck{},
		"traffic_anomalies":    traffic,
		"trace":                trace,
		"ignored":              "dropped",
	}

	out := slimRawData(raw)
	for _, key := range []string{"error", "correlation", "recent_events", "other_checks_on_host", "traffic_anomalies", "trace_error_spans"} {
		if _, ok := out[key]; !ok {
			t.Fatalf("%s was dropped from the prompt payload", key)
		}
	}
	if _, ok := out["ignored"]; ok {
		t.Fatal("an unlisted key reached the prompt")
	}

	slimTraffic := out["traffic_anomalies"].(*services.TrafficAnalysis)
	if slimTraffic.Summary != nil {
		t.Fatal("the verbose summary must not be paid for twice")
	}
	if len(slimTraffic.Signals) != 2 {
		t.Fatalf("signals must be capped at 2, got %d", len(slimTraffic.Signals))
	}
	if traffic.Summary == nil || len(traffic.Signals) != 3 {
		t.Fatal("slimming must not mutate the caller's analysis")
	}
	if spans := out["trace_error_spans"].([]services.TraceSpanSummary); len(spans) != 3 {
		t.Fatalf("error spans must be capped at 3, got %d", len(spans))
	}
}

// A dossier with no signal at all is worse than no dossier: it would make the
// caller prompt an LLM on an empty context.
func TestNonEmptyDossierRejectsASilentDossier(t *testing.T) {
	if got := nonEmptyDossier(nil); got != nil {
		t.Fatal("nil in, nil out")
	}
	if got := nonEmptyDossier(&rcaDossier{Symptom: "boom"}); got != nil {
		t.Fatal("a dossier with neither signal nor candidate must be dropped")
	}
	d := &rcaDossier{Candidates: []rcaCandidate{{Confidence: 0.4}, {Confidence: 0.9}}}
	got := nonEmptyDossier(d)
	if got == nil || got.Candidates[0].Confidence != 0.9 {
		t.Fatalf("candidates must come back ranked: %+v", got)
	}
}

// The LLM is only worth its latency when the ranking is genuinely ambiguous.
func TestShouldUseLLMOnlyWhenTheRankingIsAmbiguous(t *testing.T) {
	if shouldUseLLMForDossier(nil) {
		t.Fatal("no dossier, no call")
	}
	if shouldUseLLMForDossier(&rcaDossier{}) {
		t.Fatal("no candidate, no call")
	}
	certain := &rcaDossier{Candidates: []rcaCandidate{{Confidence: 0.95}, {Confidence: 0.5}}}
	if shouldUseLLMForDossier(certain) {
		t.Fatal("a cause that states itself must not pay for an LLM call")
	}
	single := &rcaDossier{Candidates: []rcaCandidate{{Confidence: 0.7}}}
	if shouldUseLLMForDossier(single) {
		t.Fatal("one candidate and no corroborating signal is not ambiguous")
	}
	ambiguous := &rcaDossier{Candidates: []rcaCandidate{{Confidence: 0.7}, {Confidence: 0.6}}}
	if !shouldUseLLMForDossier(ambiguous) {
		t.Fatal("two competing candidates is exactly the case the LLM is for")
	}
}

// A recognized check failure is stated with its actions; an unrecognized one
// must leave the dossier untouched rather than add an empty candidate.
func TestAddServiceResultCandidate(t *testing.T) {
	d := &rcaDossier{}
	addServiceResultCandidate(d, slimServiceResult{Message: "connection refused", CheckTarget: "db:5432"}, "fr")
	if len(d.Candidates) != 1 || d.Candidates[0].kind != "known" {
		t.Fatalf("expected one known candidate, got %+v", d.Candidates)
	}
	if len(d.StrongSignals) != 1 {
		t.Fatalf("the cause must also be listed as a strong signal: %v", d.StrongSignals)
	}

	unknown := &rcaDossier{}
	addServiceResultCandidate(unknown, slimServiceResult{Message: "something bespoke"}, "fr")
	if len(unknown.Candidates) != 0 || len(unknown.StrongSignals) != 0 {
		t.Fatalf("an unrecognized message must add nothing: %+v", unknown)
	}
}

// An agent check has no per-process metric, and that gap must be stated rather
// than left for the reader to assume it was analyzed.
func TestAddMissingSignalsDeclaresWhatWasNotAnalyzed(t *testing.T) {
	d := &rcaDossier{}
	addMissingSignals(d, "service_result", map[string]any{"service_result": slimServiceResult{CheckType: "agent_cpu"}}, "en")
	if len(d.MissingSignals) != 2 {
		t.Fatalf("expected the traffic gap and the process gap, got %v", d.MissingSignals)
	}

	withTraffic := &rcaDossier{}
	addMissingSignals(withTraffic, "error", map[string]any{
		"traffic_anomalies": &services.TrafficAnalysis{},
	}, "fr")
	if len(withTraffic.MissingSignals) != 0 {
		t.Fatalf("an analyzed window has no gap to declare: %v", withTraffic.MissingSignals)
	}
}

// A dossier only exists for the two subject types the engine understands, and
// only when the context actually carries that subject.
func TestBuildRCADossierRejectsWhatItCannotAnalyze(t *testing.T) {
	if got := buildRCADossier("error", "not a map", "fr"); got != nil {
		t.Fatal("a non-map context must not produce a dossier")
	}
	if got := buildRCADossier("unknown", map[string]any{}, "fr"); got != nil {
		t.Fatal("an unknown subject type must not produce a dossier")
	}
	if got := buildRCADossier("error", map[string]any{}, "fr"); got != nil {
		t.Fatal("a context without the error must not produce a dossier")
	}
	if got := buildRCADossier("service_result", map[string]any{}, "fr"); got != nil {
		t.Fatal("a context without the result must not produce a dossier")
	}

	d := buildRCADossier("service_result", map[string]any{
		"service_result": slimServiceResult{
			CheckType: "tcp", CheckTarget: "db:5432", Service: "api", Message: "connection refused",
		},
	}, "fr")
	if d == nil || !strings.Contains(d.Symptom, "tcp db:5432") {
		t.Fatalf("unexpected dossier: %+v", d)
	}
}

// The failing span pinpoints where the request actually broke, which outranks
// any correlation; a trace without an error span says nothing.
func TestAddTraceCandidatePinpointsTheFailingSpan(t *testing.T) {
	d := &rcaDossier{}
	addTraceCandidate(d, map[string]any{}, "fr")
	if len(d.Candidates) != 0 {
		t.Fatal("no trace, no candidate")
	}
	addTraceCandidate(d, map[string]any{"trace": &services.TraceSummary{TraceID: "t1"}}, "fr")
	if len(d.Candidates) != 0 {
		t.Fatal("a trace without a failing span is not a cause")
	}

	trace := &services.TraceSummary{
		TraceID:    "t1",
		ErrorSpans: []services.TraceSpanSummary{{ServiceName: "billing", Operation: "POST /charge", StatusCode: "ERROR", StatusMsg: "boom"}},
	}
	for _, locale := range []string{"fr", "en"} {
		got := &rcaDossier{}
		addTraceCandidate(got, map[string]any{"trace": trace}, locale)
		if len(got.Candidates) != 1 || got.Candidates[0].kind != "trace" {
			t.Fatalf("%s: expected a trace candidate, got %+v", locale, got.Candidates)
		}
		if !strings.Contains(got.Candidates[0].Label, "billing / POST /charge") {
			t.Fatalf("%s: the span must be named: %q", locale, got.Candidates[0].Label)
		}
	}

	// Without an operation the service alone still locates the failure.
	noOp := &rcaDossier{}
	addTraceCandidate(noOp, map[string]any{
		"trace": &services.TraceSummary{TraceID: "t2", ErrorSpans: []services.TraceSpanSummary{{ServiceName: "billing"}}},
	}, "fr")
	if !strings.Contains(noOp.Candidates[0].Label, "billing") {
		t.Fatalf("got %q", noOp.Candidates[0].Label)
	}
}

// A known error message is the strongest signal there is: it must be stated as
// a near-certain candidate carrying the scope the operator needs.
func TestAddKnownErrorCandidate(t *testing.T) {
	d := &rcaDossier{}
	addKnownErrorCandidate(d, slimError{Message: "runtime error: integer divide by zero", File: "main.go", Line: 12, Service: "api"}, nil, "en")
	if len(d.Candidates) != 1 || d.Candidates[0].Confidence != 0.95 {
		t.Fatalf("unexpected candidate: %+v", d.Candidates)
	}
	if !containsString(d.Candidates[0].Evidence, "api / main.go:12") {
		t.Fatalf("the scope must travel with the candidate: %v", d.Candidates[0].Evidence)
	}

	unknown := &rcaDossier{}
	addKnownErrorCandidate(unknown, slimError{Message: "bespoke failure"}, nil, "fr")
	if len(unknown.Candidates) != 0 {
		t.Fatal("an unrecognized message must not become a 0.95 candidate")
	}
}

// The FR report is the default; both locales must state the cause in bold and
// carry the scope, or the frontend renders a bare sentence.
func TestRenderRulesReportIsBilingualAndScoped(t *testing.T) {
	ctxMap := map[string]any{
		"error": slimError{Name: "panic", Message: "runtime error: integer divide by zero", File: "main.go", Line: 295, Service: "api"},
	}
	for _, locale := range []string{"fr", "en"} {
		got := renderRulesReport("error", ctxMap, locale)
		if !strings.HasPrefix(got, "**") || !strings.Contains(got, "api / main.go:295") {
			t.Fatalf("%s: unexpected report %q", locale, got)
		}
	}

	if got := renderRulesReport("unknown", ctxMap, "fr"); got != "" {
		t.Fatalf("an unknown subject has no report: %q", got)
	}
	if got := renderRulesReport("error", map[string]any{}, "fr"); got != "" {
		t.Fatalf("no subject, no report: %q", got)
	}
	if got := renderRulesReport("service_result", map[string]any{}, "fr"); got != "" {
		t.Fatalf("no subject, no report: %q", got)
	}

	// A check failure with neither a known cause nor a message still has to name
	// what failed rather than render an empty bold sentence.
	bare := map[string]any{"service_result": slimServiceResult{CheckType: "http", CheckTarget: "https://x/health", Service: "api"}}
	for _, locale := range []string{"fr", "en"} {
		got := renderRulesReport("service_result", bare, locale)
		if !strings.Contains(got, "https://x/health") {
			t.Fatalf("%s: %q", locale, got)
		}
	}
}

// The correlation line is capped at one sentence and ordered by causal
// strength; a deploy right before the incident outranks a traffic spike.
func TestCorrelationLineRanksEventsAboveTraffic(t *testing.T) {
	at := time.Date(2026, 9, 2, 14, 30, 0, 0, time.UTC)
	ctxMap := map[string]any{
		"recent_events":     []slimEvent{{Type: "noise"}, {Type: "deployment", Timestamp: at}},
		"traffic_anomalies": &services.TrafficAnalysis{WindowMinutes: 20, Signals: []services.TrafficAnomalySignal{{Service: "api", TraceCount: 10}}},
	}
	if got := correlationLine(ctxMap, false); !strings.Contains(got, "événement deployment à 14:30") {
		t.Fatalf("got %q", got)
	}
	if got := correlationLine(ctxMap, true); !strings.Contains(got, "deployment event at 14:30") {
		t.Fatalf("got %q", got)
	}

	// With no event left, the traffic spike is what remains to be reported.
	trafficOnly := map[string]any{"traffic_anomalies": ctxMap["traffic_anomalies"]}
	if got := correlationLine(trafficOnly, false); !strings.HasPrefix(got, "Corrélation possible : api") {
		t.Fatalf("got %q", got)
	}
	if got := correlationLine(trafficOnly, true); !strings.HasPrefix(got, "Possible correlation: api") {
		t.Fatalf("got %q", got)
	}

	// A declared dependency erroring first ranks with the upstream family.
	upstreamApp := map[string]any{"correlation": &slimCorrelation{UpstreamApps: []string{"billing (http, x3)"}}}
	if got := correlationLine(upstreamApp, false); !strings.Contains(got, "la dépendance billing") {
		t.Fatalf("got %q", got)
	}
	if got := correlationLine(upstreamApp, true); !strings.Contains(got, "dependency billing") {
		t.Fatalf("got %q", got)
	}

	infra := map[string]any{"correlation": &slimCorrelation{InfraSignals: []string{"CPU at 98% on host-1"}}}
	if got := correlationLine(infra, true); got != "Possible correlation: CPU at 98% on host-1." {
		t.Fatalf("got %q", got)
	}
	if got := correlationLine(infra, false); !strings.HasSuffix(got, "host-1.") {
		t.Fatalf("got %q", got)
	}

	newError := map[string]any{"correlation": &slimCorrelation{RecurrenceIsNew: true}}
	if got := correlationLine(newError, true); !strings.Contains(got, "First occurrence") {
		t.Fatalf("got %q", got)
	}
	if got := correlationLine(newError, false); !strings.Contains(got, "Première occurrence") {
		t.Fatalf("got %q", got)
	}

	if got := correlationLine(map[string]any{}, false); got != "" {
		t.Fatalf("no signal, no line: %q", got)
	}
}

// The deterministic path short-circuits the LLM, so it must only fire on a
// subject and message it genuinely recognizes.
func TestDeterministicKnownExplanation(t *testing.T) {
	if got := deterministicKnownExplanation("error", "not a map", "fr"); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := deterministicKnownExplanation("incident", map[string]any{}, "fr"); got != "" {
		t.Fatalf("an unsupported subject must not short-circuit: %q", got)
	}
	if got := deterministicKnownExplanation("error", map[string]any{}, "fr"); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := deterministicKnownExplanation("error", map[string]any{"error": slimError{Message: "bespoke"}}, "fr"); got != "" {
		t.Fatalf("an unknown message must go to the LLM, not to a made-up answer: %q", got)
	}

	ctxMap := map[string]any{"error": slimError{
		Message: "runtime error: integer divide by zero", File: "main.go", Line: 295, Service: "api",
	}}
	if got := deterministicKnownExplanation("error", ctxMap, "en"); got != "**Division by zero in the code** — main.go:295 in api." {
		t.Fatalf("got %q", got)
	}
	if got := deterministicKnownExplanation("error", ctxMap, "fr"); !strings.Contains(got, "main.go:295 dans api.") {
		t.Fatalf("got %q", got)
	}
}

// Check failures the engine recognizes get a full report with the command to
// run; the target is what every bullet is built around.
func TestDeterministicServiceResultExplanationCoversTheKnownFamilies(t *testing.T) {
	if got := deterministicServiceResultExplanation(map[string]any{}, "fr"); got != "" {
		t.Fatalf("got %q", got)
	}

	cases := []struct {
		message string
		frMatch string
		enMatch string
	}{
		{"dial tcp: connection refused", "refuse la connexion", "refuses the connection"},
		{"lookup db.internal: no such host", "DNS ne résout pas", "DNS cannot resolve"},
		{"context deadline exceeded", "ne répond pas avant le timeout", "does not answer before timeout"},
	}
	for _, c := range cases {
		ctxMap := map[string]any{"service_result": slimServiceResult{
			Service: "api", CheckType: "tcp", CheckTarget: "db.internal:5432", Message: c.message,
		}}
		if got := deterministicServiceResultExplanation(ctxMap, "fr"); !strings.Contains(got, c.frMatch) {
			t.Fatalf("fr %q: %q", c.message, got)
		}
		if got := deterministicServiceResultExplanation(ctxMap, "en"); !strings.Contains(got, c.enMatch) {
			t.Fatalf("en %q: %q", c.message, got)
		}
	}

	// A blank message carries no cause, and an unrecognized one is not a check
	// failure family we can act on.
	blank := map[string]any{"service_result": slimServiceResult{Service: "api", Message: "  "}}
	if got := deterministicKnownServiceResultExplanation(blank, slimServiceResult{Message: "  "}, "fr"); got != "" {
		t.Fatalf("got %q", got)
	}
}

// A disk or CPU alert without a traffic suspect still has to hand over the
// commands that find the culprit locally.
func TestDeterministicSaturationReportsFallBackToLocalCommands(t *testing.T) {
	cpu := map[string]any{"service_result": slimServiceResult{
		Service: "cpu", CheckType: "agent_cpu", CheckTarget: "host-1", Message: "CPU usage is 99.86%",
	}}
	if got := deterministicServiceResultExplanation(cpu, "en"); !strings.Contains(got, "no abnormal traffic detected") || !strings.Contains(got, "`top`") {
		t.Fatalf("got %q", got)
	}
	if got := deterministicServiceResultExplanation(cpu, "fr"); !strings.Contains(got, "aucun trafic anormal détecté") {
		t.Fatalf("got %q", got)
	}

	disk := map[string]any{"service_result": slimServiceResult{
		Service: "disk", CheckType: "agent_disk", CheckTarget: "host-1", Message: "Disk usage is 90.05%",
	}}
	if got := deterministicServiceResultExplanation(disk, "en"); !strings.Contains(got, "Disk 90.05% on host-1") || !strings.Contains(got, "df -h") {
		t.Fatalf("got %q", got)
	}
	if got := deterministicServiceResultExplanation(disk, "fr"); !strings.Contains(got, "Disque 90.05% sur host-1") {
		t.Fatalf("got %q", got)
	}

	// With no value and no target, the report still names something actionable.
	bare := map[string]any{"service_result": slimServiceResult{Service: "disk", CheckType: "agent_disk"}}
	if got := deterministicServiceResultExplanation(bare, "en"); !strings.Contains(got, "Disk full on disk") {
		t.Fatalf("got %q", got)
	}
	if got := deterministicServiceResultExplanation(bare, "fr"); !strings.Contains(got, "Disque plein sur disk") {
		t.Fatalf("got %q", got)
	}

	// An unknown agent check has no deterministic story to tell.
	other := map[string]any{"service_result": slimServiceResult{CheckType: "agent_mem", Service: "mem"}}
	if got := deterministicServiceResultExplanation(other, "fr"); got != "" {
		t.Fatalf("got %q", got)
	}
}

// The impact clause is what turns a single failing check into an outage
// summary; it must include the current service and stay bounded.
func TestServiceImpactTextListsAtMostThreeServices(t *testing.T) {
	if got := serviceImpactText(map[string]any{}, "api", "fr"); got != "" {
		t.Fatalf("no correlation, no impact: %q", got)
	}
	ctxMap := map[string]any{"correlation": &slimCorrelation{
		FailingNeighbours: []string{"worker", "receiver", "billing"},
	}}
	got := serviceImpactText(ctxMap, "api", "fr")
	if !strings.Contains(got, "api, worker, receiver") || strings.Contains(got, "billing") {
		t.Fatalf("got %q", got)
	}
	if got := serviceImpactText(ctxMap, "worker", "en"); !strings.Contains(got, "impact: worker, receiver, billing") {
		t.Fatalf("the current service must not be listed twice: %q", got)
	}
}

// JSON, streaming and Goose paths share one budget; an unset or invalid value must not silently stretch it.
func TestExplainTimeoutDefaultsTo60sAndHonoursOverride(t *testing.T) {
	cases := map[string]time.Duration{
		"":    60 * time.Second,
		"0":   60 * time.Second,
		"-5":  60 * time.Second,
		"90s": 60 * time.Second,
		"150": 150 * time.Second,
	}
	for raw, want := range cases {
		t.Setenv("EXPLAIN_TIMEOUT_SECONDS", raw)
		if got := explainTimeout(); got != want {
			t.Errorf("EXPLAIN_TIMEOUT_SECONDS=%q: got %s, want %s", raw, got, want)
		}
	}
}
