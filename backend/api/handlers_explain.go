package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"middle-monitor/backend/middleware"
)

// Shape returned by POST /errors/{id}/explain and /services/results/{id}/explain.
type ExplanationResponse struct {
	SubjectType string    `json:"subject_type"`
	SubjectID   int64     `json:"subject_id"`
	Content     string    `json:"content"`
	Model       string    `json:"model,omitempty"`
	DurationMS  int       `json:"duration_ms,omitempty"`
	Cached      bool      `json:"cached"`
	CreatedAt   time.Time `json:"created_at"`
}

// handleExplainError explains an application error and caches the markdown
// result in the `explanations` table.
func handleExplainError(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		vars := mux.Vars(r)
		errorID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrErrorIDInvalid)
			return
		}

		var exists bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM application_errors WHERE id = $1 AND organization_id = $2)`, errorID, orgID).Scan(&exists); err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if !exists {
			respondError(w, http.StatusNotFound, ErrErrorNotFound)
			return
		}

		force := r.URL.Query().Get("force") == "true"
		runExplain(w, r, db, orgID, "error", errorID, force)
	}
}

// handleExplainServiceResult does the same for a failing service_result.
func handleExplainServiceResult(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		vars := mux.Vars(r)
		resultID, err := strconv.ParseInt(vars["resultId"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrResultIDInvalid)
			return
		}

		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS(
				SELECT 1 FROM service_results sr
				JOIN services s ON s.id = sr.service_id
				WHERE sr.id = $1 AND s.organization_id = $2
			)`, resultID, orgID).Scan(&exists); err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if !exists {
			respondError(w, http.StatusNotFound, ErrServiceResultNotFound)
			return
		}

		force := r.URL.Query().Get("force") == "true"
		runExplain(w, r, db, orgID, "service_result", resultID, force)
	}
}

// flushSSE flushes when possible. Instrumentation middlewares wrap the writer
// and may not expose http.Flusher: buffered frames are still sent on return.
func flushSSE(w http.ResponseWriter) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// runExplain handles the shared logic: cache lookup, explain, persist.
func runExplain(w http.ResponseWriter, r *http.Request, db *sql.DB, orgID int64, subjectType string, subjectID int64, force bool) {
	isSSE := strings.Contains(r.Header.Get("Accept"), "text/event-stream")
	if isSSE {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
	} else {
		w.Header().Set("Content-Type", "application/json")
	}

	locale := explainLocale(r)

	// Default to the rules-only engine: instant, free, no GPU/LLM required.
	// The LLM-based single_shot mode is available via EXPLAIN_MODE.
	mode := strings.ToLower(envOr("EXPLAIN_MODE", "rules"))

	// The cache table is currently language-agnostic. Keep using it for the
	// historical/default French path, but bypass it for English so users don't
	// receive a cached French explanation.
	if !force && locale == "fr" {
		var resp ExplanationResponse
		var model sql.NullString
		var duration sql.NullInt64
		err := db.QueryRow(`
			SELECT subject_type, subject_id, content, model, duration_ms, created_at
			FROM explanations
			WHERE organization_id = $1 AND subject_type = $2 AND subject_id = $3
		`, orgID, subjectType, subjectID).Scan(&resp.SubjectType, &resp.SubjectID, &resp.Content, &model, &duration, &resp.CreatedAt)
		if err == nil {
			if isMalformedExplanation(resp.Content) || isStaleRulesFormat(mode, resp.Content) {
				_, _ = db.Exec(`
					DELETE FROM explanations
					WHERE organization_id = $1 AND subject_type = $2 AND subject_id = $3
				`, orgID, subjectType, subjectID)
			} else {
				resp.Cached = true
				if model.Valid {
					resp.Model = model.String
				}
				if duration.Valid {
					resp.DurationMS = int(duration.Int64)
				}

				if isSSE {
					metaBytes, _ := json.Marshal(resp)
					fmt.Fprintf(w, "event: metadata\ndata: %s\n\n", metaBytes)
					chunkBytes, _ := json.Marshal(resp.Content)
					fmt.Fprintf(w, "data: %s\n\n", chunkBytes)
					fmt.Fprintf(w, "data: [DONE]\n\n")
					flushSSE(w)
					return
				}

				_ = json.NewEncoder(w).Encode(resp)
				return
			}
		} else if err != sql.ErrNoRows {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
	}

	if isSSE && (mode == "single_shot" || mode == "singleshot" || mode == "direct") {
		flusher, ok := w.(http.Flusher)
		if ok {
			chunkChan := make(chan string, 100)

			initialMeta := ExplanationResponse{
				SubjectType: subjectType,
				SubjectID:   subjectID,
				Cached:      false,
				CreatedAt:   time.Now().UTC(),
			}
			metaBytes, _ := json.Marshal(initialMeta)
			fmt.Fprintf(w, "event: metadata\ndata: %s\n\n", metaBytes)
			flusher.Flush()

			var finalContent, finalModel string
			var finalDuration int
			var finalErr error

			done := make(chan struct{})
			go func() {
				defer close(done)
				finalContent, finalModel, finalDuration, finalErr = explainSingleShotStream(r.Context(), db, orgID, subjectType, subjectID, locale, chunkChan)
			}()

			for chunk := range chunkChan {
				chunkBytes, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\n", chunkBytes)
				flusher.Flush()
			}

			<-done

			if finalErr != nil {
				slog.Error("explain stream failed", "error", finalErr)
				errBytes, _ := json.Marshal(finalErr.Error())
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", errBytes)
				flusher.Flush()
				return
			}

			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()

			if locale == "fr" && finalContent != "" {
				go func(org int64, sType string, sID int64, content, model string, duration int) {
					_, err := db.Exec(`
						INSERT INTO explanations (organization_id, subject_type, subject_id, content, model, duration_ms, created_at)
						VALUES ($1, $2, $3, $4, $5, $6, NOW())
						ON CONFLICT (subject_type, subject_id)
						DO UPDATE SET content = EXCLUDED.content, model = EXCLUDED.model, duration_ms = EXCLUDED.duration_ms, created_at = NOW()
					`, org, sType, sID, content, model, duration)
					if err != nil {
						slog.Error("failed to persist explanation after stream", "error", err)
					}
				}(orgID, subjectType, subjectID, finalContent, finalModel, finalDuration)
			}
			return
		}
	}

	var (
		content    string
		model      string
		durationMS int
		err        error
	)
	switch mode {
	case "single_shot", "singleshot", "direct":
		content, model, durationMS, err = explainSingleShot(r.Context(), db, orgID, subjectType, subjectID, locale)
	case "rules", "local", "offline", "":
		content, model, durationMS, err = explainRulesOnly(r.Context(), db, orgID, subjectType, subjectID, locale)
	default:
		err = &ExplainModeError{Mode: mode}
	}

	if err != nil {
		slog.Error("explain failed", "mode", mode, "org_id", orgID, "subject_type", subjectType, "subject_id", subjectID, "error", err)
		if isSSE {
			errBytes, _ := json.Marshal(err.Error())
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", errBytes)
			flushSSE(w)
		} else {
			respondError(w, http.StatusBadGateway, fmt.Errorf("%w: %w", ErrExplanationFailed, err))
		}
		return
	}

	if locale == "fr" {
		_, err = db.Exec(`
			INSERT INTO explanations (organization_id, subject_type, subject_id, content, model, duration_ms, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, NOW())
			ON CONFLICT (subject_type, subject_id)
			DO UPDATE SET content = EXCLUDED.content, model = EXCLUDED.model, duration_ms = EXCLUDED.duration_ms, created_at = NOW()
		`, orgID, subjectType, subjectID, content, model, durationMS)
		if err != nil {
			slog.Error("failed to persist explanation", "error", err)
		}
	}

	resp := ExplanationResponse{
		SubjectType: subjectType,
		SubjectID:   subjectID,
		Content:     content,
		Model:       model,
		DurationMS:  durationMS,
		Cached:      false,
		CreatedAt:   time.Now().UTC(),
	}

	if isSSE {
		metaBytes, _ := json.Marshal(resp)
		fmt.Fprintf(w, "event: metadata\ndata: %s\n\n", metaBytes)
		chunkBytes, _ := json.Marshal(resp.Content)
		fmt.Fprintf(w, "data: %s\n\n", chunkBytes)
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flushSSE(w)
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func explainLocale(r *http.Request) string {
	raw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("locale")))
	if raw == "" {
		raw = strings.ToLower(strings.TrimSpace(r.Header.Get("Accept-Language")))
	}
	if strings.HasPrefix(raw, "en") {
		return "en"
	}
	return "fr"
}

var (
	explanationMarker     = regexp.MustCompile(`(?s)<!--\s*EXPLANATION\s*-->(.+?)<!--\s*/EXPLANATION\s*-->`)
	explanationOpenMarker = regexp.MustCompile(`(?s)<!--\s*EXPLANATION\s*-->(.+)$`)
)

// extractExplanation pulls the markdown between the <!-- EXPLANATION --> tags.
// If only the opening marker is present (e.g. the model hit max_tokens just
// before closing), we still return what comes after it — the closing marker is
// nice-to-have, not mandatory.
func extractExplanation(out string) string {
	var body string
	switch {
	case explanationMarker.MatchString(out):
		m := explanationMarker.FindStringSubmatch(out)
		body = m[1]
	case explanationOpenMarker.MatchString(out):
		m := explanationOpenMarker.FindStringSubmatch(out)
		body = m[1]
	default:
		body = out
	}
	return normalizeMarkdown(strings.TrimSpace(body))
}

// normalizeMarkdown massages LLM output into something our minimal renderer
// displays cleanly:
//   - ensures a blank line before every bullet group (so the `- ` lines aren't
//     absorbed into the preceding paragraph);
//   - ensures a blank line after any standalone `**...**` heading-ish line.
//
// Cheap and safe: works on line-split tokens, only INSERTS blank lines, never
// rewrites semantics.
func normalizeMarkdown(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines)+4)
	isBullet := func(t string) bool {
		t = strings.TrimSpace(t)
		return strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ")
	}
	isBoldLine := func(t string) bool {
		t = strings.TrimSpace(t)
		return strings.HasPrefix(t, "**") && strings.HasSuffix(t, "**") && len(t) > 4
	}
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if i > 0 {
			prev := strings.TrimSpace(lines[i-1])
			if isBullet(trim) && prev != "" && !isBullet(prev) {
				out = append(out, "")
			}
			if trim != "" && !isBullet(trim) && isBoldLine(prev) {
				out = append(out, "")
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func escapeJSON(s string) string {
	b, _ := json.Marshal(s)
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return s
}

// isStaleRulesFormat flags cached reports produced by the old sectioned rules
// renderer (## Résumé / ## Cause la plus probable / ...). The rules engine now
// emits a minimal two-paragraph answer and regeneration is instant and free,
// so we purge and rebuild instead of serving the verbose legacy format. Only
// applies to rules mode: LLM answers may legitimately emit markdown headings.
func isStaleRulesFormat(mode, content string) bool {
	switch mode {
	case "rules", "local", "offline", "":
		return strings.Contains(content, "## ")
	default:
		return false
	}
}

func isMalformedExplanation(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true
	}
	if len([]rune(trimmed)) < 20 {
		return true
	}
	if strings.Count(trimmed, "**")%2 != 0 {
		return true
	}
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "règles :") ||
		strings.Contains(lower, "rules:") ||
		strings.Contains(lower, "sortie :") ||
		strings.Contains(lower, "output:")
}
