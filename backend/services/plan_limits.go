package services

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"middle-monitor/backend/models"
)

// PlanLimitError is returned when a free-plan organization exceeds a resource limit.
// Handlers check for this type via errors.As to return HTTP 400 instead of 500.
type PlanLimitError struct {
	Msg string
}

func (e *PlanLimitError) Error() string { return e.Msg }

// Free plan limits. MonitoredServices counts the user-created check services
// (http, ping, sql, certificate, snmp) — agent metric services are bound to hosts
// and not counted, SDK (error) services have their own cap.
const (
	FreeMaxHosts             = 1
	FreeMaxMonitoredServices = 1
	FreeMaxErrorServices     = 1
)

// Pro plan limits (base tier, must match the pricing page)
const (
	ProMaxHosts             = 10
	ProMaxMonitoredServices = 50
	ProMaxErrorServices     = 10
)

// Length of the Pro trial a self-serve signup starts on. No card is required:
// the org row stays on the free plan, only trial_ends_at is set.
const TrialDays = 14

// Data retention per plan, in days (must match the pricing page).
const (
	FreeRetentionDays = 7
	ProRetentionDays  = 30
)

// PlanLimits holds the resource caps for a plan. -1 means unlimited.
type PlanLimits struct {
	MaxHosts             int
	MaxMonitoredServices int
	MaxErrorServices     int
}

// OrgRetention is one organization's purge deadline: data older than Cutoff is deleted.
type OrgRetention struct {
	OrgID  int64
	Cutoff time.Time
}

// BillingEnabled reports whether this instance sells plans through Stripe; every
// process that gates on plans (api, receiver, worker) needs it.
func BillingEnabled() bool {
	return os.Getenv("BILLING_ENABLED") == "true"
}

// EffectivePlan returns the plan features and quotas are gated on. A free org
// with a running trial gets the Pro plan until trial_ends_at; the stored plan
// (the billing truth) is left untouched, so expiry needs no write.
// Without billing (self-hosted) every org is custom: caps and retention come
// from the per-org overrides an operator sets, unlimited by default.
func EffectivePlan(plan string, trialEndsAt *time.Time) string {
	if !BillingEnabled() {
		return "custom"
	}
	if plan == "free" && trialEndsAt != nil && trialEndsAt.After(time.Now()) {
		return "pro"
	}
	return plan
}

// ApplyTrial resolves an org's trial deadline into the plan its API payload and
// gating must expose. Every query loading an organization goes through it.
func ApplyTrial(org *models.Organization, trialEndsAt sql.NullTime) {
	org.BillingEnabled = BillingEnabled()
	if trialEndsAt.Valid && org.BillingEnabled {
		org.TrialEndsAt = &trialEndsAt.Time
	}
	org.Plan = EffectivePlan(org.Plan, org.TrialEndsAt)
}

// RetentionDays returns how long a plan keeps data. customDays only applies to
// the custom plan (0 = fall back to the Pro baseline). -1 means keep forever.
func RetentionDays(plan string, customDays int) int {
	switch plan {
	case "free":
		return FreeRetentionDays
	case "pro":
		return ProRetentionDays
	case "custom":
		if customDays > 0 {
			return customDays
		}
		return ProRetentionDays
	default:
		return -1
	}
}

// LimitsForPlan returns the resource caps for a plan name.
// Unknown plans (e.g. custom/enterprise) are treated as unlimited.
func LimitsForPlan(plan string) PlanLimits {
	switch plan {
	case "free":
		return PlanLimits{MaxHosts: FreeMaxHosts, MaxMonitoredServices: FreeMaxMonitoredServices, MaxErrorServices: FreeMaxErrorServices}
	case "pro":
		return PlanLimits{MaxHosts: ProMaxHosts, MaxMonitoredServices: ProMaxMonitoredServices, MaxErrorServices: ProMaxErrorServices}
	default:
		return PlanLimits{MaxHosts: -1, MaxMonitoredServices: -1, MaxErrorServices: -1}
	}
}

// monitoredServicesWhere matches the user-created check services that count
// toward the plan's service quota (excludes agent metrics and SDK services).
const monitoredServicesWhere = `type NOT LIKE 'agent_%' AND type NOT LIKE 'error_service_%'`

// PlanLimitsService checks organization plan limits
type PlanLimitsService struct {
	db *sql.DB
}

func NewPlanLimitsService(db *sql.DB) *PlanLimitsService {
	return &PlanLimitsService{db: db}
}

// GetPlan returns the effective plan for an organization (free, pro or custom):
// a free org still inside its trial window is gated as pro.
func (s *PlanLimitsService) GetPlan(orgID int64) (string, error) {
	var plan string
	var trialEndsAt sql.NullTime
	err := s.db.QueryRow(`SELECT COALESCE(plan, 'free'), trial_ends_at FROM organizations WHERE id = $1`, orgID).Scan(&plan, &trialEndsAt)
	if err != nil {
		return "", err
	}
	return EffectivePlan(plan, nullTimeToPtr(trialEndsAt)), nil
}

// resolveLimits returns the plan name and effective resource caps for an org in a single
// plan lookup. For custom plans the caps come from the purchased Stripe quantities stored
// on the organization (NULL = unlimited).
func (s *PlanLimitsService) resolveLimits(orgID int64) (string, PlanLimits, error) {
	plan, err := s.GetPlan(orgID)
	if err != nil {
		return "", PlanLimits{}, err
	}
	if plan != "custom" {
		return plan, LimitsForPlan(plan), nil
	}
	var h, sv, e sql.NullInt64
	err = s.db.QueryRow(`
		SELECT custom_max_hosts, custom_max_monitored_services, custom_max_error_services
		FROM organizations WHERE id = $1
	`, orgID).Scan(&h, &sv, &e)
	if err != nil {
		return "", PlanLimits{}, err
	}
	nullOr := func(n sql.NullInt64) int {
		if n.Valid {
			return int(n.Int64)
		}
		return -1
	}
	return plan, PlanLimits{MaxHosts: nullOr(h), MaxMonitoredServices: nullOr(sv), MaxErrorServices: nullOr(e)}, nil
}

// GetLimits returns the effective resource caps for an org.
func (s *PlanLimitsService) GetLimits(orgID int64) (PlanLimits, error) {
	_, limits, err := s.resolveLimits(orgID)
	return limits, err
}

// IsFree returns true if the org is on the free plan
func (s *PlanLimitsService) IsFree(orgID int64) (bool, error) {
	plan, err := s.GetPlan(orgID)
	if err != nil {
		return false, err
	}
	return plan == "free", nil
}

// CanCreateHost returns nil if the org can create a new host
func (s *PlanLimitsService) CanCreateHost(orgID int64) error {
	plan, limits, err := s.resolveLimits(orgID)
	if err != nil {
		return err
	}
	limit := limits.MaxHosts
	if limit < 0 {
		return nil
	}
	var count int
	err = s.db.QueryRow(`SELECT COUNT(*) FROM hosts WHERE organization_id = $1`, orgID).Scan(&count)
	if err != nil {
		return err
	}
	if count >= limit {
		return &PlanLimitError{Msg: fmt.Sprintf("plan limit: %s plan allows max %d host(s). Upgrade to add more", plan, limit)}
	}
	return nil
}

// CanCreateService returns nil if the org can create a new service
// serviceType: http, snmp, certificate, sql, error_service_*, agent_*
// hostID: nil for error_service (standalone), set for host-attached services
func (s *PlanLimitsService) CanCreateService(orgID int64, serviceType string, hostID *int64) error {
	plan, limits, err := s.resolveLimits(orgID)
	if err != nil {
		return err
	}

	// Error service (SDK): org-wide cap
	if strings.HasPrefix(serviceType, "error_service_") {
		if limits.MaxErrorServices < 0 {
			return nil
		}
		var count int
		err = s.db.QueryRow(`SELECT COUNT(*) FROM services WHERE organization_id = $1 AND type LIKE 'error_service_%'`, orgID).Scan(&count)
		if err != nil {
			return err
		}
		if count >= limits.MaxErrorServices {
			return &PlanLimitError{Msg: fmt.Sprintf("plan limit: %s plan allows max %d SDK service (errors, traces, logs, profiling). Upgrade to add more", plan, limits.MaxErrorServices)}
		}
		return nil
	}

	// Agent metric services (cpu/ram/disk/network) are auto-created per host and
	// bound by the host cap — they don't count toward the service quota.
	if strings.HasPrefix(serviceType, "agent_") {
		return nil
	}

	// Monitored check services (http, ping, sql, certificate, snmp): org-wide total cap.
	if limits.MaxMonitoredServices < 0 {
		return nil
	}
	var count int
	err = s.db.QueryRow(`SELECT COUNT(*) FROM services WHERE organization_id = $1 AND `+monitoredServicesWhere, orgID).Scan(&count)
	if err != nil {
		return err
	}
	if count >= limits.MaxMonitoredServices {
		return &PlanLimitError{Msg: fmt.Sprintf("plan limit: %s plan allows max %d monitored services. Upgrade to add more", plan, limits.MaxMonitoredServices)}
	}
	return nil
}
