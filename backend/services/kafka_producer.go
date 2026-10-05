package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/segmentio/kafka-go"
)

var (
	KafkaWriterTraces       *kafka.Writer
	KafkaWriterLogs         *kafka.Writer
	KafkaWriterMetrics      *kafka.Writer
	KafkaWriterSDKErrors    *kafka.Writer
	KafkaWriterSDKMetrics   *kafka.Writer
	KafkaWriterAgentMetrics *kafka.Writer
	KafkaWriterHostEvents   *kafka.Writer
)

const (
	TopicTraces       = "topic-traces"
	TopicLogs         = "topic-logs"
	TopicMetrics      = "topic-metrics"
	TopicSDKErrors    = "topic-sdk-errors"
	TopicSDKMetrics   = "topic-sdk-metrics"
	TopicAgentMetrics = "topic-agent-metrics"
	TopicHostEvents   = "topic-host-events"
)

// InitKafkaProducers connects to Kafka and initializes the writers
func InitKafkaProducers() {
	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers == "" {
		brokers = "localhost:9092"
	}

	KafkaWriterTraces = newKafkaWriter(brokers, TopicTraces)
	KafkaWriterLogs = newKafkaWriter(brokers, TopicLogs)
	KafkaWriterMetrics = newKafkaWriter(brokers, TopicMetrics)
	KafkaWriterSDKErrors = newKafkaWriter(brokers, TopicSDKErrors)
	KafkaWriterSDKMetrics = newKafkaWriter(brokers, TopicSDKMetrics)
	KafkaWriterAgentMetrics = newKafkaWriter(brokers, TopicAgentMetrics)
	KafkaWriterHostEvents = newKafkaWriter(brokers, TopicHostEvents)

	slog.Info("kafka producers initialized", "brokers", brokers)
}

func newKafkaWriter(brokers string, topic string) *kafka.Writer {
	return &kafka.Writer{
		Addr:     kafka.TCP(brokers),
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
		// Synchronous with a leader ack: async mode ignored every broker error, so a
		// Kafka restart lost data while the client was told 200. A failed write now
		// reaches the handler, which answers 503 and lets the client retry.
		RequiredAcks: kafka.RequireOne,
		// kafka-go retries a failed batch itself before giving up.
		MaxAttempts: 5,
		// The default 1s linger would add a second to every ingestion request.
		BatchTimeout: 10 * time.Millisecond,
		// Raw OTLP compresses about tenfold; the consumer decompresses transparently.
		Compression: kafka.Zstd,
	}
}

// KafkaHeaderOrgID is the Kafka message header used to carry the resolved
// organization id alongside raw OTLP payloads (traces/logs/metrics), since the
// protobuf body itself has no place for our org scoping.
const KafkaHeaderOrgID = "org_id"

// PublishPayload marshals an empty interface to JSON and pushes to the topic
func PublishPayload(writer *kafka.Writer, key string, payload interface{}) error {
	return PublishPayloadWithOrg(writer, key, payload, 0)
}

// PublishPayloadWithOrg is PublishPayload plus an org_id message header (added
// when orgID > 0) so the consumer can scope the data to an organization.
func PublishPayloadWithOrg(writer *kafka.Writer, key string, payload interface{}, orgID int64) error {
	if writer == nil {
		return ErrKafkaWriterMissing
	}

	var data []byte
	var err error
	if rawBytes, ok := payload.([]byte); ok {
		data = rawBytes
	} else {
		data, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrPayloadMarshal, err)
		}
	}

	msg := kafka.Message{
		Key:   []byte(key),
		Value: data,
	}
	if orgID > 0 {
		msg.Headers = []kafka.Header{{
			Key:   KafkaHeaderOrgID,
			Value: []byte(strconv.FormatInt(orgID, 10)),
		}}
	}

	if err := writer.WriteMessages(context.Background(), msg); err != nil {
		slog.Error("kafka write failed", "topic", writer.Topic, "error", err)
		return ErrKafkaPublish
	}

	return nil
}

// PublishOTLP publishes a raw OTLP request, split into several messages when it
// would exceed the broker's message size. The chunks go out in one write.
func PublishOTLP(writer *kafka.Writer, signal string, body []byte, orgID int64) error {
	if writer == nil {
		return ErrKafkaWriterMissing
	}
	chunks, err := SplitOTLP(signal, body, MaxOTLPMessageBytes)
	if err != nil {
		return err
	}
	msgs := make([]kafka.Message, 0, len(chunks))
	for _, chunk := range chunks {
		msg := kafka.Message{Key: []byte(signal), Value: chunk}
		if orgID > 0 {
			msg.Headers = []kafka.Header{{Key: KafkaHeaderOrgID, Value: []byte(strconv.FormatInt(orgID, 10))}}
		}
		msgs = append(msgs, msg)
	}
	if err := writer.WriteMessages(context.Background(), msgs...); err != nil {
		slog.Error("kafka write failed", "topic", writer.Topic, "messages", len(msgs), "error", err)
		return ErrKafkaPublish
	}
	if len(chunks) > 1 {
		slog.Info("split otlp request", "signal", signal, "bytes", len(body), "messages", len(chunks))
	}
	return nil
}

// OrgIDFromHeaders extracts the org_id Kafka header, or 0 when absent/invalid.
func OrgIDFromHeaders(headers []kafka.Header) int64 {
	for _, h := range headers {
		if h.Key == KafkaHeaderOrgID {
			if v, err := strconv.ParseInt(string(h.Value), 10, 64); err == nil {
				return v
			}
		}
	}
	return 0
}
