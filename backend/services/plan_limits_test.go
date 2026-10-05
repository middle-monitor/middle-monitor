package services

import (
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestNewPlanLimitsService(t *testing.T) {
	db, _ := newDB(t)
	svc := NewPlanLimitsService(db)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
}

// A running trial gates a free org as pro without touching its billing plan; an
// expired one falls back on its own, so no job has to downgrade anybody.
func TestEffectivePlan_TrialWindow(t *testing.T) {
	running := time.Now().Add(24 * time.Hour)
	expired := time.Now().Add(-1 * time.Minute)

	if got := EffectivePlan("free", &running); got != "pro" {
		t.Fatalf("running trial: want pro, got %q", got)
	}
	if got := EffectivePlan("free", &expired); got != "free" {
		t.Fatalf("expired trial: want free, got %q", got)
	}
	if got := EffectivePlan("free", nil); got != "free" {
		t.Fatalf("no trial: want free, got %q", got)
	}
	// A leftover deadline must never upgrade a paid plan or downgrade a custom one.
	if got := EffectivePlan("custom", &running); got != "custom" {
		t.Fatalf("custom plan: want custom, got %q", got)
	}
}

// A self-hosted instance has no way to buy a plan, so the free caps must never
// apply there: every org is custom, unlimited until an operator sets overrides.
func TestEffectivePlan_SelfHostedIsCustom(t *testing.T) {
	t.Setenv("BILLING_ENABLED", "")
	expired := time.Now().Add(-1 * time.Minute)
	for _, plan := range []string{"free", "pro", "custom"} {
		if got := EffectivePlan(plan, &expired); got != "custom" {
			t.Fatalf("%s without billing: want custom, got %q", plan, got)
		}
	}
}

func TestCanCreateHost_SelfHostedIsUnlimited(t *testing.T) {
	t.Setenv("BILLING_ENABLED", "")
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	mock.ExpectQuery("SELECT custom_max_hosts").WillReturnRows(sqlmock.NewRows([]string{"h", "s", "e"}).AddRow(nil, nil, nil))
	if err := svc.CanCreateHost(1); err != nil {
		t.Fatalf("self-hosted org must not hit the free host cap, got %v", err)
	}
}

// Retention is what the pricing page sells: free keeps 7 days, pro 30, and a
// custom plan keeps exactly the tier bought on Stripe.
func TestRetentionDays_PerPlan(t *testing.T) {
	cases := []struct {
		plan       string
		customDays int
		want       int
	}{
		{"free", 0, FreeRetentionDays},
		{"pro", 0, ProRetentionDays},
		{"custom", 90, 90},
		{"custom", 0, ProRetentionDays},
		{"enterprise", 0, -1},
	}
	for _, c := range cases {
		if got := RetentionDays(c.plan, c.customDays); got != c.want {
			t.Fatalf("plan %q custom %d: want %d, got %d", c.plan, c.customDays, c.want, got)
		}
	}
}

func TestGetPlan_ReturnsValue(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(
		sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil),
	)
	plan, err := svc.GetPlan(1)
	if err != nil || plan != "pro" {
		t.Fatalf("want pro, got %q / %v", plan, err)
	}
}

func TestGetPlan_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnError(sql.ErrConnDone)
	_, err := svc.GetPlan(1)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIsFree_FreePlan(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	free, err := svc.IsFree(1)
	if err != nil || !free {
		t.Fatalf("want true, got %v / %v", free, err)
	}
}

func TestIsFree_ProPlan(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	free, err := svc.IsFree(1)
	if err != nil || free {
		t.Fatalf("want false, got %v / %v", free, err)
	}
}

func TestIsFree_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnError(sql.ErrConnDone)
	_, err := svc.IsFree(1)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCanCreateHost_ProPlan_UnderLimit(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(ProMaxHosts - 1))
	if err := svc.CanCreateHost(1); err != nil {
		t.Fatalf("expected nil under pro limit, got %v", err)
	}
}

// Pro is no longer unlimited: it caps hosts at ProMaxHosts, enforced like free.
func TestCanCreateHost_ProPlan_AtLimit(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(ProMaxHosts))
	if err := svc.CanCreateHost(1); err == nil {
		t.Fatal("expected limit error at pro host cap")
	}
}

func TestCanCreateHost_FreePlan_UnderLimit(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	if err := svc.CanCreateHost(1); err != nil {
		t.Fatalf("expected nil under limit, got %v", err)
	}
}

func TestCanCreateHost_FreePlan_AtLimit(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(FreeMaxHosts))
	err := svc.CanCreateHost(1)
	if err == nil {
		t.Fatal("expected limit error")
	}
}

func TestCanCreateHost_FreePlan_CountDBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnError(sql.ErrConnDone)
	if err := svc.CanCreateHost(1); err == nil {
		t.Fatal("expected DB error")
	}
}

func TestCanCreateService_ProPlan_UnderLimit(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(ProMaxErrorServices - 1))
	if err := svc.CanCreateService(1, "error_service_go", nil); err != nil {
		t.Fatalf("expected nil under pro limit, got %v", err)
	}
}

// Pro is no longer unlimited: it caps SDK services at ProMaxErrorServices.
func TestCanCreateService_ProPlan_AtLimit(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("pro", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(ProMaxErrorServices))
	if err := svc.CanCreateService(1, "error_service_go", nil); err == nil {
		t.Fatal("expected limit error at pro SDK cap")
	}
}

func TestCanCreateService_FreePlan_ErrorService_UnderLimit(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	if err := svc.CanCreateService(1, "error_service_go", nil); err != nil {
		t.Fatalf("expected nil under limit, got %v", err)
	}
}

func TestCanCreateService_FreePlan_ErrorService_AtLimit(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(FreeMaxErrorServices))
	err := svc.CanCreateService(1, "error_service_go", nil)
	if err == nil {
		t.Fatal("expected limit error")
	}
}

func TestCanCreateService_FreePlan_ErrorService_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnError(sql.ErrConnDone)
	err := svc.CanCreateService(1, "error_service_go", nil)
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestCanCreateService_FreePlan_CheckService_UnderLimit(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	hostID := int64(10)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	if err := svc.CanCreateService(1, "http", &hostID); err != nil {
		t.Fatalf("expected nil under limit, got %v", err)
	}
}

func TestCanCreateService_FreePlan_CheckService_AtLimit(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	hostID := int64(10)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(FreeMaxMonitoredServices))
	err := svc.CanCreateService(1, "http", &hostID)
	if err == nil {
		t.Fatal("expected limit error")
	}
}

func TestCanCreateService_FreePlan_CheckService_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	hostID := int64(10)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnError(sql.ErrConnDone)
	err := svc.CanCreateService(1, "http", &hostID)
	if err == nil {
		t.Fatal("expected DB error")
	}
}

// Monitored services are now capped org-wide, so the cap applies regardless of
// whether a host is passed (nil/zero hostID still counts and enforces the limit).
func TestCanCreateService_FreePlan_Check_CappedWithoutHost(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(FreeMaxMonitoredServices))
	if err := svc.CanCreateService(1, "http", nil); err == nil {
		t.Fatal("expected limit error for check service regardless of host")
	}
}

// Agent metric services are bound to hosts and never count toward the service
// quota: no count query is run and creation is always allowed.
func TestCanCreateService_AgentService_NotCounted(t *testing.T) {
	db, mock := newDB(t)
	svc := NewPlanLimitsService(db)
	hostID := int64(10)
	mock.ExpectQuery("SELECT COALESCE").WillReturnRows(sqlmock.NewRows([]string{"plan", "trial_ends_at"}).AddRow("free", nil))
	if err := svc.CanCreateService(1, "agent_cpu", &hostID); err != nil {
		t.Fatalf("expected nil for agent service, got %v", err)
	}
}
