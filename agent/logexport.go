package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

const (
	logBatchRecords = 1000
	logBatchBytes   = 1 << 20
	logBatchDelay   = time.Second
	logRetryMax     = 30 * time.Second
	// Time left to send the last batch when the agent stops.
	logShutdownGrace = 5 * time.Second
)

// logExporter posts batches to the OTLP logs endpoint the SDKs use.
type logExporter struct {
	endpoint string
	token    string
	client   *http.Client
	// Zero means no limit.
	maxLinesPerSecond int
}

func newLogExporter(apiURL, token string, maxLinesPerSecond int) (*logExporter, error) {
	if !strings.HasPrefix(apiURL, "http://") && !strings.HasPrefix(apiURL, "https://") {
		apiURL = "http://" + apiURL
	}
	parsed, err := url.Parse(apiURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errAPIURLInvalid, err)
	}
	return &logExporter{
		// Same host as the metrics exporter; the api routes /v1 to the receiver.
		endpoint:          parsed.Scheme + "://" + parsed.Host + "/v1/logs",
		token:             token,
		client:            &http.Client{Timeout: 30 * time.Second},
		maxLinesPerSecond: maxLinesPerSecond,
	}, nil
}

// run batches records by count, size and age until ctx is done, then sends
// what is left within a short grace period.
func (e *logExporter) run(ctx context.Context, records <-chan logRecord, positions *logPositions) {
	var batch []logRecord
	size := 0
	ticker := time.NewTicker(logBatchDelay)
	defer ticker.Stop()
	flush := func(ctx context.Context) {
		if len(batch) > 0 {
			e.send(ctx, batch, positions)
		}
		batch, size = nil, 0
	}
	for {
		select {
		case rec := <-records:
			batch = append(batch, rec)
			size += len(rec.body)
			if len(batch) >= logBatchRecords || size >= logBatchBytes {
				flush(ctx)
			}
		case <-ticker.C:
			flush(ctx)
		case <-ctx.Done():
			for drained := false; !drained; {
				select {
				case rec := <-records:
					batch = append(batch, rec)
				default:
					drained = true
				}
			}
			grace, cancel := context.WithTimeout(context.Background(), logShutdownGrace)
			e.sendOnce(grace, batch, positions)
			cancel()
			return
		}
	}
}

// send retries what a second try can fix until it lands or ctx ends. A batch
// the backend refuses outright is dropped: retrying would block every file.
func (e *logExporter) send(ctx context.Context, batch []logRecord, positions *logPositions) {
	started := time.Now()
	body, err := encodeLogBatch(batch)
	if err != nil {
		slog.Warn("encode log batch failed, dropping it", "records", len(batch), "err", err)
		ackBatch(batch, positions)
		return
	}
	backoff := time.Second
	for {
		err := e.post(ctx, body)
		if err == nil {
			break
		}
		if !retryableLogExport(err) {
			slog.Warn("log batch refused, dropping it", "records", len(batch), "err", err)
			break
		}
		slog.Warn("export logs retry", "records", len(batch), "backoff", backoff, "err", err)
		select {
		case <-ctx.Done():
			// Not acknowledged: the lines are read again after the restart.
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, logRetryMax)
	}
	ackBatch(batch, positions)
	e.throttle(ctx, len(batch), time.Since(started))
}

func (e *logExporter) sendOnce(ctx context.Context, batch []logRecord, positions *logPositions) {
	if len(batch) == 0 {
		return
	}
	body, err := encodeLogBatch(batch)
	if err == nil {
		err = e.post(ctx, body)
	}
	if err != nil {
		slog.Warn("last log batch not sent, it will be read again", "records", len(batch), "err", err)
		return
	}
	ackBatch(batch, positions)
}

// throttle holds the exporter so that n lines take at least n/limit seconds.
// The queue then fills and the tailers wait: lines are delayed, never lost.
func (e *logExporter) throttle(ctx context.Context, n int, spent time.Duration) {
	if e.maxLinesPerSecond <= 0 {
		return
	}
	wait := time.Duration(n)*time.Second/time.Duration(e.maxLinesPerSecond) - spent
	if wait <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(wait):
	}
}

func ackBatch(batch []logRecord, positions *logPositions) {
	for _, rec := range batch {
		positions.ack(rec)
	}
}

func (e *logExporter) post(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: %w", errRequestCreate, err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "gzip")
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", errLogExport, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		answer, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &logExportStatusError{StatusCode: resp.StatusCode, Body: string(answer)}
	}
	return nil
}

// retryableLogExport is true for no answer, rate limiting or a server failure.
func retryableLogExport(err error) bool {
	var status *logExportStatusError
	if errors.As(err, &status) {
		return status.StatusCode == http.StatusTooManyRequests || status.StatusCode >= 500
	}
	return true
}

// encodeLogBatch builds one OTLP request, one resource per file owner, gzipped.
func encodeLogBatch(batch []logRecord) ([]byte, error) {
	data, err := proto.Marshal(buildLogsRequest(batch))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errLogEncode, err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		return nil, fmt.Errorf("%w: %w", errLogEncode, err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("%w: %w", errLogEncode, err)
	}
	return buf.Bytes(), nil
}

func buildLogsRequest(batch []logRecord) *collogspb.ExportLogsServiceRequest {
	req := &collogspb.ExportLogsServiceRequest{}
	scopes := map[*logOrigin]*logspb.ScopeLogs{}
	for _, rec := range batch {
		scope, ok := scopes[rec.origin]
		if !ok {
			scope = &logspb.ScopeLogs{Scope: &commonpb.InstrumentationScope{Name: "middle-monitor-agent", Version: Version}}
			req.ResourceLogs = append(req.ResourceLogs, &logspb.ResourceLogs{
				Resource:  &resourcepb.Resource{Attributes: rec.origin.resource()},
				ScopeLogs: []*logspb.ScopeLogs{scope},
			})
			scopes[rec.origin] = scope
		}
		var attrs []*commonpb.KeyValue
		if rec.path != "" {
			attrs = append(attrs, stringAttr("log.file.path", rec.path))
		}
		if rec.stream != "" {
			attrs = append(attrs, stringAttr("log.iostream", rec.stream))
		}
		scope.LogRecords = append(scope.LogRecords, &logspb.LogRecord{
			TimeUnixNano:         uint64(rec.time.UnixNano()),
			ObservedTimeUnixNano: uint64(rec.observed.UnixNano()),
			SeverityNumber:       severityNumbers[rec.severity],
			SeverityText:         rec.severity,
			Body:                 &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: rec.body}},
			Attributes:           attrs,
		})
	}
	return req
}
