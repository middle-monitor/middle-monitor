package services

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func statusResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body))}
}

// The worker logged "status 400" for months while OpenSearch was saying exactly
// which field mapping refused the document; the reason must reach the log.
func TestOpenSearchStatusErrorKeepsTheRejectionReason(t *testing.T) {
	body := `{"error":{"root_cause":[],"type":"mapper_parsing_exception",` +
		`"reason":"failed to parse field [metadata] of type [text] in document with id 'x'. ` +
		`Preview of field's value: '{http_server_ms=9.5, token=secret}'"},"status":400}`

	got := newOpenSearchStatusError("index document", statusResponse(400, body)).Error()

	want := "opensearch index document: status 400: mapper_parsing_exception: " +
		"failed to parse field [metadata] of type [text] in document with id 'x'"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if strings.Contains(got, "secret") {
		t.Error("the rejected value must not be logged: it can be customer data")
	}
}

func TestOpenSearchStatusErrorWithoutAnExplanation(t *testing.T) {
	for _, body := range []string{"", "<html>bad gateway</html>", `{"error":{}}`} {
		got := newOpenSearchStatusError("search", statusResponse(502, body)).Error()
		if got != "opensearch search: status 502" {
			t.Errorf("%q: got %q", body, got)
		}
	}
}
