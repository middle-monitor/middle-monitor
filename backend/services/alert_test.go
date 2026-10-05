package services

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"middle-monitor/backend/models"
)

func TestNewAlertService(t *testing.T) {
	db, _ := newDB(t)
	if NewAlertService(db) == nil {
		t.Fatal("expected non-nil")
	}
}

func TestCreateChannel_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	now := time.Now()
	mock.ExpectQuery("INSERT INTO notification_channels").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(1, now, now))
	ch, err := svc.CreateChannel(models.NotificationChannel{
		OrganizationID: 1, Name: "slack", Type: "slack", Enabled: true,
		Config: map[string]interface{}{"webhook_url": "https://hooks.slack.com"},
	})
	if err != nil || ch.ID != 1 {
		t.Fatalf("expected created channel: %v/%v", ch, err)
	}
}

func TestCreateChannel_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("INSERT INTO notification_channels").WillReturnError(sql.ErrConnDone)
	_, err := svc.CreateChannel(models.NotificationChannel{})
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestGetChannels_Empty(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows(channelColumns))
	channels, err := svc.GetChannels(1)
	if err != nil || len(channels) != 0 {
		t.Fatalf("expected empty: %v/%v", channels, err)
	}
}

func TestGetChannels_WithResult(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	now := time.Now()
	configJSON := []byte(`{"webhook_url":"https://hooks.slack.com"}`)
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows(channelColumns).
		AddRow(1, 1, "slack", "slack", configJSON, true, now, now))
	channels, err := svc.GetChannels(1)
	if err != nil || len(channels) != 1 {
		t.Fatalf("expected 1 channel: %v/%v", channels, err)
	}
}

func TestGetChannels_NullConfig(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	now := time.Now()
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows(channelColumns).
		AddRow(1, 1, "email", "email", nil, true, now, now))
	channels, err := svc.GetChannels(1)
	if err != nil || len(channels) != 1 {
		t.Fatalf("expected 1 channel with nil config: %v/%v", channels, err)
	}
	if channels[0].Config == nil {
		t.Fatal("expected empty map for nil config")
	}
}

func TestGetChannels_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("SELECT id").WillReturnError(sql.ErrConnDone)
	_, err := svc.GetChannels(1)
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestUpdateChannel_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	now := time.Now()
	mock.ExpectQuery("UPDATE notification_channels").
		WillReturnRows(sqlmock.NewRows(channelColumns).
			AddRow(1, 1, "webhook", "webhook", []byte(`{}`), true, now, now))
	ch, err := svc.UpdateChannel(1, 1, models.NotificationChannel{Name: "webhook", Type: "webhook", Enabled: true})
	if err != nil || ch.ID != 1 {
		t.Fatalf("expected updated channel: %v/%v", ch, err)
	}
}

func TestUpdateChannel_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("UPDATE notification_channels").WillReturnError(sql.ErrConnDone)
	_, err := svc.UpdateChannel(1, 1, models.NotificationChannel{})
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestDeleteChannel_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectExec("DELETE FROM notification_channels").WillReturnResult(sqlmock.NewResult(1, 1))
	if err := svc.DeleteChannel(1, 1); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestDeleteChannel_NotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectExec("DELETE FROM notification_channels").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := svc.DeleteChannel(1, 1); err == nil {
		t.Fatal("expected not found error")
	}
}

func TestDeleteChannel_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectExec("DELETE FROM notification_channels").WillReturnError(sql.ErrConnDone)
	if err := svc.DeleteChannel(1, 1); err == nil {
		t.Fatal("expected DB error")
	}
}

func TestGetAlertRules_Empty(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows(alertRuleColumns))
	rules, err := svc.GetAlertRules(1)
	if err != nil || len(rules) != 0 {
		t.Fatalf("expected empty: %v/%v", rules, err)
	}
}

func TestGetAlertRules_WithResult(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	now := time.Now()
	chJSON, _ := json.Marshal([]int64{1, 2})
	w, c, r := 80.0, 90.0, 70.0
	desc := "test rule"
	tgtID := int64(5)
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows(alertRuleColumns).AddRow(
		1, 1, "cpu alert", &desc, "threshold", "service", &tgtID,
		"cpu", "gt", 75.0, 60, "critical", true, chJSON,
		"avg", &w, &c, &r, "env:prod", true, true, now, now, nil, []byte("[]"),
	))
	rules, err := svc.GetAlertRules(1)
	if err != nil || len(rules) != 1 {
		t.Fatalf("expected 1 rule: %v/%v", rules, err)
	}
	if rules[0].WarningThreshold == nil || *rules[0].WarningThreshold != 80.0 {
		t.Fatal("expected warning threshold")
	}
}

func TestGetAlertRules_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("SELECT id").WillReturnError(sql.ErrConnDone)
	_, err := svc.GetAlertRules(1)
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestCreateAlertRule_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	now := time.Now()
	mock.ExpectQuery("INSERT INTO alert_rules").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(1, now, now))
	rule, err := svc.CreateAlertRule(models.AlertRule{OrganizationID: 1, Name: "test"})
	if err != nil || rule.ID != 1 {
		t.Fatalf("expected created rule: %v/%v", rule, err)
	}
	// Default metric and type should be "routing"
	if rule.Metric != "routing" || rule.Type != "routing" {
		t.Fatalf("expected metric=routing/type=routing, got %q/%q", rule.Metric, rule.Type)
	}
}

func TestCreateAlertRule_WithMetric(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	now := time.Now()
	mock.ExpectQuery("INSERT INTO alert_rules").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(2, now, now))
	rule, err := svc.CreateAlertRule(models.AlertRule{
		OrganizationID: 1, Name: "cpu", Metric: "cpu", Type: "threshold", Aggregation: "max",
	})
	if err != nil || rule.ID != 2 {
		t.Fatalf("expected created rule: %v/%v", rule, err)
	}
}

func TestCreateAlertRule_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("INSERT INTO alert_rules").WillReturnError(sql.ErrConnDone)
	_, err := svc.CreateAlertRule(models.AlertRule{})
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestUpdateAlertRule_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	now := time.Now()
	chJSON, _ := json.Marshal([]int64{1})
	w, c, r := 80.0, 90.0, 70.0
	desc := "upd"
	tgtID := int64(5)
	mock.ExpectQuery("UPDATE alert_rules").
		WillReturnRows(sqlmock.NewRows(alertRuleColumns).AddRow(
			1, 1, "updated", &desc, "threshold", "service", &tgtID,
			"cpu", "gt", 75.0, 60, "critical", true, chJSON,
			"avg", &w, &c, &r, "env:prod", true, true, now, now, nil, []byte("[]"),
		))
	rule, err := svc.UpdateAlertRule(1, 1, models.AlertRule{Name: "updated"})
	if err != nil || rule.Name != "updated" {
		t.Fatalf("expected updated rule: %v/%v", rule, err)
	}
}

func TestUpdateAlertRule_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("UPDATE alert_rules").WillReturnError(sql.ErrConnDone)
	_, err := svc.UpdateAlertRule(1, 1, models.AlertRule{})
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestDeleteAlertRule_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectExec("DELETE FROM alert_rules").WillReturnResult(sqlmock.NewResult(1, 1))
	if err := svc.DeleteAlertRule(1, 1); err != nil {
		t.Fatalf("expected nil: %v", err)
	}
}

func TestDeleteAlertRule_NotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectExec("DELETE FROM alert_rules").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := svc.DeleteAlertRule(1, 1); err == nil {
		t.Fatal("expected not found error")
	}
}

func TestDeleteAlertRule_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectExec("DELETE FROM alert_rules").WillReturnError(sql.ErrConnDone)
	if err := svc.DeleteAlertRule(1, 1); err == nil {
		t.Fatal("expected DB error")
	}
}

func TestToggleAlertRule_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectExec("UPDATE alert_rules").WillReturnResult(sqlmock.NewResult(1, 1))
	if err := svc.ToggleAlertRule(1, 1, false); err != nil {
		t.Fatalf("expected nil: %v", err)
	}
}

func TestToggleAlertRule_NotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectExec("UPDATE alert_rules").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := svc.ToggleAlertRule(1, 1, true); err == nil {
		t.Fatal("expected not found error")
	}
}

func TestToggleAlertRule_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectExec("UPDATE alert_rules").WillReturnError(sql.ErrConnDone)
	if err := svc.ToggleAlertRule(1, 1, true); err == nil {
		t.Fatal("expected DB error")
	}
}

func TestGetEnabledAlertRules_Empty(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows(enabledRuleColumns))
	rules, err := svc.GetEnabledAlertRules()
	if err != nil || len(rules) != 0 {
		t.Fatalf("expected empty: %v/%v", rules, err)
	}
}

func TestGetEnabledAlertRules_WithResult(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	w, c, r := 80.0, 90.0, 70.0
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows(enabledRuleColumns).AddRow(
		1, 1, "cpu", nil, "threshold", nil, nil,
		"cpu", "gt", 75.0, 60, "critical", true, []byte(`[1]`),
		"avg", &w, &c, &r, nil, []byte("[]"),
	))
	rules, err := svc.GetEnabledAlertRules()
	if err != nil || len(rules) != 1 {
		t.Fatalf("expected 1 rule: %v/%v", rules, err)
	}
}

func TestGetEnabledAlertRules_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("SELECT id").WillReturnError(sql.ErrConnDone)
	_, err := svc.GetEnabledAlertRules()
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestHasLiveIncidentForRule_True(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	has, err := svc.HasLiveIncidentForRule(1)
	if err != nil || !has {
		t.Fatalf("expected true: %v/%v", has, err)
	}
}

func TestHasLiveIncidentForRule_False(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	has, err := svc.HasLiveIncidentForRule(1)
	if err != nil || has {
		t.Fatalf("expected false: %v/%v", has, err)
	}
}

func TestHasLiveIncidentForServiceCheck_True(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	has, err := svc.HasLiveIncidentForServiceCheck(1)
	if err != nil || !has {
		t.Fatalf("expected true: %v/%v", has, err)
	}
}

func TestGetChannelsByIDs_Empty(t *testing.T) {
	db, _ := newDB(t)
	svc := NewAlertService(db)
	channels, err := svc.GetChannelsByIDs(nil, 1)
	if err != nil || channels != nil {
		t.Fatalf("expected nil for empty IDs: %v/%v", channels, err)
	}
}

func TestGetChannelsByIDs_WithIDs(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	now := time.Now()
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows(channelColumns).
		AddRow(1, 1, "slack", "slack", []byte(`{}`), true, now, now))
	channels, err := svc.GetChannelsByIDs([]int64{1, 2}, 1)
	if err != nil || len(channels) != 1 {
		t.Fatalf("expected 1 channel: %v/%v", channels, err)
	}
}

func TestGetChannelsByIDs_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("SELECT id").WillReturnError(sql.ErrConnDone)
	_, err := svc.GetChannelsByIDs([]int64{1}, 1)
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestCreateIncident_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	now := time.Now()
	mock.ExpectQuery("INSERT INTO incidents").
		WillReturnRows(sqlmock.NewRows([]string{"id", "started_at"}).AddRow(1, now))
	inc, err := svc.CreateIncident(models.Incident{OrganizationID: 1, Title: "High CPU", Severity: "critical"})
	if err != nil || inc.ID != 1 || inc.Status != "open" {
		t.Fatalf("expected created incident: %v/%v", inc, err)
	}
}

func TestCreateIncident_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("INSERT INTO incidents").WillReturnError(sql.ErrConnDone)
	_, err := svc.CreateIncident(models.Incident{})
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestGetIncidents_WithStatus(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	now := time.Now()
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows(incidentColumns).
		AddRow(1, 1, nil, nil, nil, "High CPU", nil, "critical", "open", nil, now, nil, nil, nil, nil))
	incidents, err := svc.GetIncidents(1, IncidentFilter{Status: "open"})
	if err != nil || len(incidents) != 1 {
		t.Fatalf("expected 1 incident: %v/%v", incidents, err)
	}
}

func TestGetIncidents_NoStatus(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows(incidentColumns))
	incidents, err := svc.GetIncidents(1, IncidentFilter{})
	if err != nil || len(incidents) != 0 {
		t.Fatalf("expected empty: %v/%v", incidents, err)
	}
}

func TestGetIncidents_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("SELECT id").WillReturnError(sql.ErrConnDone)
	_, err := svc.GetIncidents(1, IncidentFilter{})
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestGetIncidents_WithFilters(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	now := time.Now()
	// severity + service + search all narrow the WHERE clause; the query still
	// starts with "SELECT id" so the regex match holds, and LIMIT/OFFSET are added.
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows(incidentColumns).
		AddRow(1, 1, nil, nil, nil, "Timeout on api", nil, "critical", "open", "api", now, nil, nil, nil, nil))
	incidents, err := svc.GetIncidents(1, IncidentFilter{Severity: "critical", Service: "api", Search: "timeout", Limit: 25, Offset: 50})
	if err != nil || len(incidents) != 1 {
		t.Fatalf("expected 1 incident: %v/%v", incidents, err)
	}
}

func TestCountIncidents(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(42))
	total, err := svc.CountIncidents(1, IncidentFilter{Status: "open"})
	if err != nil || total != 42 {
		t.Fatalf("expected 42, got %d/%v", total, err)
	}
}

func TestCountIncidents_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("SELECT COUNT").WillReturnError(sql.ErrConnDone)
	if _, err := svc.CountIncidents(1, IncidentFilter{}); err == nil {
		t.Fatal("expected DB error")
	}
}

func TestIncidentStats(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectQuery("FROM incidents WHERE organization_id").WillReturnRows(
		sqlmock.NewRows([]string{"total", "open", "acknowledged", "resolved", "critical"}).AddRow(10, 3, 2, 5, 4))
	mock.ExpectQuery("SELECT DISTINCT service").WillReturnRows(
		sqlmock.NewRows([]string{"service"}).AddRow("api").AddRow("web"))
	stats, err := svc.IncidentStats(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.Total != 10 || stats.Open != 3 || stats.Acknowledged != 2 || stats.Resolved != 5 || stats.Critical != 4 {
		t.Fatalf("unexpected counts: %+v", stats)
	}
	if len(stats.Services) != 2 || stats.Services[0] != "api" || stats.Services[1] != "web" {
		t.Fatalf("unexpected services: %v", stats.Services)
	}
}

func TestUpdateIncidentStatus_Acknowledged(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	userID := int64(1)
	mock.ExpectExec("UPDATE incidents").WillReturnResult(sqlmock.NewResult(1, 1))
	if err := svc.UpdateIncidentStatus(1, 1, "acknowledged", &userID, nil); err != nil {
		t.Fatalf("expected nil: %v", err)
	}
}

func TestUpdateIncidentStatus_Resolved(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectExec("UPDATE incidents").WillReturnResult(sqlmock.NewResult(1, 1))
	if err := svc.UpdateIncidentStatus(1, 1, "resolved", nil, nil); err != nil {
		t.Fatalf("expected nil: %v", err)
	}
}

func TestUpdateIncidentStatus_Default(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectExec("UPDATE incidents").WillReturnResult(sqlmock.NewResult(1, 1))
	if err := svc.UpdateIncidentStatus(1, 1, "closed", nil, nil); err != nil {
		t.Fatalf("expected nil for default status: %v", err)
	}
}

func TestUpdateIncidentStatus_NotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectExec("UPDATE incidents").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := svc.UpdateIncidentStatus(1, 1, "resolved", nil, nil); err == nil {
		t.Fatal("expected not found error")
	}
}

func TestUpdateIncidentStatus_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAlertService(db)
	mock.ExpectExec("UPDATE incidents").WillReturnError(sql.ErrConnDone)
	if err := svc.UpdateIncidentStatus(1, 1, "resolved", nil, nil); err == nil {
		t.Fatal("expected DB error")
	}
}
