package services

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRuleMatchesTarget_AnyType_AlwaysMatches(t *testing.T) {
	r := routingRule{targetType: "any"}
	if !ruleMatchesTarget(r, nil, nil) {
		t.Fatal("rule with type=any must match nil targets")
	}
	svcID := int64(1)
	if !ruleMatchesTarget(r, &svcID, nil) {
		t.Fatal("rule with type=any must match specific service")
	}
}

func TestRuleMatchesTarget_EmptyType_AlwaysMatches(t *testing.T) {
	r := routingRule{targetType: ""}
	if !ruleMatchesTarget(r, nil, nil) {
		t.Fatal("rule with empty type must match")
	}
}

func TestRuleMatchesTarget_NilTargetID_AlwaysMatches(t *testing.T) {
	r := routingRule{targetType: "service", targetID: nil}
	svcID := int64(1)
	if !ruleMatchesTarget(r, &svcID, nil) {
		t.Fatal("rule with nil targetID must match any service")
	}
}

func TestRuleMatchesTarget_ServiceMatch(t *testing.T) {
	targetID := int64(5)
	r := routingRule{targetType: "service", targetID: &targetID}
	svcID := int64(5)
	if !ruleMatchesTarget(r, &svcID, nil) {
		t.Fatal("rule must match matching service ID")
	}
}

func TestRuleMatchesTarget_ServiceMismatch(t *testing.T) {
	targetID := int64(5)
	r := routingRule{targetType: "service", targetID: &targetID}
	svcID := int64(9)
	if ruleMatchesTarget(r, &svcID, nil) {
		t.Fatal("rule must not match different service ID")
	}
}

func TestRuleMatchesTarget_HostMatch(t *testing.T) {
	targetID := int64(3)
	r := routingRule{targetType: "host", targetID: &targetID}
	hostID := int64(3)
	if !ruleMatchesTarget(r, nil, &hostID) {
		t.Fatal("rule must match matching host ID")
	}
}

func TestRuleMatchesTarget_HostMismatch(t *testing.T) {
	targetID := int64(3)
	r := routingRule{targetType: "host", targetID: &targetID}
	hostID := int64(7)
	if ruleMatchesTarget(r, nil, &hostID) {
		t.Fatal("rule must not match different host ID")
	}
}

func TestRuleMatchesTarget_ServiceRule_NilServiceID(t *testing.T) {
	targetID := int64(5)
	r := routingRule{targetType: "service", targetID: &targetID}
	if ruleMatchesTarget(r, nil, nil) {
		t.Fatal("rule must not match when serviceID is nil and rule targets specific service")
	}
}

func TestRuleMatchesTarget_HostRule_NilHostID(t *testing.T) {
	targetID := int64(3)
	r := routingRule{targetType: "host", targetID: &targetID}
	if ruleMatchesTarget(r, nil, nil) {
		t.Fatal("rule must not match when hostID is nil and rule targets specific host")
	}
}

func TestSplitTags_Empty(t *testing.T) {
	if got := splitTags(""); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}

func TestSplitTags_Single(t *testing.T) {
	got := splitTags("env:prod")
	if len(got) != 1 || got[0] != "env:prod" {
		t.Fatalf("want [env:prod], got %v", got)
	}
}

func TestSplitTags_Multiple(t *testing.T) {
	got := splitTags("env:prod, team:infra,region:eu")
	if len(got) != 3 {
		t.Fatalf("want 3 tags, got %v", got)
	}
}

func TestSplitTags_EmptySegmentsDropped(t *testing.T) {
	got := splitTags("a,,b")
	for _, t2 := range got {
		if t2 == "" {
			t.Fatal("empty tag should be dropped")
		}
	}
}

func TestRoutingRulesForSeverity_Critical_Success(t *testing.T) {
	db, mock := newDB(t)
	chJSON, _ := json.Marshal([]int64{42, 43})
	mock.ExpectQuery("alert_rules").
		WillReturnRows(sqlmock.NewRows(routingCols).
			AddRow(chJSON, "env:prod", "any", nil))
	rules := routingRulesForSeverity(db, 1, "critical")
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if len(rules[0].channels) != 2 {
		t.Fatalf("expected 2 channels, got %v", rules[0].channels)
	}
}

func TestRoutingRulesForSeverity_Warning_ColSwitch(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectQuery("alert_rules").
		WillReturnRows(sqlmock.NewRows(routingCols).
			AddRow([]byte("[]"), "", "any", nil))
	rules := routingRulesForSeverity(db, 1, "warning")
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule (warning col), got %d", len(rules))
	}
}

func TestRoutingRulesForSeverity_DBError_ReturnsNil(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectQuery("alert_rules").
		WillReturnError(errors.New("connection lost"))
	rules := routingRulesForSeverity(db, 1, "critical")
	if rules != nil {
		t.Fatalf("expected nil on DB error, got %v", rules)
	}
}

func TestRoutingRulesForSeverity_ScanError_SkipsRow(t *testing.T) {
	db, mock := newDB(t)
	// Return a row where target_id cannot be scanned into sql.NullInt64.
	mock.ExpectQuery("alert_rules").
		WillReturnRows(sqlmock.NewRows(routingCols).
			AddRow([]byte("[1]"), "tag", "any", "not-an-int"))
	// Scan error is silently skipped (continue); result is empty.
	rules := routingRulesForSeverity(db, 1, "critical")
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules after scan error, got %d", len(rules))
	}
}

func TestRoutingRulesForSeverity_WithTargetID(t *testing.T) {
	db, mock := newDB(t)
	chJSON, _ := json.Marshal([]int64{10})
	mock.ExpectQuery("alert_rules").
		WillReturnRows(sqlmock.NewRows(routingCols).
			AddRow(chJSON, "", "service", int64(55)))
	rules := routingRulesForSeverity(db, 1, "critical")
	if len(rules) != 1 || rules[0].targetID == nil || *rules[0].targetID != 55 {
		t.Fatalf("expected rule with targetID=55, got %v", rules)
	}
}

func TestRouteAlert_NoRules_FallbackEmptyChannels(t *testing.T) {
	db, mock := newDB(t)
	// routingRulesForSeverity → empty
	mock.ExpectQuery("alert_rules").WillReturnRows(sqlmock.NewRows(routingCols))
	// GetChannels fallback → empty
	mock.ExpectQuery("notification_channels").WillReturnRows(sqlmock.NewRows(channelColumns))
	RouteAlert(db, 1, "critical", "Down", "Service is down", "alias-1")
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRouteAlert_NoRules_FallbackDisabledChannel(t *testing.T) {
	db, mock := newDB(t)
	now := time.Now()
	configJSON, _ := json.Marshal(map[string]interface{}{"webhook_url": "http://hook.example.com"})
	// routingRulesForSeverity → empty
	mock.ExpectQuery("alert_rules").WillReturnRows(sqlmock.NewRows(routingCols))
	// GetChannels fallback → 1 disabled channel (skipped by ch.Enabled check)
	mock.ExpectQuery("notification_channels").
		WillReturnRows(sqlmock.NewRows(channelColumns).
			AddRow(int64(1), int64(1), "webhook", "webhook", configJSON, false, now, now))
	RouteAlert(db, 1, "critical", "Down", "Service is down", "alias-2")
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRouteAlertTargeted_WithRules_EmptyRuleChannels(t *testing.T) {
	db, mock := newDB(t)
	// Rule with no specific channels → resolveChannels calls GetChannels
	mock.ExpectQuery("alert_rules").
		WillReturnRows(sqlmock.NewRows(routingCols).
			AddRow([]byte("[]"), "", "any", nil))
	mock.ExpectQuery("notification_channels").WillReturnRows(sqlmock.NewRows(channelColumns))
	RouteAlertTargeted(db, 1, "critical", "Down", "body", "alias-3", nil, nil)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRouteAlertTargeted_WithRules_SpecificChannels_EnabledWebhook(t *testing.T) {
	db, mock := newDB(t)
	now := time.Now()
	configJSON, _ := json.Marshal(map[string]interface{}{"webhook_url": "http://test-webhook.example.com"})
	chJSON, _ := json.Marshal([]int64{42})
	// routingRulesForSeverity → 1 rule with channel [42]
	mock.ExpectQuery("alert_rules").
		WillReturnRows(sqlmock.NewRows(routingCols).
			AddRow(chJSON, "env:prod", "any", nil))
	// GetChannelsByIDs → 1 enabled webhook channel
	mock.ExpectQuery("notification_channels").
		WillReturnRows(sqlmock.NewRows(channelColumns).
			AddRow(int64(42), int64(1), "wh", "webhook", configJSON, true, now, now))

	// Mock HTTP so sendWebhook does not dial the network.
	orig := http.DefaultTransport
	http.DefaultTransport = &mockRoundTripper{statusCode: 200, body: ""}
	defer func() { http.DefaultTransport = orig }()

	RouteAlertTargeted(db, 1, "critical", "Down", "body", "alias-4", nil, nil)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRouteAlertTargeted_WithRules_AlreadySent_Deduplicated(t *testing.T) {
	db, mock := newDB(t)
	now := time.Now()
	configJSON, _ := json.Marshal(map[string]interface{}{"webhook_url": "http://test-webhook.example.com"})
	chJSON, _ := json.Marshal([]int64{42})
	// Two rules pointing at the same channel → only one send (dedup via sent map).
	mock.ExpectQuery("alert_rules").
		WillReturnRows(sqlmock.NewRows(routingCols).
			AddRow(chJSON, "a", "any", nil).
			AddRow(chJSON, "b", "any", nil))
	// resolveChannels for rule 1 (len>0) → GetChannelsByIDs
	mock.ExpectQuery("notification_channels").
		WillReturnRows(sqlmock.NewRows(channelColumns).
			AddRow(int64(42), int64(1), "wh", "webhook", configJSON, true, now, now))
	// resolveChannels for rule 2 (len>0) → GetChannelsByIDs again
	mock.ExpectQuery("notification_channels").
		WillReturnRows(sqlmock.NewRows(channelColumns).
			AddRow(int64(42), int64(1), "wh", "webhook", configJSON, true, now, now))

	orig := http.DefaultTransport
	http.DefaultTransport = &mockRoundTripper{statusCode: 200, body: ""}
	defer func() { http.DefaultTransport = orig }()

	RouteAlertTargeted(db, 1, "critical", "Down", "body", "alias-5", nil, nil)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRouteAlertTargeted_TargetFilter_NoMatch(t *testing.T) {
	db, mock := newDB(t)
	now := time.Now()
	configJSON, _ := json.Marshal(map[string]interface{}{"webhook_url": "http://test-webhook.example.com"})
	chJSON, _ := json.Marshal([]int64{42})
	svcTarget := int64(99)
	requestedSvc := int64(1) // does not match rule's target 99
	// Rule targets service 99; request is for service 1 → rule filtered out → fallback
	mock.ExpectQuery("alert_rules").
		WillReturnRows(sqlmock.NewRows(routingCols).
			AddRow(chJSON, "", "service", svcTarget))
	// Fallback to GetChannels (no matched rules)
	mock.ExpectQuery("notification_channels").
		WillReturnRows(sqlmock.NewRows(channelColumns).
			AddRow(int64(42), int64(1), "wh", "webhook", configJSON, true, now, now))

	orig := http.DefaultTransport
	http.DefaultTransport = &mockRoundTripper{statusCode: 200, body: ""}
	defer func() { http.DefaultTransport = orig }()

	RouteAlertTargeted(db, 1, "critical", "Down", "body", "alias-6", &requestedSvc, nil)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRouteResolve_NoRules_FallbackEmpty(t *testing.T) {
	db, mock := newDB(t)
	// warning rules → empty
	mock.ExpectQuery("alert_rules").WillReturnRows(sqlmock.NewRows(routingCols))
	// critical rules → empty
	mock.ExpectQuery("alert_rules").WillReturnRows(sqlmock.NewRows(routingCols))
	// GetChannels fallback → empty
	mock.ExpectQuery("notification_channels").WillReturnRows(sqlmock.NewRows(channelColumns))
	RouteResolve(db, 1, "alias-7", "Resolved", "Service recovered")
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRouteResolveTargeted_MatchedRules_WebhookChannel(t *testing.T) {
	db, mock := newDB(t)
	now := time.Now()
	configJSON, _ := json.Marshal(map[string]interface{}{"webhook_url": "http://test-webhook.example.com"})
	chJSON, _ := json.Marshal([]int64{43})
	// warning → 1 rule
	mock.ExpectQuery("alert_rules").
		WillReturnRows(sqlmock.NewRows(routingCols).
			AddRow(chJSON, "", "any", nil))
	// critical → empty
	mock.ExpectQuery("alert_rules").WillReturnRows(sqlmock.NewRows(routingCols))
	// resolveChannels for the warning rule → GetChannelsByIDs
	mock.ExpectQuery("notification_channels").
		WillReturnRows(sqlmock.NewRows(channelColumns).
			AddRow(int64(43), int64(1), "wh", "webhook", configJSON, true, now, now))

	orig := http.DefaultTransport
	http.DefaultTransport = &mockRoundTripper{statusCode: 200, body: ""}
	defer func() { http.DefaultTransport = orig }()

	RouteResolveTargeted(db, 1, "alias-8", "Resolved", "body", nil, nil)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRouteResolveTargeted_MatchedRules_DisabledChannel(t *testing.T) {
	db, mock := newDB(t)
	now := time.Now()
	configJSON, _ := json.Marshal(map[string]interface{}{"webhook_url": "http://test-webhook.example.com"})
	chJSON, _ := json.Marshal([]int64{44})
	mock.ExpectQuery("alert_rules").
		WillReturnRows(sqlmock.NewRows(routingCols).
			AddRow(chJSON, "", "any", nil))
	mock.ExpectQuery("alert_rules").WillReturnRows(sqlmock.NewRows(routingCols))
	// GetChannelsByIDs returns a disabled channel (ch.Enabled == false → skipped)
	// Note: GetChannelsByIDs queries enabled=true, so disabled channels are never returned.
	// To reach the ch.Enabled check in RouteResolveTargeted, we return empty here.
	mock.ExpectQuery("notification_channels").
		WillReturnRows(sqlmock.NewRows(channelColumns).
			AddRow(int64(44), int64(1), "wh", "webhook", configJSON, false, now, now))
	RouteResolveTargeted(db, 1, "alias-9", "Resolved", "body", nil, nil)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
