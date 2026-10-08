package services

import (
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func intAttr(k string, v int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v}}}
}

// reportError is what the docs tell users to call. Its span is the only trace
// of the error, so if it does not become an Errors entry the Errors page stays
// empty for every manually reported error.
func TestReportErrorSpanBecomesAnErrorsEntry(t *testing.T) {
	span := &tracepb.Span{
		Name:    "error.report",
		TraceId: []byte{0xab, 0xcd},
		Attributes: []*commonpb.KeyValue{
			strAttr("error.type", "SyntaxError"),
			strAttr("error.message", "unexpected token"),
			strAttr("error.file", "/app/checkout.js"),
			intAttr("error.line", 42),
		},
	}

	appErr, ok := errorFromReportSpan(span, "checkout", 7)
	if !ok {
		t.Fatal("an error.report span was dropped: the Errors page never sees it")
	}
	if appErr.OrganizationID != 7 || appErr.Service != "checkout" {
		t.Errorf("filed under org %d service %q, want org 7 service checkout", appErr.OrganizationID, appErr.Service)
	}
	if appErr.Name != "SyntaxError" || appErr.Message != "unexpected token" || appErr.File != "/app/checkout.js" || appErr.Line != 42 {
		t.Errorf("error details lost: %+v", appErr)
	}
	if appErr.TraceID == nil || *appErr.TraceID != "abcd" {
		t.Error("trace id dropped: the error can no longer be correlated with its trace")
	}
}

// The Go SDK sends no error.type; its entries must still show a name.
func TestReportErrorSpanWithoutTypeIsStillNamed(t *testing.T) {
	span := &tracepb.Span{Name: "error.report", Attributes: []*commonpb.KeyValue{strAttr("error.message", "db timeout")}}

	appErr, ok := errorFromReportSpan(span, "api", 3)
	if !ok || appErr.Name == "" || appErr.File == "" {
		t.Fatalf("got %+v, %v; want a named entry with a file placeholder", appErr, ok)
	}
}

// Request spans and middleware errors reach the Errors page through
// POST /api/v1/errors; reading them here as well would count each one twice.
func TestOrdinarySpansDoNotCreateErrors(t *testing.T) {
	span := &tracepb.Span{
		Name:       "GET /checkout",
		Attributes: []*commonpb.KeyValue{strAttr("error.message", "boom")},
		Status:     &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: "boom"},
	}
	if _, ok := errorFromReportSpan(span, "api", 3); ok {
		t.Error("a request span was turned into an error entry")
	}
}

// CreateError files an error without an org under org 1, which would show one
// tenant's errors to another.
func TestReportErrorSpanWithoutOrgIsDropped(t *testing.T) {
	span := &tracepb.Span{Name: "error.report", Attributes: []*commonpb.KeyValue{strAttr("error.message", "boom")}}
	if _, ok := errorFromReportSpan(span, "api", 0); ok {
		t.Error("an error with no organization would be filed under org 1")
	}
}
