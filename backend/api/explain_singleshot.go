package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"middle-monitor/backend/services"
)

// explainSingleShot fetches the minimal useful context from the DB in a couple
// of SQL round-trips, stuffs everything in a single prompt, and makes ONE
// non-streaming `chat/completions` call to the LLM (EXPLAIN_MODE=single_shot).
func explainSingleShot(ctx context.Context, db *sql.DB, orgID int64, subjectType string, subjectID int64, locale string) (content, model string, durationMS int, err error) {
	locale = normalizeExplainLocale(locale)
	cctx, cancel := context.WithTimeout(ctx, explainTimeout())
	defer cancel()

	correlationSvc := services.NewCorrelationService(db)
	trafficAnalyzer := services.NewTrafficAnalyzer(db, opensearch)

	var rawContext any
	switch subjectType {
	case "error":
		ctxData, buildErr := buildErrorContext(cctx, db, correlationSvc, trafficAnalyzer, orgID, subjectID)
		if buildErr != nil {
			return "", "", 0, buildErr
		}
		rawContext = ctxData
	case "service_result":
		ctxData, buildErr := buildServiceResultContext(cctx, db, correlationSvc, trafficAnalyzer, orgID, subjectID)
		if buildErr != nil {
			return "", "", 0, buildErr
		}
		rawContext = ctxData
	default:
		return "", "", 0, &SubjectTypeError{SubjectType: subjectType}
	}

	dossier := buildRCADossier(subjectType, rawContext, locale)

	if content := deterministicKnownExplanation(subjectType, rawContext, locale); content != "" {
		return content, "rules", 0, nil
	}

	promptSubject := "rca_dossier"
	// Slim raw_data: enough signals to confirm or rule out a correlation,
	// without the token cost of the full raw context.
	promptPayload := map[string]any{
		"dossier":  dossier,
		"raw_data": slimRawData(rawContext),
	}
	contextJSON, _ := json.Marshal(promptPayload)

	system := systemPromptFor(promptSubject, locale)
	user := promptUserPrefix(locale) + string(contextJSON)

	start := time.Now()
	completion, callErr := callChatCompletion(cctx, system, user)
	durationMS = int(time.Since(start).Milliseconds())
	if callErr != nil {
		return "", "", durationMS, callErr
	}

	content = sanitizeLLMOutput(extractExplanation(completion), rawContext)
	if strings.TrimSpace(content) == "" {
		return "", "", durationMS, &LLMEmptyError{Provider: "LLM", Detail: "raw: " + truncate(completion, 300)}
	}
	model = envOr("OPENAI_MODEL", "")
	return content, model, durationMS, nil
}

// explainTimeout bounds one LLM call; EXPLAIN_TIMEOUT_SECONDS overrides it.
func explainTimeout() time.Duration {
	if n, err := strconv.Atoi(os.Getenv("EXPLAIN_TIMEOUT_SECONDS")); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 60 * time.Second
}

func normalizeExplainLocale(locale string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "en") {
		return "en"
	}
	return "fr"
}

// explainSingleShotStream provides the same logic as explainSingleShot but streams the content back.
func explainSingleShotStream(ctx context.Context, db *sql.DB, orgID int64, subjectType string, subjectID int64, locale string, chunkChan chan<- string) (content, model string, durationMS int, err error) {
	defer close(chunkChan)
	locale = normalizeExplainLocale(locale)

	correlationSvc := services.NewCorrelationService(db)
	trafficAnalyzer := services.NewTrafficAnalyzer(db, opensearch)

	var rawContext any
	if subjectType == "error" {
		rawContext, err = buildErrorContext(ctx, db, correlationSvc, trafficAnalyzer, orgID, subjectID)
	} else if subjectType == "service_result" {
		rawContext, err = buildServiceResultContext(ctx, db, correlationSvc, trafficAnalyzer, orgID, subjectID)
	} else {
		return "", "", 0, &SubjectTypeError{SubjectType: subjectType}
	}
	if err != nil {
		return "", "", 0, err
	}

	dossier := buildRCADossier(subjectType, rawContext, locale)
	if !shouldUseLLMForDossier(dossier) {
		if content := deterministicKnownExplanation(subjectType, rawContext, locale); content != "" {
			chunkChan <- content
			return content, "deterministic", 0, nil
		}
	}

	cctx, cancel := context.WithTimeout(ctx, explainTimeout())
	defer cancel()

	promptSubject := "rca_dossier"
	// Slim raw_data: enough signals to confirm or rule out a correlation,
	// without the token cost of the full raw context.
	promptPayload := map[string]any{
		"dossier":  dossier,
		"raw_data": slimRawData(rawContext),
	}
	contextJSON, _ := json.Marshal(promptPayload)

	system := systemPromptFor(promptSubject, locale)
	user := promptUserPrefix(locale) + string(contextJSON)

	start := time.Now()
	content, callErr := streamChatCompletion(cctx, system, user, chunkChan)
	durationMS = int(time.Since(start).Milliseconds())
	if callErr != nil {
		return "", "", durationMS, callErr
	}

	content = sanitizeLLMOutput(content, rawContext)
	if strings.TrimSpace(content) == "" {
		return "", "", durationMS, &LLMEmptyError{Provider: "LLM"}
	}
	chunkChan <- content
	model = envOr("OPENAI_MODEL", "")
	return content, model, durationMS, nil
}
