package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/webhook"
)

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

type CreateCheckoutRequest struct {
	OrganizationID   int64  `json:"organization_id"`
	OrganizationSlug string `json:"organization_slug"`
	Plan             string `json:"plan"`
	SuccessURL       string `json:"success_url"`
	CancelURL        string `json:"cancel_url"`
}

type CreateCheckoutResponse struct {
	URL string `json:"url"`
}

func jsonError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func handleCreateCheckout(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := os.Getenv("STRIPE_SECRET_KEY")
		if key == "" {
			jsonError(w, "Stripe payments are not configured on this instance. Set STRIPE_SECRET_KEY and STRIPE_PRICE_PRO to enable upgrades.", http.StatusServiceUnavailable)
			return
		}
		priceID := os.Getenv("STRIPE_PRICE_PRO")
		if priceID == "" {
			jsonError(w, "Stripe price ID not configured (STRIPE_PRICE_PRO missing).", http.StatusServiceUnavailable)
			return
		}

		var req CreateCheckoutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if req.OrganizationID <= 0 || req.OrganizationSlug == "" || req.Plan == "" || req.SuccessURL == "" || req.CancelURL == "" {
			http.Error(w, "missing required fields", http.StatusBadRequest)
			return
		}
		if req.Plan != "pro" {
			http.Error(w, "invalid plan", http.StatusBadRequest)
			return
		}

		stripe.Key = key

		params := &stripe.CheckoutSessionParams{
			Mode: stripe.String(string(stripe.CheckoutSessionModeSubscription)),
			LineItems: []*stripe.CheckoutSessionLineItemParams{
				{
					Price:    stripe.String(priceID),
					Quantity: stripe.Int64(1),
				},
			},
			SuccessURL:        stripe.String(req.SuccessURL),
			CancelURL:         stripe.String(req.CancelURL),
			ClientReferenceID: stripe.String(strconv.FormatInt(req.OrganizationID, 10)),
			Metadata: map[string]string{
				"organization_id":   strconv.FormatInt(req.OrganizationID, 10),
				"organization_slug": req.OrganizationSlug,
				"plan":              req.Plan,
			},
		}

		sess, err := session.New(params)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(CreateCheckoutResponse{URL: sess.URL})
	}
}

func handleStripeWebhook(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := os.Getenv("STRIPE_SECRET_KEY")
		secret := os.Getenv("STRIPE_WEBHOOK_SECRET")
		if key == "" || secret == "" {
			http.Error(w, "webhook not configured", http.StatusInternalServerError)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}

		event, err := webhook.ConstructEvent(body, r.Header.Get("Stripe-Signature"), secret)
		if err != nil {
			http.Error(w, "invalid signature", http.StatusBadRequest)
			return
		}

		switch event.Type {
		case "checkout.session.completed":
			handleCheckoutCompleted(db, event.Data.Raw)
		case "customer.subscription.updated", "customer.subscription.deleted":
			handleSubscriptionChange(db, string(event.Type), event.Data.Raw)
		}

		w.WriteHeader(http.StatusOK)
	}
}

func handleCheckoutCompleted(db *sql.DB, payload []byte) {
	// event.Data.Raw is already the session itself, not a {"object": ...} wrapper.
	var sess struct {
		ID                string            `json:"id"`
		ClientReferenceID string            `json:"client_reference_id"`
		Customer          string            `json:"customer"`
		Subscription      string            `json:"subscription"`
		Metadata          map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(payload, &sess); err != nil {
		return
	}
	orgID, _ := strconv.ParseInt(sess.ClientReferenceID, 10, 64)
	if orgID == 0 && sess.Metadata != nil {
		orgID, _ = strconv.ParseInt(sess.Metadata["organization_id"], 10, 64)
	}
	orgSlug := ""
	if sess.Metadata != nil {
		orgSlug = sess.Metadata["organization_slug"]
	}
	plan := "pro"
	if sess.Metadata != nil && sess.Metadata["plan"] != "" {
		plan = sess.Metadata["plan"]
	}

	_, err := db.Exec(`
		INSERT INTO subscriptions (organization_id, organization_slug, stripe_customer_id, stripe_subscription_id, plan, status, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'active', NOW())
		ON CONFLICT (organization_id) DO UPDATE SET
			stripe_customer_id = EXCLUDED.stripe_customer_id,
			stripe_subscription_id = EXCLUDED.stripe_subscription_id,
			plan = EXCLUDED.plan,
			status = 'active',
			updated_at = NOW()
	`, orgID, orgSlug, sess.Customer, sess.Subscription, plan)
	if err != nil {
		return
	}

	syncOrgPlanToMainBackend(orgID, plan)
}

func handleSubscriptionChange(db *sql.DB, eventType string, payload []byte) {
	// event.Data.Raw is already the subscription itself, not a {"object": ...} wrapper.
	var sub struct {
		ID                string            `json:"id"`
		Status            string            `json:"status"`
		Metadata          map[string]string `json:"metadata"`
		CancelAtPeriodEnd bool              `json:"cancel_at_period_end"`
	}
	if err := json.Unmarshal(payload, &sub); err != nil {
		return
	}

	var orgID int64
	var orgSlug string
	err := db.QueryRow(`
		SELECT organization_id, organization_slug FROM subscriptions WHERE stripe_subscription_id = $1
	`, sub.ID).Scan(&orgID, &orgSlug)
	if err != nil {
		return
	}

	plan := "free"
	if eventType == "customer.subscription.updated" && sub.Status == "active" && !sub.CancelAtPeriodEnd {
		plan = "pro"
	}

	_, err = db.Exec(`
		UPDATE subscriptions SET plan = $1, status = $2, updated_at = NOW()
		WHERE stripe_subscription_id = $3
	`, plan, sub.Status, sub.ID)
	if err != nil {
		return
	}

	syncOrgPlanToMainBackend(orgID, plan)
}

func syncOrgPlanToMainBackend(orgID int64, plan string) {
	mainURL := os.Getenv("MAIN_BACKEND_URL")
	apiKey := os.Getenv("MAIN_BACKEND_INTERNAL_KEY")
	if mainURL == "" || apiKey == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{"plan": plan})
	req, err := http.NewRequest("POST", mainURL+"/internal/organizations/"+strconv.FormatInt(orgID, 10)+"/plan", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Key", apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func handleGetSubscription(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		orgID, err := strconv.ParseInt(vars["orgId"], 10, 64)
		if err != nil {
			http.Error(w, "invalid org id", http.StatusBadRequest)
			return
		}

		var plan, status string
		err = db.QueryRow(`
			SELECT plan, status FROM subscriptions WHERE organization_id = $1
		`, orgID).Scan(&plan, &status)
		if err == sql.ErrNoRows {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"plan": "free", "status": ""})
			return
		}
		if err != nil {
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"plan": plan, "status": status})
	}
}
