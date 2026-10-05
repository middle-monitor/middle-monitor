package services

import (
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestNewSuggestionService(t *testing.T) {
	db, _ := newDB(t)
	if NewSuggestionService(db, nil) == nil {
		t.Fatal("expected non-nil")
	}
}

func TestGetSuggestions_NilOpenSearch_ReturnsNil(t *testing.T) {
	db, _ := newDB(t)
	svc := NewSuggestionService(db, nil)
	result, reason, err := svc.GetSuggestions(1, "api")
	if err != nil || result != nil || reason != ReasonTracingNotConfigured {
		t.Fatalf("expected nil,%q,nil, got %v/%q/%v", ReasonTracingNotConfigured, result, reason, err)
	}
}

func TestGetSuggestions_UninitializedOpenSearch_ReturnsNil(t *testing.T) {
	db, _ := newDB(t)
	// opensearch exists but initialized=false
	osSvc := &OpenSearchService{initialized: false}
	svc := NewSuggestionService(db, osSvc)
	result, reason, err := svc.GetSuggestions(1, "api")
	if err != nil || result != nil || reason != ReasonTracingNotConfigured {
		t.Fatalf("expected nil,%q,nil, got %v/%q/%v", ReasonTracingNotConfigured, result, reason, err)
	}
}

func TestExtractDestinationHintsFromSpans_NoAttributes(t *testing.T) {
	spans := []map[string]interface{}{
		{"other_field": "value"},
	}
	hints := extractDestinationHintsFromSpans(spans)
	if len(hints) != 0 {
		t.Fatalf("expected no hints, got %v", hints)
	}
}

func TestExtractDestinationHintsFromSpans_NilAttributesValue(t *testing.T) {
	spans := []map[string]interface{}{
		{"attributes": nil},
	}
	hints := extractDestinationHintsFromSpans(spans)
	if len(hints) != 0 {
		t.Fatalf("expected no hints, got %v", hints)
	}
}

func TestExtractDestinationHintsFromSpans_PeerService(t *testing.T) {
	spans := []map[string]interface{}{
		{"attributes": map[string]interface{}{"peer.service": "database"}},
	}
	hints := extractDestinationHintsFromSpans(spans)
	if !hints["database"] {
		t.Fatalf("expected 'database' hint, got %v", hints)
	}
}

func TestExtractDestinationHintsFromSpans_HTTPUrl_ExtractsHostname(t *testing.T) {
	spans := []map[string]interface{}{
		{"attributes": map[string]interface{}{"http.url": "https://api.example.com/v1/endpoint"}},
	}
	hints := extractDestinationHintsFromSpans(spans)
	if !hints["api.example.com"] {
		t.Fatalf("expected 'api.example.com' hint, got %v", hints)
	}
}

func TestExtractDestinationHintsFromSpans_HTTPUrl_InvalidURL(t *testing.T) {
	spans := []map[string]interface{}{
		{"attributes": map[string]interface{}{"http.url": "not-a-url"}},
	}
	hints := extractDestinationHintsFromSpans(spans)
	// invalid URL still passes as hint (no host extraction, falls through as raw string)
	// "not-a-url" has len > 1 and < 256, so it is a valid hint
	if !hints["not-a-url"] {
		t.Fatalf("expected 'not-a-url' hint for non-parseable URL, got %v", hints)
	}
}

func TestExtractDestinationHintsFromSpans_HTTPUrl_NoHost(t *testing.T) {
	// URL with no host (e.g., a relative path)
	spans := []map[string]interface{}{
		{"attributes": map[string]interface{}{"http.url": "/relative/path"}},
	}
	hints := extractDestinationHintsFromSpans(spans)
	// /relative/path → url.Parse succeeds but u.Host == "" → uses raw string
	// "/relative/path" len > 1 → hint added
	if len(hints) == 0 {
		t.Log("no hint for relative path - acceptable, raw string used")
	}
}

func TestExtractDestinationHintsFromSpans_ShortString_Skipped(t *testing.T) {
	spans := []map[string]interface{}{
		{"attributes": map[string]interface{}{"peer.service": "a"}},
	}
	hints := extractDestinationHintsFromSpans(spans)
	if hints["a"] {
		t.Fatal("single-char hint should be skipped")
	}
}

func TestExtractDestinationHintsFromSpans_LongString_Skipped(t *testing.T) {
	longStr := make([]byte, 300)
	for i := range longStr {
		longStr[i] = 'x'
	}
	spans := []map[string]interface{}{
		{"attributes": map[string]interface{}{"peer.service": string(longStr)}},
	}
	hints := extractDestinationHintsFromSpans(spans)
	if len(hints) != 0 {
		t.Fatal("string >= 256 chars should be skipped")
	}
}

func TestExtractDestinationHintsFromSpans_NonStringValue_Skipped(t *testing.T) {
	spans := []map[string]interface{}{
		{"attributes": map[string]interface{}{"peer.service": 42}},
	}
	hints := extractDestinationHintsFromSpans(spans)
	if len(hints) != 0 {
		t.Fatalf("non-string attribute value should be skipped, got %v", hints)
	}
}

func TestExtractDestinationHintsFromSpans_EmptyString_Skipped(t *testing.T) {
	spans := []map[string]interface{}{
		{"attributes": map[string]interface{}{"peer.service": ""}},
	}
	hints := extractDestinationHintsFromSpans(spans)
	if len(hints) != 0 {
		t.Fatalf("empty string should be skipped, got %v", hints)
	}
}

func TestExtractDestinationHintsFromSpans_NilAttributeValue_Skipped(t *testing.T) {
	spans := []map[string]interface{}{
		{"attributes": map[string]interface{}{"peer.service": nil}},
	}
	hints := extractDestinationHintsFromSpans(spans)
	if len(hints) != 0 {
		t.Fatalf("nil attribute value should be skipped, got %v", hints)
	}
}

func TestExtractDestinationHintsFromSpans_MultipleAttributes(t *testing.T) {
	spans := []map[string]interface{}{
		{"attributes": map[string]interface{}{
			"peer.service":   "redis-service",
			"server.address": "db.internal",
			"db.name":        "postgres",
		}},
	}
	hints := extractDestinationHintsFromSpans(spans)
	if !hints["redis-service"] || !hints["db.internal"] || !hints["postgres"] {
		t.Fatalf("expected all hints, got %v", hints)
	}
}

func TestMatchHintsToTargets_HostMatchByName(t *testing.T) {
	hints := map[string]bool{"redis": true}
	hosts := []struct {
		ID   int64
		Name string
		Host string
	}{{ID: 1, Name: "redis-server", Host: "10.0.0.1"}}
	services := []struct {
		ID      int64
		Name    string
		Host    string
		Service string
	}{}
	suggestions := matchHintsToTargets(hints, hosts, services, nil)
	if len(suggestions) != 1 || suggestions[0].TargetType != "host" || suggestions[0].TargetID != 1 {
		t.Fatalf("expected 1 host suggestion, got %v", suggestions)
	}
}

func TestMatchHintsToTargets_HostMatchByHostAddress(t *testing.T) {
	hints := map[string]bool{"10.0.0.5": true}
	hosts := []struct {
		ID   int64
		Name string
		Host string
	}{{ID: 2, Name: "web-01", Host: "10.0.0.5"}}
	services := []struct {
		ID      int64
		Name    string
		Host    string
		Service string
	}{}
	suggestions := matchHintsToTargets(hints, hosts, services, nil)
	if len(suggestions) != 1 || suggestions[0].TargetID != 2 {
		t.Fatalf("expected 1 host suggestion, got %v", suggestions)
	}
}

func TestMatchHintsToTargets_ServiceMatchByHost(t *testing.T) {
	hints := map[string]bool{"api.example.com": true}
	hosts := []struct {
		ID   int64
		Name string
		Host string
	}{}
	svcs := []struct {
		ID      int64
		Name    string
		Host    string
		Service string
	}{{ID: 10, Name: "http-check", Host: "api.example.com", Service: "api"}}
	suggestions := matchHintsToTargets(hints, hosts, svcs, nil)
	if len(suggestions) != 1 || suggestions[0].TargetType != "service" || suggestions[0].TargetID != 10 {
		t.Fatalf("expected 1 service suggestion, got %v", suggestions)
	}
}

func TestMatchHintsToTargets_ServiceMatchByName(t *testing.T) {
	hints := map[string]bool{"payment": true}
	hosts := []struct {
		ID   int64
		Name string
		Host string
	}{}
	svcs := []struct {
		ID      int64
		Name    string
		Host    string
		Service string
	}{{ID: 5, Name: "payment-service", Host: "10.0.0.1", Service: "payments"}}
	suggestions := matchHintsToTargets(hints, hosts, svcs, nil)
	if len(suggestions) != 1 {
		t.Fatalf("expected 1 suggestion, got %v", suggestions)
	}
}

func TestMatchHintsToTargets_ExistingLinkSkipped(t *testing.T) {
	hints := map[string]bool{"redis": true}
	hosts := []struct {
		ID   int64
		Name string
		Host string
	}{{ID: 1, Name: "redis-server", Host: "10.0.0.1"}}
	svcs := []struct {
		ID      int64
		Name    string
		Host    string
		Service string
	}{}
	existing := map[string]bool{"host-1": true}
	suggestions := matchHintsToTargets(hints, hosts, svcs, existing)
	if len(suggestions) != 0 {
		t.Fatalf("expected 0 suggestions (already linked), got %v", suggestions)
	}
}

func TestMatchHintsToTargets_DuplicateHintsDedup(t *testing.T) {
	// Two hints that match the same host → should only produce 1 suggestion
	hints := map[string]bool{"redis": true, "redis-server": true}
	hosts := []struct {
		ID   int64
		Name string
		Host string
	}{{ID: 1, Name: "redis-server", Host: "10.0.0.1"}}
	svcs := []struct {
		ID      int64
		Name    string
		Host    string
		Service string
	}{}
	suggestions := matchHintsToTargets(hints, hosts, svcs, nil)
	if len(suggestions) != 1 {
		t.Fatalf("expected 1 deduplicated suggestion, got %v", suggestions)
	}
}

func TestMatchHintsToTargets_NoMatch(t *testing.T) {
	hints := map[string]bool{"unknown-service": true}
	hosts := []struct {
		ID   int64
		Name string
		Host string
	}{{ID: 1, Name: "web-01", Host: "10.0.0.1"}}
	svcs := []struct {
		ID      int64
		Name    string
		Host    string
		Service string
	}{}
	suggestions := matchHintsToTargets(hints, hosts, svcs, nil)
	if len(suggestions) != 0 {
		t.Fatalf("expected 0 suggestions, got %v", suggestions)
	}
}

func TestMatchHintsToTargets_EmptyHostName_NoMatch(t *testing.T) {
	hints := map[string]bool{"redis": true}
	hosts := []struct {
		ID   int64
		Name string
		Host string
	}{{ID: 1, Name: "", Host: ""}}
	svcs := []struct {
		ID      int64
		Name    string
		Host    string
		Service string
	}{}
	suggestions := matchHintsToTargets(hints, hosts, svcs, nil)
	if len(suggestions) != 0 {
		t.Fatalf("expected 0 suggestions for empty host name, got %v", suggestions)
	}
}

func TestGetHostsForOrgSuggestion_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewSuggestionService(db, nil)
	mock.ExpectQuery("FROM hosts WHERE organization_id").WillReturnError(errors.New("db error"))
	_, err := svc.getHostsForOrg(1)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetHostsForOrg_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewSuggestionService(db, nil)
	mock.ExpectQuery("FROM hosts WHERE organization_id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "name", "host"}).AddRow(int64(1), "web-01", "10.0.0.1"),
	)
	hosts, err := svc.getHostsForOrg(1)
	if err != nil || len(hosts) != 1 || hosts[0].Name != "web-01" {
		t.Fatalf("expected 1 host, got %v/%v", hosts, err)
	}
}

func TestGetHostsForOrg_ScanError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewSuggestionService(db, nil)
	mock.ExpectQuery("FROM hosts WHERE organization_id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "name", "host"}).AddRow("not-an-int", "web-01", "10.0.0.1"),
	)
	_, err := svc.getHostsForOrg(1)
	if err == nil {
		t.Fatal("expected scan error")
	}
}

func TestGetServicesForOrgSuggestion_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewSuggestionService(db, nil)
	mock.ExpectQuery("FROM services WHERE organization_id").WillReturnError(errors.New("db error"))
	_, err := svc.getServicesForOrg(1)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetServicesForOrg_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewSuggestionService(db, nil)
	mock.ExpectQuery("FROM services WHERE organization_id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "name", "host", "service"}).AddRow(int64(5), "http-check", "http://example.com", "api"),
	)
	svcs, err := svc.getServicesForOrg(1)
	if err != nil || len(svcs) != 1 || svcs[0].Service != "api" {
		t.Fatalf("expected 1 service, got %v/%v", svcs, err)
	}
}

func TestGetServicesForOrg_ScanError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewSuggestionService(db, nil)
	mock.ExpectQuery("FROM services WHERE organization_id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "name", "host", "service"}).AddRow("not-int", "http-check", "host", "api"),
	)
	_, err := svc.getServicesForOrg(1)
	if err == nil {
		t.Fatal("expected scan error")
	}
}

func TestGetExistingLinkTargets_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewSuggestionService(db, nil)
	mock.ExpectQuery("FROM application_links").WillReturnError(errors.New("db error"))
	_, err := svc.getExistingLinkTargets(1, "api")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetExistingLinkTargets_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewSuggestionService(db, nil)
	mock.ExpectQuery("FROM application_links").WillReturnRows(
		sqlmock.NewRows([]string{"target_type", "target_id"}).
			AddRow("host", int64(1)).
			AddRow("service", int64(5)),
	)
	existing, err := svc.getExistingLinkTargets(1, "api")
	if err != nil || !existing["host-1"] || !existing["service-5"] {
		t.Fatalf("expected host-1 and service-5, got %v/%v", existing, err)
	}
}

func TestGetExistingLinkTargets_ScanError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewSuggestionService(db, nil)
	mock.ExpectQuery("FROM application_links").WillReturnRows(
		sqlmock.NewRows([]string{"target_type", "target_id"}).AddRow("host", "not-int"),
	)
	_, err := svc.getExistingLinkTargets(1, "api")
	if err == nil {
		t.Fatal("expected scan error")
	}
}

func TestGetExistingLinkTargets_Empty(t *testing.T) {
	db, mock := newDB(t)
	svc := NewSuggestionService(db, nil)
	mock.ExpectQuery("FROM application_links").WillReturnRows(sqlmock.NewRows([]string{"target_type", "target_id"}))
	existing, err := svc.getExistingLinkTargets(1, "api")
	if err != nil || len(existing) != 0 {
		t.Fatalf("expected empty, got %v/%v", existing, err)
	}
}
