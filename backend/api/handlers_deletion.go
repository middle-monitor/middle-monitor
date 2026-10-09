package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/stripe/stripe-go/v82"
	substripe "github.com/stripe/stripe-go/v82/subscription"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/services"
)

// deleteOrganization stops the org's billing, deletes its rows, then starts the
// purge of its stored telemetry.
func deleteOrganization(db *sql.DB, authService *services.AuthService, opensearch *services.OpenSearchService, orgID int64) error {
	if err := cancelSubscription(db, orgID); err != nil {
		return err
	}
	if err := authService.DeleteOrganization(orgID); err != nil {
		return err
	}
	if opensearch != nil {
		// The rows are gone already; a failed purge is logged, not reported.
		if err := opensearch.DeleteOrganizationData(orgID); err != nil {
			slog.Error("organization data purge failed", "org_id", orgID, "error", err)
		}
	}
	slog.Info("organization deleted", "org_id", orgID)
	return nil
}

// cancelSubscription ends an org's Stripe subscription, so a deleted org is
// never charged again.
func cancelSubscription(db *sql.DB, orgID int64) error {
	var subID string
	err := db.QueryRow(`
		SELECT stripe_subscription_id FROM subscriptions
		WHERE organization_id = $1 AND stripe_subscription_id IS NOT NULL AND stripe_subscription_id <> ''
	`, orgID).Scan(&subID)
	if err == sql.ErrNoRows {
		return nil
	} else if err != nil {
		return fmt.Errorf("%w: %w", ErrSubscriptionCancel, err)
	}
	stripe.Key = os.Getenv("STRIPE_SECRET_KEY")
	if stripe.Key == "" {
		return nil
	}
	if _, err := substripe.Cancel(subID, nil); err != nil {
		var stripeErr *stripe.Error
		if errors.As(err, &stripeErr) && stripeErr.Code == stripe.ErrorCodeResourceMissing {
			return nil
		}
		return fmt.Errorf("%w: %w", ErrSubscriptionCancel, err)
	}
	return nil
}

// handleDeleteOrganization deletes the active organization (admin only). The body
// must repeat the org slug, so a stray request cannot wipe an organization.
func handleDeleteOrganization(db *sql.DB, authService *services.AuthService, opensearch *services.OpenSearchService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		if orgID <= 0 {
			respondError(w, http.StatusUnauthorized, ErrOrganizationNotFound)
			return
		}
		var req struct {
			Confirm string `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		var slug string
		if err := db.QueryRow(`SELECT slug FROM organizations WHERE id = $1`, orgID).Scan(&slug); err != nil {
			respondError(w, http.StatusNotFound, ErrOrganizationNotFound)
			return
		}
		if req.Confirm != slug {
			respondError(w, http.StatusBadRequest, ErrDeleteConfirmMismatch)
			return
		}
		if err := deleteOrganization(db, authService, opensearch, orgID); err != nil {
			respondError(w, deletionStatus(err), err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleDeleteAccount deletes the caller's account after checking their password.
// Organizations they are the only member of are deleted with it.
func handleDeleteAccount(db *sql.DB, authService *services.AuthService, opensearch *services.OpenSearchService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := middleware.GetUserID(r.Context())
		if userID <= 0 {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}
		var req struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if err := authService.CheckPassword(userID, req.Password); err != nil {
			if errors.Is(err, services.ErrInvalidCredentials) {
				err = ErrPasswordIncorrect
			}
			respondError(w, deletionStatus(err), err)
			return
		}
		orgIDs, err := authService.OrgsDeletedWithAccount(userID)
		if err != nil {
			if errors.Is(err, services.ErrLastAdmin) {
				err = ErrLastAdminLeaving
			}
			respondError(w, deletionStatus(err), err)
			return
		}
		for _, orgID := range orgIDs {
			if err := deleteOrganization(db, authService, opensearch, orgID); err != nil {
				respondError(w, deletionStatus(err), err)
				return
			}
		}
		if err := authService.DeleteAccount(userID); err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		slog.Info("account deleted", "user_id", userID, "organizations_deleted", len(orgIDs))
		w.WriteHeader(http.StatusNoContent)
	}
}

func deletionStatus(err error) int {
	switch {
	case errors.Is(err, ErrPasswordIncorrect):
		return http.StatusForbidden
	case errors.Is(err, ErrLastAdminLeaving):
		return http.StatusConflict
	case errors.Is(err, services.ErrOrgNotFound), errors.Is(err, services.ErrUserNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrSubscriptionCancel):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}
