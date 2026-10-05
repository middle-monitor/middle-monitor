package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/models"
	"middle-monitor/backend/services"

	"github.com/gorilla/mux"
)

// resolveIngestOrg resolves the organization for an ingestion token. The token
// may be a per-service token (created with an APM/error service), an org API key
// ("mm_...", created in the dashboard) or an install token: the agent holds only
// the latter, and its OTLP export carries it as the bearer credential. Returns
// the org id and whether it was resolved.
func resolveIngestOrg(db *sql.DB, token string) (int64, bool) {
	token = strings.TrimSpace(strings.TrimPrefix(token, "Bearer "))
	if token == "" {
		return 0, false
	}
	var orgID int64
	err := db.QueryRow(`SELECT organization_id FROM services WHERE token = $1 LIMIT 1`, token).Scan(&orgID)
	if err == nil && orgID != 0 {
		return orgID, true
	}
	if ident, err := services.NewAPIKeyService(db).ValidateKey(token); err == nil && ident.OrgID != 0 {
		return ident.OrgID, true
	}
	if installOrgID, err := services.NewInstallTokenService(db).ValidateToken(token); err == nil && installOrgID != 0 {
		return installOrgID, true
	}
	return 0, false
}

// Error handlers
func handleSubmitError(db *sql.DB, opensearch *services.OpenSearchService) http.HandlerFunc {
	errorService := services.NewErrorService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		// Resolve org from a service token or an org API key ("mm_..."). If the
		// token resolves to neither, we still accept and fall back to org 1.
		orgIDFromToken, _ := resolveIngestOrg(db, r.Header.Get("Authorization"))

		// Cap the request body: this endpoint is public (no auth) and a single
		// oversized payload shouldn't be able to exhaust memory. 1 MiB is far
		// above any legitimate error payload (SDKs cap http_body at ~2 KB).
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

		var appErr models.ApplicationError
		if err := json.NewDecoder(r.Body).Decode(&appErr); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		if appErr.Name == "" || appErr.Message == "" || appErr.File == "" || appErr.Service == "" {
			respondError(w, http.StatusBadRequest, ErrProfileFieldsMissing)
			return
		}

		if orgIDFromToken != 0 {
			appErr.OrganizationID = orgIDFromToken
		}

		// Log HTTP info for debugging
		if appErr.HTTPURL != nil {
			slog.Debug("received error with http context", "method", *appErr.HTTPMethod, "url", *appErr.HTTPURL)
		}

		if services.KafkaWriterSDKErrors != nil {
			if err := services.PublishPayload(services.KafkaWriterSDKErrors, appErr.Name, appErr); err != nil {
				respondPublishError(w, err)
				return
			}
			appErr.Timestamp = time.Now()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(appErr)
			return
		}

		result, err := errorService.CreateError(appErr)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		// Index to OpenSearch for correlation (fire-and-forget)
		if opensearch != nil {
			doc := map[string]interface{}{
				"@timestamp":      result.Timestamp.UTC().Format(time.RFC3339),
				"organization_id": result.OrganizationID,
				"name":            result.Name,
				"message":         result.Message,
				"file":            result.File,
				"line":            result.Line,
				"service":         result.Service,
			}
			if result.HTTPMethod != nil {
				doc["http_method"] = *result.HTTPMethod
			}
			if result.HTTPURL != nil {
				doc["http_url"] = *result.HTTPURL
			}
			if result.HTTPHeaders != nil {
				doc["http_headers"] = *result.HTTPHeaders
			}
			if result.HTTPBody != nil {
				doc["http_body"] = *result.HTTPBody
			}
			if result.TraceID != nil && *result.TraceID != "" {
				doc["trace_id"] = *result.TraceID
			}
			if result.Fingerprint != "" {
				doc["fingerprint"] = result.Fingerprint
			}
			if err := opensearch.IndexError(context.Background(), doc); err != nil {
				slog.Error("failed to index error to opensearch", "error", err)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func handleGetErrors(db *sql.DB) http.HandlerFunc {
	errorService := services.NewErrorService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		service := r.URL.Query().Get("service")
		limit := r.URL.Query().Get("limit")
		grouped := r.URL.Query().Get("grouped") == "1"
		window := r.URL.Query().Get("window")
		if window == "" {
			window = "24h"
		}
		startDate := r.URL.Query().Get("start")
		endDate := r.URL.Query().Get("end")
		// since/until is the spelling every list endpoint documents; start/end
		// is what this one shipped with and what the dashboard still sends.
		if startDate == "" {
			startDate = r.URL.Query().Get("since")
		}
		if endDate == "" {
			endDate = r.URL.Query().Get("until")
		}

		orgID := middleware.GetOrganizationID(r.Context())

		if grouped {
			// The raw-row fetch cap stays internal (the service default of 2000);
			// limit/offset here paginate the grouped result. Pagination is only
			// applied when the caller asks for it, so the errors overview (which
			// aggregates per-service facets over every group) still gets them all.
			groups, err := errorService.GetErrorsGroupedForOrg(orgID, service, "", window, startDate, endDate)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			total := len(groups)
			q := r.URL.Query()
			if _, hasLimit := q["limit"]; hasLimit || q.Get("offset") != "" {
				pLimit, pOffset := parseLimitOffset(r, 50, 500)
				start := pOffset
				if start > total {
					start = total
				}
				end := start + pLimit
				if end > total {
					end = total
				}
				groups = groups[start:end]
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Total-Count", strconv.Itoa(total))
			json.NewEncoder(w).Encode(groups)
			return
		}

		errors, err := errorService.GetErrorsForOrg(orgID, service, limit)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(errors)
	}
}

// handleGetError returns one error with its HTTP request headers and body,
// which the grouped list no longer carries.
func handleGetError(db *sql.DB) http.HandlerFunc {
	errorService := services.NewErrorService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		errorID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrErrorIDInvalid)
			return
		}

		appErr, err := errorService.GetErrorByID(errorID, orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if appErr == nil {
			respondError(w, http.StatusNotFound, ErrErrorNotFound)
			return
		}

		respondJSON(w, appErr)
	}
}

func handleGetErrorServices(db *sql.DB) http.HandlerFunc {
	serviceService := services.NewServiceService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())

		errorServices, err := serviceService.GetErrorServicesForOrg(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(errorServices)
	}
}

// Application links (for alert correlation: link app service+env to hosts/services)
func handleCreateApplicationLink(db *sql.DB) http.HandlerFunc {
	linkService := services.NewLinkService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		var link models.ApplicationLink
		if err := json.NewDecoder(r.Body).Decode(&link); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if link.AppServiceName == "" || link.TargetType == "" || link.TargetID == 0 {
			respondError(w, http.StatusBadRequest, ErrLinkFieldsMissing)
			return
		}
		created, err := linkService.Create(orgID, link)
		if err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	}
}

func handleGetApplicationLinks(db *sql.DB) http.HandlerFunc {
	linkService := services.NewLinkService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		links, err := linkService.ListForOrg(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if links == nil {
			links = []models.ApplicationLink{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(links)
	}
}

func handleDeleteApplicationLink(db *sql.DB) http.HandlerFunc {
	linkService := services.NewLinkService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		vars := mux.Vars(r)
		id, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrLinkIDInvalid)
			return
		}
		if err := linkService.Delete(id, orgID); err != nil {
			respondError(w, http.StatusNotFound, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Host groups (scope alert correlation to a host + the hosts sharing its role)
func handleGetHostGroups(db *sql.DB) http.HandlerFunc {
	svc := services.NewHostGroupService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		groups, err := svc.ListForOrg(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(groups)
	}
}

func handleCreateHostGroup(db *sql.DB) http.HandlerFunc {
	svc := services.NewHostGroupService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		var body struct {
			Name        string  `json:"name"`
			DisplayName *string `json:"display_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		g, err := svc.Create(orgID, body.Name, body.DisplayName)
		if err != nil {
			// (organization_id, name) is unique: report which group already
			// holds the name rather than a bare failure.
			var existing int64
			if db.QueryRow(`SELECT id FROM host_groups WHERE organization_id=$1 AND name=$2`,
				orgID, body.Name).Scan(&existing); existing != 0 {
				respondConflict(w, err, existing)
				return
			}
			respondError(w, http.StatusBadRequest, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(g)
	}
}

func handleUpdateHostGroup(db *sql.DB) http.HandlerFunc {
	svc := services.NewHostGroupService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrHostGroupIDInvalid)
			return
		}
		var body struct {
			Name        string  `json:"name"`
			DisplayName *string `json:"display_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if err := svc.Update(orgID, id, body.Name, body.DisplayName); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleDeleteHostGroup(db *sql.DB) http.HandlerFunc {
	svc := services.NewHostGroupService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrHostGroupIDInvalid)
			return
		}
		if err := svc.Delete(orgID, id); err != nil {
			if errors.Is(err, services.ErrGroupHasHosts) {
				respondError(w, http.StatusConflict, err)
				return
			}
			respondError(w, http.StatusBadRequest, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAssignHostGroup(db *sql.DB) http.HandlerFunc {
	svc := services.NewHostGroupService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		hostID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrHostIDInvalid)
			return
		}
		var body struct {
			HostGroupID int64 `json:"host_group_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if err := svc.AssignHost(orgID, hostID, body.HostGroupID); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleGetLinkSuggestions returns suggested application links derived from trace data (peer.service, server.address, db.name, etc.)
func handleGetLinkSuggestions(db *sql.DB, opensearch *services.OpenSearchService) http.HandlerFunc {
	suggestionService := services.NewSuggestionService(db, opensearch)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		service := r.URL.Query().Get("service")
		if service == "" {
			respondError(w, http.StatusBadRequest, ErrServiceParamMissing)
			return
		}
		suggestions, emptyReason, err := suggestionService.GetSuggestions(orgID, service)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if suggestions == nil {
			suggestions = []models.LinkSuggestion{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(models.LinkSuggestionsResponse{Suggestions: suggestions, EmptyReason: emptyReason})
	}
}

// handleGetErrorCorrelation returns worker results, infra metrics, and semantic matches correlated to an error
func handleGetErrorCorrelation(db *sql.DB, opensearch *services.OpenSearchService) http.HandlerFunc {
	correlationService := services.NewCorrelationService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		vars := mux.Vars(r)
		errorID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrErrorIDInvalid)
			return
		}

		result, err := correlationService.GetCorrelationForError(orgID, errorID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

// handleGetServiceResultCorrelation returns root cause analysis for a failing service check
func handleGetServiceResultCorrelation(db *sql.DB) http.HandlerFunc {
	correlationService := services.NewCorrelationService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		vars := mux.Vars(r)
		resultID, err := strconv.ParseInt(vars["resultId"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrResultIDInvalid)
			return
		}

		result, err := correlationService.GetCorrelationForServiceResult(orgID, resultID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

// Metrics handlers
func handleSubmitMetrics(db *sql.DB) http.HandlerFunc {
	metricService := services.NewMetricService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		var metric models.SystemMetric
		if err := json.NewDecoder(r.Body).Decode(&metric); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		if metric.Service == "" {
			respondError(w, http.StatusBadRequest, ErrServiceFieldMissing)
			return
		}

		result, err := metricService.CreateMetric(metric)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func handleGetMetrics(db *sql.DB) http.HandlerFunc {
	metricService := services.NewMetricService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		service := r.URL.Query().Get("service")
		orgID := middleware.GetOrganizationID(r.Context())

		metrics, err := metricService.GetMetricsForOrg(orgID, service)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metrics)
	}
}

// Service handlers
func handleSubmitService(db *sql.DB) http.HandlerFunc {
	serviceService := services.NewServiceService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		var service models.Service
		if err := json.NewDecoder(r.Body).Decode(&service); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		if err := validateName("name", service.Name, maxNameLen); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		if service.Type == "" || service.Host == "" || service.Service == "" {
			respondError(w, http.StatusBadRequest, ErrServiceFieldsMissing)
			return
		}
		if err := validateServiceType(service.Type); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		// Set organization ID from context
		service.OrganizationID = middleware.GetOrganizationID(r.Context())

		// Set defaults then validate bounds.
		if service.ServiceInterval <= 0 {
			service.ServiceInterval = 60
		}
		if service.MaxAttempts <= 0 {
			service.MaxAttempts = 3
		}
		if err := validateServiceInterval(service.ServiceInterval); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		if err := validateMaxAttempts(service.MaxAttempts); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		if service.ExpectedStatusCode != nil {
			if err := validateExpectedStatusCode(*service.ExpectedStatusCode); err != nil {
				respondError(w, http.StatusBadRequest, err)
				return
			}
		}

		// Encrypt sensitive credentials before persisting.
		if service.Type == "sql" && service.Credentials != nil {
			encrypted, err := services.EncryptSQLCredentials(*service.Credentials)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			service.Credentials = &encrypted
		}

		result, err := serviceService.CreateService(service)
		if err != nil {
			var planErr *services.PlanLimitError
			if errors.As(err, &planErr) {
				respondPlanLimit(w, err)
				return
			}
			if errors.Is(err, services.ErrHostNotFound) {
				respondError(w, http.StatusBadRequest, err)
				return
			}
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func handleGetServices(db *sql.DB) http.HandlerFunc {
	serviceService := services.NewServiceService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		service := q.Get("service")
		startDate := q.Get("start_date")
		endDate := q.Get("end_date")
		orgID := middleware.GetOrganizationID(r.Context())

		// Pagination is opt-in (only the services page passes limit/offset); other
		// callers (service pickers, dashboards...) keep receiving the full list.
		if _, hasLimit := q["limit"]; hasLimit || q.Get("offset") != "" {
			limit, offset := parseLimitOffset(r, 50, 200)
			page, total, err := serviceService.GetServicesPageForOrg(orgID, q.Get("search"), q.Get("status"), q.Get("type"), startDate, endDate, limit, offset)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			if page == nil {
				page = []models.ServiceWithResults{}
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Total-Count", strconv.Itoa(total))
			json.NewEncoder(w).Encode(page)
			return
		}

		servicesWithResults, err := serviceService.GetAllServicesForOrg(orgID, service, startDate, endDate)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(servicesWithResults)
	}
}

func handleGetServiceStats(db *sql.DB) http.HandlerFunc {
	serviceService := services.NewServiceService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		stats, err := serviceService.ServiceStats(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}
}

func handleUpdateService(db *sql.DB) http.HandlerFunc {
	serviceService := services.NewServiceService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		serviceID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrServiceIDInvalid)
			return
		}

		var service models.Service
		if err := json.NewDecoder(r.Body).Decode(&service); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		if service.Type != "" {
			if err := validateServiceType(service.Type); err != nil {
				respondError(w, http.StatusBadRequest, err)
				return
			}
		}
		// Set defaults then validate bounds.
		if service.ServiceInterval <= 0 {
			service.ServiceInterval = 60
		}
		if service.MaxAttempts <= 0 {
			service.MaxAttempts = 3
		}
		if err := validateServiceInterval(service.ServiceInterval); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		if err := validateMaxAttempts(service.MaxAttempts); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		if service.ExpectedStatusCode != nil {
			if err := validateExpectedStatusCode(*service.ExpectedStatusCode); err != nil {
				respondError(w, http.StatusBadRequest, err)
				return
			}
		}

		// Encrypt sensitive credentials before persisting.
		if service.Type == "sql" && service.Credentials != nil {
			encrypted, err := services.EncryptSQLCredentials(*service.Credentials)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			service.Credentials = &encrypted
		}

		if !requireRowInOrg(w, r, db, servicesTable, serviceID, ErrServiceNotFound) {
			return
		}
		result, err := serviceService.UpdateService(serviceID, service)
		if err != nil {
			if errors.Is(err, services.ErrServiceNotFound) {
				respondError(w, http.StatusNotFound, err)
			} else {
				respondError(w, http.StatusInternalServerError, err)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func handleGetServiceDetail(db *sql.DB) http.HandlerFunc {
	serviceService := services.NewServiceService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		serviceID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrServiceIDInvalid)
			return
		}

		orgID := middleware.GetOrganizationID(r.Context())
		svc, err := serviceService.GetServiceDetail(serviceID, orgID)
		if err != nil {
			respondError(w, http.StatusNotFound, ErrServiceNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(svc)
	}
}

func handleDeleteService(db *sql.DB) http.HandlerFunc {
	serviceService := services.NewServiceService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		serviceID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrServiceIDInvalid)
			return
		}
		if !requireRowInOrg(w, r, db, servicesTable, serviceID, ErrServiceNotFound) {
			return
		}

		err = serviceService.DeleteService(serviceID)
		if err != nil {
			if errors.Is(err, services.ErrServiceNotFound) {
				respondError(w, http.StatusNotFound, err)
			} else {
				respondError(w, http.StatusInternalServerError, err)
			}
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func handleGetServiceResults(db *sql.DB) http.HandlerFunc {
	serviceService := services.NewServiceService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		serviceID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrServiceIDInvalid)
			return
		}
		if !requireRowInOrg(w, r, db, servicesTable, serviceID, ErrServiceNotFound) {
			return
		}

		startDate := r.URL.Query().Get("start_date")
		endDate := r.URL.Query().Get("end_date")
		if startDate == "" {
			startDate = r.URL.Query().Get("since")
		}
		if endDate == "" {
			endDate = r.URL.Query().Get("until")
		}

		results, err := serviceService.GetServiceResults(serviceID, startDate, endDate)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	}
}

// Event handlers
func handleSubmitEvent(db *sql.DB) http.HandlerFunc {
	eventService := services.NewEventService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		var event models.Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		if event.Type == "" || event.Service == "" || event.Message == "" {
			respondError(w, http.StatusBadRequest, ErrEventFieldsMissing)
			return
		}

		// Set organization ID from context
		event.OrganizationID = middleware.GetOrganizationID(r.Context())

		result, err := eventService.CreateEvent(event)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func handleGetEvents(db *sql.DB) http.HandlerFunc {
	eventService := services.NewEventService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		service := r.URL.Query().Get("service")
		limitStr := r.URL.Query().Get("limit")
		limit := 100
		if limitStr != "" {
			if parsed, err := strconv.Atoi(limitStr); err == nil {
				limit = parsed
			}
		}
		orgID := middleware.GetOrganizationID(r.Context())

		events, err := eventService.GetEventsForOrg(orgID, service, limit)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(events)
	}
}

// Dashboard handlers
func handleGetHealth(db *sql.DB) http.HandlerFunc {
	dashboardService := services.NewDashboardService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())

		health, err := dashboardService.GetHealthForOrg(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(health)
	}
}

func handleGetErrorStats(db *sql.DB) http.HandlerFunc {
	dashboardService := services.NewDashboardService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		service := r.URL.Query().Get("service")
		orgID := middleware.GetOrganizationID(r.Context())

		stats, err := dashboardService.GetErrorStatsForOrg(orgID, service)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}
}

func handleGetMetricStats(db *sql.DB) http.HandlerFunc {
	dashboardService := services.NewDashboardService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		service := r.URL.Query().Get("service")
		orgID := middleware.GetOrganizationID(r.Context())

		stats, err := dashboardService.GetMetricStatsForOrg(orgID, service)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}
}

// Host handlers
func handleSubmitHost(db *sql.DB) http.HandlerFunc {
	hostService := services.NewHostService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		var host models.Host
		if err := json.NewDecoder(r.Body).Decode(&host); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		if err := validateName("name", host.Name, maxNameLen); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		if err := validateName("host", host.Host, maxHostLen); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		if host.Service == "" {
			host.Service = host.Name
		}

		// Set organization ID from context
		host.OrganizationID = middleware.GetOrganizationID(r.Context())

		result, err := hostService.CreateHost(host)
		if err != nil {
			var planErr *services.PlanLimitError
			if errors.As(err, &planErr) {
				respondPlanLimit(w, err)
				return
			}
			var nameTaken *services.NameTakenError
			if errors.As(err, &nameTaken) {
				// The name is unique per organization, so the caller can turn
				// this into a get and converge.
				var existing int64
				db.QueryRow(`SELECT id FROM hosts WHERE organization_id=$1 AND name=$2`,
					host.OrganizationID, host.Name).Scan(&existing)
				respondConflict(w, err, existing)
				return
			}
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func handleGetHosts(db *sql.DB) http.HandlerFunc {
	hostService := services.NewHostService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())

		// Pagination is opt-in: only the hosts page passes limit/offset. Every
		// other caller (host pickers, dashboards...) expects the full list, so
		// without those params we keep the original unpaginated behaviour.
		q := r.URL.Query()
		if _, hasLimit := q["limit"]; hasLimit || q.Get("offset") != "" {
			limit, offset := parseLimitOffset(r, 50, 200)
			search := q.Get("search")
			status := q.Get("status")
			total, err := hostService.CountHostsFiltered(orgID, search, status)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			hosts, err := hostService.GetHostsWithStatsPaged(orgID, search, status, limit, offset)
			if err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			if hosts == nil {
				hosts = []models.HostWithStats{}
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Total-Count", strconv.Itoa(total))
			json.NewEncoder(w).Encode(hosts)
			return
		}

		hosts, err := hostService.GetHostsWithStats(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(hosts)
	}
}

func handleGetHostStats(db *sql.DB) http.HandlerFunc {
	hostService := services.NewHostService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		stats, err := hostService.HostStats(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}
}

func handleGetHostDetail(db *sql.DB) http.HandlerFunc {
	hostService := services.NewHostService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		hostID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrHostIDInvalid)
			return
		}

		orgID := middleware.GetOrganizationID(r.Context())
		host, err := hostService.GetHostDetail(hostID, orgID)
		if err != nil {
			respondError(w, http.StatusNotFound, ErrHostNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(host)
	}
}

func handleUpdateHost(db *sql.DB) http.HandlerFunc {
	hostService := services.NewHostService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		hostID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrHostIDInvalid)
			return
		}

		var host models.Host
		if err := json.NewDecoder(r.Body).Decode(&host); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		if !requireRowInOrg(w, r, db, hostsTable, hostID, ErrHostNotFound) {
			return
		}
		result, err := hostService.UpdateHost(hostID, host)
		if err != nil {
			if errors.Is(err, services.ErrHostNotFound) {
				respondError(w, http.StatusNotFound, err)
				return
			}
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func handleDeleteHost(db *sql.DB) http.HandlerFunc {
	hostService := services.NewHostService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		hostID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrHostIDInvalid)
			return
		}
		if !requireRowInOrg(w, r, db, hostsTable, hostID, ErrHostNotFound) {
			return
		}

		err = hostService.DeleteHost(hostID)
		if err != nil {
			if errors.Is(err, services.ErrHostHasServices) {
				respondError(w, http.StatusConflict, err)
				return
			}
			if errors.Is(err, services.ErrHostNotFound) {
				respondError(w, http.StatusNotFound, err)
				return
			}
			respondError(w, http.StatusInternalServerError, err)
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func handleGetHostServices(db *sql.DB) http.HandlerFunc {
	serviceService := services.NewServiceService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		hostID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrHostIDInvalid)
			return
		}

		orgID := middleware.GetOrganizationID(r.Context())
		startDate := r.URL.Query().Get("start_date")
		endDate := r.URL.Query().Get("end_date")
		servicesWithResults, err := serviceService.GetServicesForHost(hostID, orgID, startDate, endDate)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(servicesWithResults)
	}
}

func handleGetHostServicesByName(db *sql.DB) http.HandlerFunc {
	hostService := services.NewHostService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		vars := mux.Vars(r)
		hostName := vars["name"]

		if hostName == "" {
			respondError(w, http.StatusBadRequest, ErrHostNameRequired)
			return
		}

		host, err := hostService.GetHostByName(hostName, orgID)
		if err != nil {
			respondError(w, http.StatusNotFound, ErrHostNotFound)
			return
		}

		servicesWithResults, err := hostService.GetHostServices(host.ID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		// Sort services by latest result timestamp or created_at
		sort.Slice(servicesWithResults, func(i, j int) bool {
			iTime := servicesWithResults[i].CreatedAt
			jTime := servicesWithResults[j].CreatedAt
			if len(servicesWithResults[i].Results) > 0 {
				iTime = servicesWithResults[i].Results[0].Timestamp
			}
			if len(servicesWithResults[j].Results) > 0 {
				jTime = servicesWithResults[j].Results[0].Timestamp
			}
			return iTime.After(jTime)
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(servicesWithResults)
	}
}

func handleGetTimeline(db *sql.DB) http.HandlerFunc {
	dashboardService := services.NewDashboardService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		service := r.URL.Query().Get("service")
		limitStr := r.URL.Query().Get("limit")
		limit := 200
		if limitStr != "" {
			if parsed, err := strconv.Atoi(limitStr); err == nil {
				limit = parsed
			}
		}
		orgID := middleware.GetOrganizationID(r.Context())

		var from, to time.Time
		if s := r.URL.Query().Get("start_date"); s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				from = t
			}
		}
		if s := r.URL.Query().Get("end_date"); s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				to = t
			}
		}

		events, err := dashboardService.GetTimelineForOrgFiltered(orgID, service, limit, from, to)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(events)
	}
}

// Agent handlers
func handleAgentRegister(db *sql.DB) http.HandlerFunc {
	agentService := services.NewAgentService(db)
	installTokenService := services.NewInstallTokenService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("agent register request received", "remote_addr", r.RemoteAddr)

		var registration models.AgentRegistration
		if err := json.NewDecoder(r.Body).Decode(&registration); err != nil {
			slog.Warn("agent register rejected, invalid body", "error", err)
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		slog.Debug("agent register", "hostname", registration.Hostname, "service", registration.Service)

		if registration.Hostname == "" || registration.Service == "" {
			respondError(w, http.StatusBadRequest, ErrAgentFieldsMissing)
			return
		}

		// Token obligatoire (comme Datadog/Terraform Cloud)
		token := r.Header.Get("X-Install-Token")
		if token == "" {
			if auth := r.Header.Get("Authorization"); len(auth) > 7 && auth[:7] == "Bearer " {
				token = auth[7:]
			}
		}
		if token == "" {
			slog.Warn("agent register rejected, no install token", "hostname", registration.Hostname)
			respondError(w, http.StatusUnauthorized, ErrInstallTokenRequired)
			return
		}
		orgID, err := installTokenService.ValidateToken(token)
		if err != nil {
			slog.Warn("agent register rejected, invalid token", "hostname", registration.Hostname, "error", err)
			respondError(w, http.StatusUnauthorized, ErrInstallTokenInvalid)
			return
		}

		slog.Debug("agent register, creating host and services", "org_id", orgID)
		result, err := agentService.RegisterAgent(registration, orgID)
		if err != nil {
			slog.Error("agent register failed", "error", err)
			var planErr *services.PlanLimitError
			if errors.As(err, &planErr) {
				respondError(w, http.StatusBadRequest, err)
				return
			}
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		slog.Info("agent registered", "hostname", registration.Hostname, "host_id", result.HostID, "service_ids", result.ServiceIDs)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(result)
	}
}

func handleAgentMetrics(db *sql.DB) http.HandlerFunc {
	agentService := services.NewAgentService(db)
	installTokenService := services.NewInstallTokenService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		var metrics models.AgentMetrics
		if err := json.NewDecoder(r.Body).Decode(&metrics); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		if metrics.Hostname == "" || metrics.Service == "" {
			respondError(w, http.StatusBadRequest, ErrAgentFieldsMissing)
			return
		}

		// Require install token so we know which org the metrics belong to
		token := r.Header.Get("X-Install-Token")
		if token == "" {
			if auth := r.Header.Get("Authorization"); len(auth) > 7 && auth[:7] == "Bearer " {
				token = auth[7:]
			}
		}
		if token == "" {
			slog.Warn("agent metrics rejected, no install token", "hostname", metrics.Hostname)
			respondError(w, http.StatusUnauthorized, ErrInstallTokenRequired)
			return
		}
		orgID, err := installTokenService.ValidateToken(token)
		if err != nil {
			slog.Warn("agent metrics rejected, invalid token", "hostname", metrics.Hostname, "error", err)
			respondError(w, http.StatusUnauthorized, ErrInstallTokenInvalid)
			return
		}

		if services.KafkaWriterAgentMetrics != nil {
			payload := struct {
				Metrics models.AgentMetrics `json:"metrics"`
				OrgID   int64               `json:"org_id"`
			}{Metrics: metrics, OrgID: orgID}

			if err := services.PublishPayload(services.KafkaWriterAgentMetrics, metrics.Hostname, payload); err != nil {
				respondPublishError(w, err)
				return
			}
			respondAgentAccepted(w, http.StatusAccepted, orgID)
			return
		}

		err = agentService.StoreMetrics(metrics, orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		respondAgentAccepted(w, http.StatusOK, orgID)
	}
}

// Agent download handler - serves agent binaries. The version segment is
// optional: pinning a version is the default practice in production, and the
// unversioned URL stays the one the install script uses.
func handleAgentDownload(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		osName := vars["os"]
		arch := vars["arch"]

		if !validAgentPlatform(osName, arch) {
			respondError(w, http.StatusBadRequest, ErrAgentPlatformInvalid)
			return
		}

		// A pinned version that no longer matches what is on offer has to fail
		// loudly: silently serving another build is how a fleet drifts.
		if version := vars["version"]; version != "" && version != "latest" && version != agentVersion() {
			respondError(w, http.StatusNotFound, ErrAgentVersionUnknown)
			return
		}

		agentPath, found := agentBinaryPath(osName, arch)
		if !found {
			respondError(w, http.StatusNotFound, ErrAgentBinaryMissing)
			return
		}

		info, err := os.Stat(agentPath)
		if err != nil {
			respondError(w, http.StatusInternalServerError, ErrAgentBinaryRead)
			return
		}

		file, err := os.Open(agentPath)
		if err != nil {
			respondError(w, http.StatusInternalServerError, ErrAgentBinaryRead)
			return
		}
		defer file.Close()

		// Verify it's actually a binary file (check first few bytes)
		buf := make([]byte, 4)
		if _, err := file.Read(buf); err != nil {
			respondError(w, http.StatusInternalServerError, ErrAgentBinaryRead)
			return
		}
		file.Seek(0, 0) // Reset to beginning

		// Check for ELF (Linux) or Mach-O (macOS) magic numbers
		isBinary := (buf[0] == 0x7f && buf[1] == 0x45 && buf[2] == 0x4c && buf[3] == 0x46) || // ELF
			(buf[0] == 0xcf && buf[1] == 0xfa) || // Mach-O (32-bit)
			(buf[0] == 0xce && buf[1] == 0xfa) || // Mach-O (32-bit, swapped)
			(buf[0] == 0xfe && buf[1] == 0xed && buf[2] == 0xfa && buf[3] == 0xce) || // Mach-O (64-bit)
			(buf[0] == 0xfe && buf[1] == 0xed && buf[2] == 0xfa && buf[3] == 0xcf) // Mach-O (64-bit, swapped)

		if !isBinary {
			respondError(w, http.StatusInternalServerError, ErrAgentBinaryRead)
			return
		}

		// The digest doubles as the ETag, so a fleet re-running its deployment
		// gets a 304 instead of 14 MB per host.
		if sum, err := agentFileChecksum(agentPath, info); err == nil {
			w.Header().Set("ETag", `"`+sum+`"`)
			w.Header().Set("X-Agent-SHA256", sum)
		}
		w.Header().Set("X-Agent-Version", agentVersion())
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=middle-monitor-agent-%s-%s", osName, arch))

		// Passing the real modification time is what makes ServeContent emit
		// Last-Modified and honour If-Modified-Since and If-None-Match.
		http.ServeContent(w, r, fmt.Sprintf("middle-monitor-agent-%s-%s", osName, arch), info.ModTime(), file)
	}
}

// Agent install script handler - serves the install script (embedded in binary)
func handleAgentInstallScript(db *sql.DB) http.HandlerFunc {
	installTokenService := services.NewInstallTokenService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		script := installScript

		// The URL every agent installed by this script will talk to, so it
		// has to be the public one rather than what the last proxy hop saw.
		apiURL := requestBaseURL(r)

		// Inject API URL into the script by replacing the default
		scriptStr := string(script)
		scriptStr = strings.Replace(scriptStr,
			`API_URL="${MIDDLE_MONITOR_API_URL:-http://localhost:8080}"`,
			fmt.Sprintf(`API_URL="${MIDDLE_MONITOR_API_URL:-%s}"`, apiURL),
			1)

		// Inject install token if present and valid (?token= in URL)
		installToken := r.URL.Query().Get("token")
		if installToken != "" {
			if _, err := installTokenService.ValidateToken(installToken); err == nil {
				scriptStr = strings.Replace(scriptStr,
					`INSTALL_TOKEN_INJECTED=""`,
					fmt.Sprintf(`INSTALL_TOKEN_INJECTED="%s"`, installToken),
					1)
			}
		}

		w.Header().Set("Content-Type", "text/x-shellscript")
		w.Header().Set("Content-Disposition", "inline; filename=install.sh")
		w.Write([]byte(scriptStr))
	}
}

// Agent update script handler - serves the update script
func handleAgentUpdateScript() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		script := updateScript

		// The URL every agent installed by this script will talk to, so it
		// has to be the public one rather than what the last proxy hop saw.
		apiURL := requestBaseURL(r)

		// Inject API URL into the script by replacing the default
		scriptStr := string(script)
		scriptStr = strings.Replace(scriptStr,
			`API_URL="${MIDDLE_MONITOR_API_URL:-http://localhost:8080}"`,
			fmt.Sprintf(`API_URL="${MIDDLE_MONITOR_API_URL:-%s}"`, apiURL),
			1)

		w.Header().Set("Content-Type", "text/x-shellscript")
		w.Header().Set("Content-Disposition", "inline; filename=update.sh")
		w.Write([]byte(scriptStr))
	}
}

// Get agent metrics for a specific service
func handleGetAgentMetrics(db *sql.DB) http.HandlerFunc {
	agentService := services.NewAgentService(db)
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		serviceID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrServiceIDInvalid)
			return
		}
		if !requireRowInOrg(w, r, db, servicesTable, serviceID, ErrServiceNotFound) {
			return
		}

		limitStr := r.URL.Query().Get("limit")
		limit := 200
		if limitStr != "" {
			if parsed, err := strconv.Atoi(limitStr); err == nil {
				limit = parsed
			}
		}

		metrics, err := agentService.GetAgentMetrics(serviceID, limit)
		if err != nil {
			if errors.Is(err, services.ErrNotAgentService) {
				respondError(w, http.StatusBadRequest, err)
			} else if errors.Is(err, services.ErrServiceNotFound) {
				respondError(w, http.StatusNotFound, ErrServiceNotFound)
			} else {
				respondError(w, http.StatusInternalServerError, err)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(metrics)
	}
}

// SQL health check handler
func handleSQLHealthCheck(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Simple ping to check database connection
		err := db.Ping()
		latency := time.Since(start)

		status := "ok"
		statusCode := http.StatusOK
		message := "Database connection successful"

		if err != nil {
			status = "error"
			statusCode = http.StatusServiceUnavailable
			message = fmt.Sprintf("Database connection failed: %v", err)
		}

		response := map[string]interface{}{
			"status":     status,
			"message":    message,
			"latency_ms": latency.Milliseconds(),
			"timestamp":  time.Now().UTC(),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(response)
	}
}

// Tables requireRowInOrg checks; a fixed set because the name is spliced into SQL.
const (
	hostsTable    = "hosts"
	servicesTable = "services"
)

// requireRowInOrg answers 404 unless the row belongs to the caller's
// organization. Ids are sequential, so without it any user reaches any tenant.
func requireRowInOrg(w http.ResponseWriter, r *http.Request, db *sql.DB, table string, id int64, notFound error) bool {
	var owned bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+table+` WHERE id = $1 AND organization_id = $2)`,
		id, middleware.GetOrganizationID(r.Context())).Scan(&owned)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return false
	}
	if !owned {
		respondError(w, http.StatusNotFound, notFound)
		return false
	}
	return true
}
