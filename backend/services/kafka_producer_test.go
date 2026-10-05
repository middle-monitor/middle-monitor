package services

import (
	"strings"
	"testing"

	"github.com/segmentio/kafka-go"
)

func TestPublishPayload_NilWriter(t *testing.T) {
	err := PublishPayload(nil, "key", "payload")
	if err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected not initialized error, got %v", err)
	}
}

func TestPublishPayload_BytesPayload(t *testing.T) {
	w := &kafka.Writer{
		Addr:  kafka.TCP("localhost:19092"), // non-existent broker
		Topic: "test",
		Async: true,
	}
	defer w.Close()
	// Bytes payload path; async writer may return nil
	_ = PublishPayload(w, "key", []byte(`{"test":true}`))
}

func TestPublishPayload_StructPayload(t *testing.T) {
	w := &kafka.Writer{
		Addr:  kafka.TCP("localhost:19092"),
		Topic: "test",
		Async: true,
	}
	defer w.Close()
	// Struct payload goes through json.Marshal path
	_ = PublishPayload(w, "key", map[string]string{"hello": "world"})
}

func TestPublishPayload_UnmarshalablePayload(t *testing.T) {
	w := &kafka.Writer{
		Addr:  kafka.TCP("localhost:19092"),
		Topic: "test",
		Async: true,
	}
	defer w.Close()
	// Channels can't be JSON-marshaled
	err := PublishPayload(w, "key", make(chan int))
	if err == nil || !strings.Contains(err.Error(), "marshal") {
		t.Fatalf("expected marshal error, got %v", err)
	}
}

func TestInitKafkaProducers_SetsGlobalWriters(t *testing.T) {
	t.Setenv("KAFKA_BROKERS", "localhost:19092")
	InitKafkaProducers()
	if KafkaWriterTraces == nil || KafkaWriterLogs == nil {
		t.Fatal("expected kafka writers to be set")
	}
}
