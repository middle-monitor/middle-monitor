package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/services"
)

// Metric series handlers. Custom metrics carry labels from the customer's own
// infrastructure, so every query is scoped to the caller's organization.

const (
	defaultSeriesWindow = time.Hour
	defaultTermsSize    = 100
	maxTermsSize        = 1000
)

// seriesTimeRange reads start/end, defaulting to the last hour.
func seriesTimeRange(r *http.Request) (time.Time, time.Time, error) {
	end := time.Now().UTC()
	start := end.Add(-defaultSeriesWindow)

	if raw := r.URL.Query().Get("end"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return start, end, err
		}
		end = parsed
	}
	if raw := r.URL.Query().Get("start"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return start, end, err
		}
		start = parsed
	}
	return start, end, nil
}

// seriesHostScope resolves the host_id / host_group_id parameters to the host
// names series carry. Resolved per request rather than stamped at ingestion, so
// moving a host between groups does not make the data already written wrong.
//
// Returns ok=false when a scope was asked for but matches no host: that must
// yield nothing rather than silently widening to the whole organization.
func seriesHostScope(db *sql.DB, r *http.Request, orgID int64) (names []string, scoped bool, ok bool) {
	hostID := r.URL.Query().Get("host_id")
	groupID := r.URL.Query().Get("host_group_id")
	if hostID == "" && groupID == "" {
		return nil, false, true
	}

	var rows *sql.Rows
	var err error
	if hostID != "" {
		rows, err = db.Query(`SELECT name FROM hosts WHERE id = $1 AND organization_id = $2`, hostID, orgID)
	} else {
		rows, err = db.Query(`SELECT name FROM hosts WHERE host_group_id = $1 AND organization_id = $2`, groupID, orgID)
	}
	if err != nil {
		return nil, true, false
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, true, false
		}
		names = append(names, name)
	}
	return names, true, len(names) > 0
}

func termsSize(r *http.Request) int {
	size := defaultTermsSize
	if parsed, err := strconv.Atoi(r.URL.Query().Get("size")); err == nil && parsed > 0 {
		size = parsed
	}
	if size > maxTermsSize {
		size = maxTermsSize
	}
	return size
}

func handleListMetricNames(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opensearch == nil {
			respondJSON(w, map[string]interface{}{"names": []string{}})
			return
		}

		start, end, err := seriesTimeRange(r)
		if err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}

		orgID := middleware.GetOrganizationID(r.Context())
		hostnames, scoped, ok := seriesHostScope(db, r, orgID)
		if scoped && !ok {
			respondJSON(w, map[string]interface{}{"names": []string{}})
			return
		}
		names, err := opensearch.ListMetricNames(r.Context(), orgID, start, end, r.URL.Query().Get("prefix"), termsSize(r), hostnames)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		respondJSON(w, map[string]interface{}{"names": names})
	}
}

func handleListMetricLabelKeys(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if opensearch == nil {
			respondJSON(w, map[string]interface{}{"keys": []string{}})
			return
		}

		start, end, err := seriesTimeRange(r)
		if err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}

		orgID := middleware.GetOrganizationID(r.Context())
		hostnames, scoped, ok := seriesHostScope(db, r, orgID)
		if scoped && !ok {
			respondJSON(w, map[string]interface{}{"keys": []string{}})
			return
		}
		keys, err := opensearch.ListLabelKeys(r.Context(), orgID, r.URL.Query().Get("metric"), start, end, termsSize(r), hostnames)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		respondJSON(w, map[string]interface{}{"keys": keys})
	}
}

func handleListMetricLabelValues(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		labelKey := r.URL.Query().Get("key")
		if labelKey == "" {
			respondError(w, http.StatusBadRequest, services.ErrSeriesLabelKeyRequired)
			return
		}
		if opensearch == nil {
			respondJSON(w, map[string]interface{}{"values": []string{}})
			return
		}

		start, end, err := seriesTimeRange(r)
		if err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}

		orgID := middleware.GetOrganizationID(r.Context())
		hostnames, scoped, ok := seriesHostScope(db, r, orgID)
		if scoped && !ok {
			respondJSON(w, map[string]interface{}{"values": []string{}})
			return
		}
		values, err := opensearch.ListLabelValues(
			r.Context(), orgID, r.URL.Query().Get("metric"), labelKey, r.URL.Query().Get("prefix"), start, end, termsSize(r), hostnames)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		respondJSON(w, map[string]interface{}{"values": values})
	}
}

func handleQueryMetricSeries(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start, end, err := seriesTimeRange(r)
		if err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}

		filters, err := services.ParseFilters(r.URL.Query()["filter"])
		if err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}

		aggregation := r.URL.Query().Get("aggregation")
		if aggregation == "" {
			aggregation = "avg"
		}

		var step time.Duration
		if raw := r.URL.Query().Get("step"); raw != "" {
			seconds, err := strconv.Atoi(raw)
			if err != nil || seconds <= 0 {
				respondError(w, http.StatusBadRequest, services.ErrSeriesStep)
				return
			}
			step = time.Duration(seconds) * time.Second
		}

		orgID := middleware.GetOrganizationID(r.Context())
		hostnames, scoped, ok := seriesHostScope(db, r, orgID)
		if scoped && !ok {
			respondJSON(w, map[string]interface{}{"series": []services.SeriesResult{}})
			return
		}

		query := services.SeriesQuery{
			OrganizationID: orgID,
			MetricName:     r.URL.Query().Get("metric"),
			Filters:        filters,
			GroupBy:        r.URL.Query().Get("group_by"),
			Aggregation:    aggregation,
			Start:          start,
			End:            end,
			Step:           step,
			Hostnames:      hostnames,
		}

		if opensearch == nil {
			respondJSON(w, map[string]interface{}{"series": []services.SeriesResult{}})
			return
		}

		results, err := opensearch.QuerySeries(r.Context(), query)
		if err != nil {
			// A bad metric name, aggregation or range is a client mistake.
			if isSeriesClientError(err) {
				respondError(w, http.StatusBadRequest, err)
				return
			}
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if results == nil {
			results = []services.SeriesResult{}
		}
		respondJSON(w, map[string]interface{}{"series": results})
	}
}

// Queries and expression go in the body; range and host scope stay query params, as on GET.
func handleQueryMetricExpression(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start, end, err := seriesTimeRange(r)
		if err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}

		var step time.Duration
		if raw := r.URL.Query().Get("step"); raw != "" {
			seconds, err := strconv.Atoi(raw)
			if err != nil || seconds <= 0 {
				respondError(w, http.StatusBadRequest, localizeSeriesError(r, services.ErrSeriesStep))
				return
			}
			step = time.Duration(seconds) * time.Second
		}

		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		var body struct {
			Queries    []services.NamedSeriesQuery `json:"queries"`
			Expression string                      `json:"expression"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		body.Expression = strings.TrimSpace(body.Expression)
		// Validated before the OpenSearch check, so bad input is a 400 on every deployment.
		if err := services.ValidateSeriesExpression(body.Queries, body.Expression); err != nil {
			respondError(w, http.StatusBadRequest, localizeSeriesError(r, err))
			return
		}

		orgID := middleware.GetOrganizationID(r.Context())
		hostnames, scoped, ok := seriesHostScope(db, r, orgID)
		if (scoped && !ok) || opensearch == nil {
			respondJSON(w, map[string]interface{}{"series": []services.SeriesResult{}})
			return
		}

		results, err := opensearch.QueryExpression(r.Context(), services.SeriesExpressionQuery{
			OrganizationID: orgID,
			Queries:        body.Queries,
			Expression:     body.Expression,
			Start:          start,
			End:            end,
			Step:           step,
			Hostnames:      hostnames,
		})
		if err != nil {
			if isSeriesClientError(err) {
				respondError(w, http.StatusBadRequest, localizeSeriesError(r, err))
				return
			}
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if results == nil {
			results = []services.SeriesResult{}
		}
		respondJSON(w, map[string]interface{}{"series": results})
	}
}

// isSeriesClientError tells a malformed request apart from a backend failure, so
// a bad aggregation name does not surface as a 500.
func isSeriesClientError(err error) bool {
	return errors.Is(err, services.ErrSeriesMetricRequired) ||
		errors.Is(err, services.ErrSeriesAggregation) ||
		errors.Is(err, services.ErrSeriesRange) ||
		errors.Is(err, services.ErrSeriesTooMany) ||
		errors.Is(err, services.ErrExpressionQueries) ||
		errors.Is(err, services.ErrExpressionRef) ||
		errors.Is(err, services.ErrExpressionTooLong) ||
		errors.Is(err, services.ErrExpressionSyntax) ||
		errors.Is(err, services.ErrExpressionUnknownRef) ||
		errors.Is(err, services.ErrExpressionFunction) ||
		errors.Is(err, services.ErrExpressionScalar) ||
		errors.Is(err, services.ErrExpressionNoMatch)
}
