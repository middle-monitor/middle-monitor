package api

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"middle-monitor/backend/services"
)

var (
	otlpReceiver *services.OTLPReceiverService
	opensearch   *services.OpenSearchService
	otlpDB       *sql.DB
)

// Initialize OTLP services (called from main.go)
func InitializeOTLPServices(db *sql.DB) *services.OpenSearchService {
	otlpDB = db
	opensearch = services.NewOpenSearchService()
	if err := opensearch.Initialize(); err != nil {
		slog.Warn("failed to initialize opensearch", "error", err)
		slog.Warn("opensearch integration disabled, set OPENSEARCH_URL to enable")
		opensearch = nil
	}

	if opensearch != nil {
		otlpReceiver = services.NewOTLPReceiverService(opensearch, db)
		slog.Info("otlp receiver initialized")
	}
	if db != nil {
		ingestBudget = services.NewIngestBudget(db)
		ingestBudget.StartFlusher(make(chan struct{}))
	}
	return opensearch
}

// OTLP HTTP Handlers (OTLP uses HTTP/gRPC, we'll implement HTTP for simplicity)

// handleOTLPTraces handles OTLP trace export via HTTP
// Endpoint: POST /v1/traces
func handleOTLPTraces(w http.ResponseWriter, r *http.Request) {
	slog.Debug("received trace request", "content_length", r.ContentLength)
	if otlpReceiver == nil {
		respondError(w, http.StatusServiceUnavailable, ErrOTLPReceiverUnavailable)
		return
	}

	// Authenticate before reading: an anonymous caller must not get a body decoded.
	orgID, ok := resolveIngestOrg(otlpDB, r.Header.Get("Authorization"))
	if !ok {
		respondError(w, http.StatusUnauthorized, ErrIngestTokenRequired)
		return
	}

	body, err := readOTLPBody(w, r)
	if err != nil {
		respondOTLPBodyError(w, err)
		return
	}
	defer r.Body.Close()

	contentType := r.Header.Get("Content-Type")
	if contentType != "application/x-protobuf" && contentType != "application/protobuf" {
		slog.Warn("unsupported content-type for traces", "content_type", contentType)
		respondError(w, http.StatusUnsupportedMediaType, ErrContentTypeNotProtobuf)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if services.KafkaWriterTraces != nil {
		if err := services.PublishOTLP(services.KafkaWriterTraces, "traces", body, orgID); err != nil {
			respondPublishError(w, err)
			return
		}
	} else if err := otlpReceiver.ReceiveTraces(ctx, body, orgID); err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Errorf("%w: %w", ErrTracesProcess, err))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte{})
}

// handleOTLPLogs handles OTLP log export via HTTP
// Endpoint: POST /v1/logs
func handleOTLPLogs(w http.ResponseWriter, r *http.Request) {
	if otlpReceiver == nil {
		respondError(w, http.StatusServiceUnavailable, ErrOTLPReceiverUnavailable)
		return
	}

	// Authenticate before reading: an anonymous caller must not get a body decoded.
	orgID, ok := resolveIngestOrg(otlpDB, r.Header.Get("Authorization"))
	if !ok {
		respondError(w, http.StatusUnauthorized, ErrIngestTokenRequired)
		return
	}

	body, err := readOTLPBody(w, r)
	if err != nil {
		respondOTLPBodyError(w, err)
		return
	}
	defer r.Body.Close()

	contentType := r.Header.Get("Content-Type")
	if contentType != "application/x-protobuf" && contentType != "application/protobuf" {
		slog.Warn("unsupported content-type for logs", "content_type", contentType)
		respondError(w, http.StatusUnsupportedMediaType, ErrContentTypeNotProtobuf)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if services.KafkaWriterLogs != nil {
		if err := services.PublishOTLP(services.KafkaWriterLogs, "logs", body, orgID); err != nil {
			respondPublishError(w, err)
			return
		}
	} else if err := otlpReceiver.ReceiveLogs(ctx, body, orgID); err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Errorf("%w: %w", ErrLogsProcess, err))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte{})
}

// handleOTLPMetrics handles OTLP metrics export via HTTP
// Endpoint: POST /v1/metrics
func handleOTLPMetrics(w http.ResponseWriter, r *http.Request) {
	if otlpReceiver == nil {
		respondError(w, http.StatusServiceUnavailable, ErrOTLPReceiverUnavailable)
		return
	}

	// Authenticate before reading: an anonymous caller must not get a body decoded.
	orgID, ok := resolveIngestOrg(otlpDB, r.Header.Get("Authorization"))
	if !ok {
		respondError(w, http.StatusUnauthorized, ErrIngestTokenRequired)
		return
	}

	body, err := readOTLPBody(w, r)
	if err != nil {
		respondOTLPBodyError(w, err)
		return
	}
	defer r.Body.Close()

	contentType := r.Header.Get("Content-Type")
	if contentType != "application/x-protobuf" && contentType != "application/protobuf" {
		slog.Warn("unsupported content-type for metrics", "content_type", contentType)
		respondError(w, http.StatusUnsupportedMediaType, ErrContentTypeNotProtobuf)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	metered, err := applyIngestBudget(body, orgID)
	if err != nil {
		respondPublishError(w, err)
		return
	}

	if metered.granted > 0 || metered.rejected == 0 {
		if services.KafkaWriterMetrics != nil {
			if err := services.PublishOTLP(services.KafkaWriterMetrics, "metrics", metered.body, orgID); err != nil {
				// Not stored, so not charged: the client retries the 503.
				ingestBudget.Refund(orgID, metered.granted)
				respondPublishError(w, err)
				return
			}
		} else if err := otlpReceiver.ReceiveMetrics(ctx, metered.body, orgID); err != nil {
			respondError(w, http.StatusInternalServerError, fmt.Errorf("%w: %w", ErrMetricsProcess, err))
			return
		}
	}

	if metered.over > 0 {
		writePartialSuccess(w, metered)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte{})
}
