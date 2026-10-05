package workers

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/segmentio/kafka-go"
	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

// AgentMetricsPayload is used to wrap the payload with orgID for agent metrics
type AgentMetricsPayload struct {
	Metrics models.AgentMetrics `json:"metrics"`
	OrgID   int64               `json:"org_id"`
}

var (
	otlpReceiver *services.OTLPReceiverService
)

func StartKafkaConsumers(db *sql.DB, opensearch *services.OpenSearchService) {
	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers == "" {
		slog.Warn("kafka brokers not set, skipping consumers")
		return
	}

	if opensearch != nil {
		otlpReceiver = services.NewOTLPReceiverService(opensearch, db)
	}

	errorService := services.NewErrorService(db)
	agentService := services.NewAgentService(db)

	slog.Info("starting kafka consumers", "brokers", brokers)

	go consumeTopic(brokers, services.TopicTraces, "worker-group", func(msg kafka.Message) {
		if otlpReceiver != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := otlpReceiver.ReceiveTraces(ctx, msg.Value, services.OrgIDFromHeaders(msg.Headers)); err != nil {
				slog.Error("failed to process traces", "error", err)
			}
		}
	})

	go consumeTopic(brokers, services.TopicLogs, "worker-group", func(msg kafka.Message) {
		if otlpReceiver != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := otlpReceiver.ReceiveLogs(ctx, msg.Value, services.OrgIDFromHeaders(msg.Headers)); err != nil {
				slog.Error("failed to process logs", "error", err)
			}
		}
	})

	go consumeTopic(brokers, services.TopicMetrics, "worker-group", func(msg kafka.Message) {
		if otlpReceiver != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := otlpReceiver.ReceiveMetrics(ctx, msg.Value, services.OrgIDFromHeaders(msg.Headers)); err != nil {
				slog.Error("failed to process metrics", "error", err)
			}
		}
	})

	go consumeTopic(brokers, services.TopicSDKErrors, "worker-group", func(msg kafka.Message) {
		var appErr models.ApplicationError
		if err := json.Unmarshal(msg.Value, &appErr); err == nil {
			result, err := errorService.CreateError(appErr)
			if err != nil {
				slog.Error("failed to save sdk error", "error", err)
				return
			}
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
		} else {
			slog.Error("failed to parse sdk error payload", "error", err)
		}
	})

	go consumeTopic(brokers, services.TopicAgentMetrics, "worker-group", func(msg kafka.Message) {
		var payload AgentMetricsPayload
		if err := json.Unmarshal(msg.Value, &payload); err == nil {
			if err := agentService.StoreMetrics(payload.Metrics, payload.OrgID); err != nil {
				slog.Error("failed to store agent metrics", "error", err)
			}
		} else {
			slog.Error("failed to parse agent metrics payload", "error", err)
		}
	})
}

// consumeTopic reads messages from the topic
func consumeTopic(brokers, topic, groupID string, handler func(kafka.Message)) {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  []string{brokers},
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3, // 10KB
		MaxBytes: 10e6, // 10MB
		// Topics are created by the first message produced, often after this
		// reader joined: without the watch it keeps an empty assignment forever.
		WatchPartitionChanges: true,
	})

	slog.Info("kafka consumer listening", "topic", topic)

	for {
		m, err := r.FetchMessage(context.Background())
		if err != nil {
			slog.Error("kafka consumer stopped", "topic", topic, "error", err)
			break
		}

		handler(m)

		if err := r.CommitMessages(context.Background(), m); err != nil {
			slog.Error("kafka consumer commit failed", "topic", topic, "error", err)
		}
	}

	if err := r.Close(); err != nil {
		slog.Error("kafka consumer close failed", "topic", topic, "error", err)
	}
}
