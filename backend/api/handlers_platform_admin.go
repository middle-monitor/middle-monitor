package api

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"

	"middle-monitor/backend/middleware"
)

// PlatformOrganization is one row of the cross-org admin listing: just enough to
// see and change plans without exposing anything else about the tenant.
type PlatformOrganization struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	Plan        string     `json:"plan"`
	TrialEndsAt *time.Time `json:"trial_ends_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// handleListPlatformOrganizations lists every organization on the instance, for
// the platform-admin plan-management page.
func handleListPlatformOrganizations(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(`
			SELECT id, name, slug, COALESCE(plan, 'free'), trial_ends_at, created_at
			FROM organizations
			ORDER BY created_at DESC
		`)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		defer rows.Close()

		orgs := []PlatformOrganization{}
		for rows.Next() {
			var o PlatformOrganization
			var trialEndsAt sql.NullTime
			if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.Plan, &trialEndsAt, &o.CreatedAt); err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
			if trialEndsAt.Valid {
				o.TrialEndsAt = &trialEndsAt.Time
			}
			orgs = append(orgs, o)
		}
		if err := rows.Err(); err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		respondJSON(w, orgs)
	}
}

// handleSetPlatformOrganizationPlan overrides an organization's plan directly,
// bypassing Stripe. For comping or downgrading an account by hand; setOrgPlan
// also clears any running trial so the override sticks.
func handleSetPlatformOrganizationPlan(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaimsFromContext(r.Context())
		if claims == nil {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		orgID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrOrganizationIDInvalid)
			return
		}

		var req struct {
			Plan string `json:"plan"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		switch req.Plan {
		case "free", "pro", "custom":
		default:
			respondError(w, http.StatusBadRequest, ErrPlanInvalid)
			return
		}

		// setOrgPlan writes with a WHERE that simply matches nothing on an unknown
		// id, and reports no error: on the route whose whole job is to comp or
		// downgrade one account by hand, a typo would read as a success.
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM organizations WHERE id = $1)`, orgID).Scan(&exists); err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		if !exists {
			respondError(w, http.StatusNotFound, ErrOrganizationNotFound)
			return
		}

		if err := setOrgPlan(db, orgID, req.Plan); err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		// Every Stripe-driven plan change is logged; this one has a human behind it.
		slog.Info("org plan overridden", "org_id", orgID, "plan", req.Plan, "by", claims.Email)

		respondJSON(w, map[string]string{"plan": req.Plan})
	}
}
