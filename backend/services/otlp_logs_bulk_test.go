package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

func logsRequest(t *testing.T, n int) []byte {
	t.Helper()
	records := make([]*logspb.LogRecord, n)
	for i := range records {
		records[i] = &logspb.LogRecord{
			SeverityText: "INFO",
			Body:         &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "GET /orders 200"}},
		}
	}
	data, err := proto.Marshal(&collogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
		Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
			{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "api"}}},
		}},
		ScopeLogs: []*logspb.ScopeLogs{{LogRecords: records}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The agent ships tailed files a thousand lines at a time; one HTTP round trip
// per line kept the worker busy on overhead and fell behind a single busy host.
func TestReceiveLogsIndexesARequestInOneBulk(t *testing.T) {
	f := &fakeBulk{indices: map[string]int{}}
	s := NewOTLPReceiverService(bulkService(t, f), nil)

	if err := s.ReceiveLogs(context.Background(), logsRequest(t, 1000), 7); err != nil {
		t.Fatal(err)
	}
	if f.requests != 1 || f.docs != 1000 || f.indices["middle-monitor-logs"] != 1000 {
		t.Errorf("%d requests, %d docs, indices %v: want one bulk of 1000 into middle-monitor-logs", f.requests, f.docs, f.indices)
	}
}

// A bulk answers 200 even when documents are refused: a mapping conflict on a
// customer's log attributes must surface, without quoting the rejected value.
func TestBulkIndexReportsRefusedDocuments(t *testing.T) {
	f := &fakeBulk{indices: map[string]int{}, answer: `{"errors":true,"items":[{"index":{"error":{"type":"mapper_parsing_exception","reason":"failed to parse field [attributes.user]. Preview of field's value: 'alice@example.com'"}}},{"index":{}}]}`}
	s := bulkService(t, f)

	err := s.BulkIndex(context.Background(), "middle-monitor-logs", []map[string]interface{}{{"body": "a"}, {"body": "b"}})
	var bulkErr *BulkIndexError
	if !errors.As(err, &bulkErr) || bulkErr.Failed != 1 || bulkErr.Total != 2 {
		t.Fatalf("got %v, want 1 of 2 refused", err)
	}
	if !errors.Is(err, ErrIndexBulk) || strings.Contains(err.Error(), "alice") {
		t.Errorf("want the reason without the rejected value: %v", err)
	}
}
