package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"middle-monitor/backend/services"
)

func gzipped(b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

// OTLP/HTTP servers must accept gzip: exporters compress, and compression is
// what keeps an agent scrape small on the wire.
func TestReadOTLPBodyDecodesGzip(t *testing.T) {
	payload := bytes.Repeat([]byte("otlp"), 1000)
	req := httptest.NewRequest("POST", "/v1/metrics", bytes.NewReader(gzipped(payload)))
	req.Header.Set("Content-Encoding", "gzip")

	got, err := readOTLPBody(httptest.NewRecorder(), req)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("got %d bytes, err %v", len(got), err)
	}
}

// A small compressed body can expand without bound; the decoded cap is what
// keeps one request from taking the receiver's memory.
func TestReadOTLPBodyCapsTheDecodedSize(t *testing.T) {
	bomb := gzipped(make([]byte, maxOTLPDecodedBytes+1))
	req := httptest.NewRequest("POST", "/v1/metrics", bytes.NewReader(bomb))
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()

	_, err := readOTLPBody(rec, req)
	respondOTLPBodyError(rec, err)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status %d, want 413", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("export smaller batches")) {
		t.Errorf("the 413 must tell the client what to change, got %s", rec.Body.String())
	}
}

func TestReadOTLPBodyRefusesAnUnknownEncoding(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1/metrics", bytes.NewReader([]byte("x")))
	req.Header.Set("Content-Encoding", "br")
	rec := httptest.NewRecorder()

	_, err := readOTLPBody(rec, req)
	respondOTLPBodyError(rec, err)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status %d, want 415", rec.Code)
	}
}

// OTLP exporters retry 429/502/503/504 and give up on 500: a broker hiccup
// answered 500 was data lost for good.
func TestPublishFailureIsRetryable(t *testing.T) {
	rec := httptest.NewRecorder()
	respondPublishError(rec, services.ErrKafkaPublish)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Errorf("status %d Retry-After %q, want 503 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
}

// trackingBody records whether the handler read the request body at all.
type trackingBody struct{ read bool }

func (b *trackingBody) Read(p []byte) (int, error) { b.read = true; return 0, io.EOF }
func (b *trackingBody) Close() error               { return nil }

// Anonymous OTLP used to be stored with no organization, outside any budget:
// anyone with the URL could fill the series store. It must be refused, before
// the body is even decoded.
func TestOTLPRefusesAnonymousRequests(t *testing.T) {
	previous := otlpReceiver
	otlpReceiver = &services.OTLPReceiverService{}
	t.Cleanup(func() { otlpReceiver = previous })

	for name, handler := range map[string]http.HandlerFunc{
		"traces": handleOTLPTraces, "logs": handleOTLPLogs, "metrics": handleOTLPMetrics,
	} {
		body := &trackingBody{}
		req := httptest.NewRequest("POST", "/v1/"+name, nil)
		req.Body = body
		req.Header.Set("Content-Type", "application/x-protobuf")
		rec := httptest.NewRecorder()

		handler(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, rec.Code)
		}
		if body.read {
			t.Errorf("%s: the body was read before authentication", name)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte("Authorization: Bearer")) {
			t.Errorf("%s: the 401 must say what to send: %s", name, rec.Body.String())
		}
	}
}
