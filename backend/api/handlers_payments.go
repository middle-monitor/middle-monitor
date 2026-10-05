package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"middle-monitor/backend/middleware"

	"github.com/gorilla/mux"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/billingportal/session"
	checkoutsession "github.com/stripe/stripe-go/v82/checkout/session"
	substripe "github.com/stripe/stripe-go/v82/subscription"
	"github.com/stripe/stripe-go/v82/webhook"
)

type CheckoutRequest struct {
	Plan string `json:"plan"`
}

// handleCreateCheckoutSession starts a Stripe Checkout subscription for Pro.
func handleCreateCheckoutSession() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := requireStripeConfigured(w); err != nil {
			return
		}

		orgID := middleware.GetOrganizationID(r.Context())
		if orgID <= 0 {
			respondError(w, http.StatusUnauthorized, ErrOrganizationNotFound)
			return
		}
		orgSlug := mux.Vars(r)["org_slug"]

		var req CheckoutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if req.Plan != "pro" {
			respondError(w, http.StatusBadRequest, ErrPlanNotPurchasable)
			return
		}

		priceID := stripeProPriceID()
		if priceID == "" {
			respondError(w, http.StatusInternalServerError, ErrStripePriceMissing)
			return
		}

		base := frontendBaseURL()
		settingsURL := fmt.Sprintf("%s/organizations/%s/settings", base, orgSlug)

		params := &stripe.CheckoutSessionParams{
			Mode: stripe.String(string(stripe.CheckoutSessionModeSubscription)),
			LineItems: []*stripe.CheckoutSessionLineItemParams{
				{
					Price:    stripe.String(priceID),
					Quantity: stripe.Int64(1),
				},
			},
			SuccessURL:        stripe.String(settingsURL + "?checkout=success"),
			CancelURL:         stripe.String(settingsURL + "?checkout=cancelled"),
			ClientReferenceID: stripe.String(strconv.FormatInt(orgID, 10)),
			Metadata: map[string]string{
				"organization_id":   strconv.FormatInt(orgID, 10),
				"organization_slug": orgSlug,
				"plan":              "pro",
			},
		}
		if email := middleware.GetUserEmail(r.Context()); email != "" {
			params.CustomerEmail = stripe.String(email)
		}

		sess, err := checkoutsession.New(params)
		if err != nil {
			slog.Error("stripe checkout session failed", "error", err)
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": sess.URL})
	}
}

type CustomCheckoutRequest struct {
	Hosts         int `json:"hosts"`
	Services      int `json:"services"`
	SDKServices   int `json:"sdk_services"`
	RetentionDays int `json:"retention_days"`
}

// handleCreateCustomCheckoutSession starts a Stripe Checkout for a custom plan.
// Line items: Pro base (fixed) + adjustable increments for hosts, services, SDK, and retention.
func handleCreateCustomCheckoutSession() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := requireStripeConfigured(w); err != nil {
			return
		}

		orgID := middleware.GetOrganizationID(r.Context())
		if orgID <= 0 {
			respondError(w, http.StatusUnauthorized, ErrOrganizationNotFound)
			return
		}
		orgSlug := mux.Vars(r)["org_slug"]

		var req CustomCheckoutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		priceIDs := map[string]string{
			"pro":      strings.TrimSpace(os.Getenv("STRIPE_PRICE_PRO")),
			"host":     strings.TrimSpace(os.Getenv("STRIPE_PRICE_EXTRA_HOST")),
			"service":  strings.TrimSpace(os.Getenv("STRIPE_PRICE_EXTRA_SERVICE")),
			"sdk":      strings.TrimSpace(os.Getenv("STRIPE_PRICE_EXTRA_SDK")),
			"ret_60d":  strings.TrimSpace(os.Getenv("STRIPE_PRICE_RETENTION_60D")),
			"ret_90d":  strings.TrimSpace(os.Getenv("STRIPE_PRICE_RETENTION_90D")),
			"ret_365d": strings.TrimSpace(os.Getenv("STRIPE_PRICE_RETENTION_365D")),
		}
		if priceIDs["pro"] == "" {
			respondError(w, http.StatusInternalServerError, ErrStripePriceMissing)
			return
		}

		lineItems := []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String(priceIDs["pro"]),
				Quantity: stripe.Int64(1),
			},
		}

		addAdjustable := func(priceKey string, qty, max int64) {
			if priceIDs[priceKey] == "" || qty <= 0 {
				return
			}
			lineItems = append(lineItems, &stripe.CheckoutSessionLineItemParams{
				Price:    stripe.String(priceIDs[priceKey]),
				Quantity: stripe.Int64(qty),
				AdjustableQuantity: &stripe.CheckoutSessionLineItemAdjustableQuantityParams{
					Enabled: stripe.Bool(true),
					Minimum: stripe.Int64(0),
					Maximum: stripe.Int64(max),
				},
			})
		}

		addAdjustable("host", int64(req.Hosts-10), 190)
		addAdjustable("service", int64(req.Services-50), 450)
		addAdjustable("sdk", int64(req.SDKServices-10), 90)

		// Retention is a discrete flat add-on, not per-unit.
		retentionKey := ""
		switch req.RetentionDays {
		case 60:
			retentionKey = "ret_60d"
		case 90:
			retentionKey = "ret_90d"
		case 365:
			retentionKey = "ret_365d"
		}
		if retentionKey != "" && priceIDs[retentionKey] != "" {
			lineItems = append(lineItems, &stripe.CheckoutSessionLineItemParams{
				Price:    stripe.String(priceIDs[retentionKey]),
				Quantity: stripe.Int64(1),
			})
		}

		base := frontendBaseURL()
		settingsURL := fmt.Sprintf("%s/organizations/%s/settings", base, orgSlug)

		params := &stripe.CheckoutSessionParams{
			Mode:              stripe.String(string(stripe.CheckoutSessionModeSubscription)),
			LineItems:         lineItems,
			SuccessURL:        stripe.String(settingsURL + "?checkout=success"),
			CancelURL:         stripe.String(settingsURL + "?checkout=cancelled"),
			ClientReferenceID: stripe.String(strconv.FormatInt(orgID, 10)),
			Metadata: map[string]string{
				"organization_id":   strconv.FormatInt(orgID, 10),
				"organization_slug": orgSlug,
				"plan":              "custom",
			},
		}
		if email := middleware.GetUserEmail(r.Context()); email != "" {
			params.CustomerEmail = stripe.String(email)
		}

		sess, err := checkoutsession.New(params)
		if err != nil {
			slog.Error("stripe custom checkout session failed", "error", err)
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": sess.URL})
	}
}

// retentionPriceID maps a retention day count to its Stripe price ID (empty for the 30-day baseline).
func retentionPriceID(days int) string {
	switch days {
	case 60:
		return strings.TrimSpace(os.Getenv("STRIPE_PRICE_RETENTION_60D"))
	case 90:
		return strings.TrimSpace(os.Getenv("STRIPE_PRICE_RETENTION_90D"))
	case 365:
		return strings.TrimSpace(os.Getenv("STRIPE_PRICE_RETENTION_365D"))
	}
	return ""
}

// managedAddonPriceIDs returns every add-on price the app manages on a subscription
// (per-unit increments + the discrete retention tiers). Pro base is never in this set.
func managedAddonPriceIDs() map[string]bool {
	ids := map[string]bool{}
	for _, k := range []string{
		"STRIPE_PRICE_EXTRA_HOST", "STRIPE_PRICE_EXTRA_SERVICE", "STRIPE_PRICE_EXTRA_SDK",
		"STRIPE_PRICE_RETENTION_60D", "STRIPE_PRICE_RETENTION_90D", "STRIPE_PRICE_RETENTION_365D",
	} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			ids[v] = true
		}
	}
	return ids
}

// syncCustomLimits derives the purchased resource caps from a Stripe subscription's line
// items and stores them on the organization, so the UI and enforcement reflect what the
// customer actually pays for (instead of treating custom as unlimited).
func syncCustomLimits(db *sql.DB, orgID int64, subID string) {
	if subID == "" {
		return
	}
	sub, err := substripe.Get(subID, nil)
	if err != nil {
		slog.Error("stripe sync custom limits: subscription lookup failed", "error", err)
		return
	}
	hosts, services, sdk, retention := 10, 50, 10, 30
	hostPrice := strings.TrimSpace(os.Getenv("STRIPE_PRICE_EXTRA_HOST"))
	svcPrice := strings.TrimSpace(os.Getenv("STRIPE_PRICE_EXTRA_SERVICE"))
	sdkPrice := strings.TrimSpace(os.Getenv("STRIPE_PRICE_EXTRA_SDK"))
	for _, it := range sub.Items.Data {
		if it.Price == nil {
			continue
		}
		switch it.Price.ID {
		case hostPrice:
			hosts = 10 + int(it.Quantity)
		case svcPrice:
			services = 50 + int(it.Quantity)
		case sdkPrice:
			sdk = 10 + int(it.Quantity)
		case retentionPriceID(60):
			if it.Quantity > 0 {
				retention = 60
			}
		case retentionPriceID(90):
			if it.Quantity > 0 {
				retention = 90
			}
		case retentionPriceID(365):
			if it.Quantity > 0 {
				retention = 365
			}
		}
	}
	if _, err := db.Exec(`
		UPDATE organizations
		SET custom_max_hosts = $1, custom_max_monitored_services = $2, custom_max_error_services = $3,
		    custom_retention_days = $4, updated_at = NOW()
		WHERE id = $5
	`, hosts, services, sdk, retention, orgID); err != nil {
		slog.Error("stripe sync custom limits: write failed", "error", err)
	}
}

// handleGetSubscription returns the current purchased quantities (derived from the Stripe
// subscription line items) so the pricing calculator can pre-fill the sliders.
func handleGetSubscription(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := requireStripeConfigured(w); err != nil {
			return
		}

		orgID := middleware.GetOrganizationID(r.Context())
		if orgID <= 0 {
			respondError(w, http.StatusUnauthorized, ErrOrganizationNotFound)
			return
		}

		var subID string
		err := db.QueryRow(`
			SELECT stripe_subscription_id FROM subscriptions
			WHERE organization_id = $1 AND stripe_subscription_id IS NOT NULL AND stripe_subscription_id <> ''
		`, orgID).Scan(&subID)
		if err == sql.ErrNoRows || subID == "" {
			respondError(w, http.StatusBadRequest, ErrSubscriptionMissing)
			return
		}
		if err != nil {
			slog.Error("stripe subscription lookup failed", "error", err)
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		sub, err := substripe.Get(subID, nil)
		if err != nil {
			slog.Error("stripe get subscription failed", "error", err)
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		// Start from the Pro baseline; add the purchased add-on quantities on top.
		hosts, services, sdk, retention := 10, 50, 10, 30
		hostPrice := strings.TrimSpace(os.Getenv("STRIPE_PRICE_EXTRA_HOST"))
		svcPrice := strings.TrimSpace(os.Getenv("STRIPE_PRICE_EXTRA_SERVICE"))
		sdkPrice := strings.TrimSpace(os.Getenv("STRIPE_PRICE_EXTRA_SDK"))
		for _, it := range sub.Items.Data {
			if it.Price == nil {
				continue
			}
			switch it.Price.ID {
			case hostPrice:
				hosts = 10 + int(it.Quantity)
			case svcPrice:
				services = 50 + int(it.Quantity)
			case sdkPrice:
				sdk = 10 + int(it.Quantity)
			case retentionPriceID(60):
				if it.Quantity > 0 {
					retention = 60
				}
			case retentionPriceID(90):
				if it.Quantity > 0 {
					retention = 90
				}
			case retentionPriceID(365):
				if it.Quantity > 0 {
					retention = 365
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{
			"hosts":          hosts,
			"services":       services,
			"sdk_services":   sdk,
			"retention_days": retention,
		})
	}
}

// handleUpdateSubscription modifies an existing subscription's add-on quantities so a
// Pro subscriber can add hosts/services/SDK (moving them to Custom). Stripe prorates and
// invoices the difference immediately on the saved payment method.
func handleUpdateSubscription(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := requireStripeConfigured(w); err != nil {
			return
		}

		orgID := middleware.GetOrganizationID(r.Context())
		if orgID <= 0 {
			respondError(w, http.StatusUnauthorized, ErrOrganizationNotFound)
			return
		}

		var req CustomCheckoutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		var subID string
		err := db.QueryRow(`
			SELECT stripe_subscription_id FROM subscriptions
			WHERE organization_id = $1 AND stripe_subscription_id IS NOT NULL AND stripe_subscription_id <> ''
		`, orgID).Scan(&subID)
		if err == sql.ErrNoRows || subID == "" {
			respondError(w, http.StatusBadRequest, ErrSubscriptionUpdateMissing)
			return
		}
		if err != nil {
			slog.Error("stripe subscription update lookup failed", "error", err)
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		sub, err := substripe.Get(subID, nil)
		if err != nil {
			slog.Error("stripe get subscription failed", "error", err)
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		// Desired quantity per managed add-on price (0 = remove).
		desired := map[string]int64{}
		if id := strings.TrimSpace(os.Getenv("STRIPE_PRICE_EXTRA_HOST")); id != "" {
			desired[id] = int64(maxInt(0, req.Hosts-10))
		}
		if id := strings.TrimSpace(os.Getenv("STRIPE_PRICE_EXTRA_SERVICE")); id != "" {
			desired[id] = int64(maxInt(0, req.Services-50))
		}
		if id := strings.TrimSpace(os.Getenv("STRIPE_PRICE_EXTRA_SDK")); id != "" {
			desired[id] = int64(maxInt(0, req.SDKServices-10))
		}
		// Retention: the chosen tier gets quantity 1, the others are removed.
		for _, id := range []string{retentionPriceID(60), retentionPriceID(90), retentionPriceID(365)} {
			if id != "" {
				desired[id] = 0
			}
		}
		if rid := retentionPriceID(req.RetentionDays); rid != "" {
			desired[rid] = 1
		}

		managed := managedAddonPriceIDs()
		existing := map[string]string{} // priceID -> subscription item ID
		for _, it := range sub.Items.Data {
			if it.Price != nil && managed[it.Price.ID] {
				existing[it.Price.ID] = it.ID
			}
		}

		var items []*stripe.SubscriptionItemsParams
		for priceID, qty := range desired {
			if itemID, ok := existing[priceID]; ok {
				if qty == 0 {
					items = append(items, &stripe.SubscriptionItemsParams{ID: stripe.String(itemID), Deleted: stripe.Bool(true)})
				} else {
					items = append(items, &stripe.SubscriptionItemsParams{ID: stripe.String(itemID), Quantity: stripe.Int64(qty)})
				}
			} else if qty > 0 {
				items = append(items, &stripe.SubscriptionItemsParams{Price: stripe.String(priceID), Quantity: stripe.Int64(qty)})
			}
		}

		if len(items) == 0 {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "unchanged"})
			return
		}

		_, err = substripe.Update(subID, &stripe.SubscriptionParams{
			Items:             items,
			ProrationBehavior: stripe.String("always_invoice"),
		})
		if err != nil {
			slog.Error("stripe subscription update failed", "error", err)
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		// The customer.subscription.updated webhook reconciles the plan; update locally too
		// so the UI reflects it without waiting for the webhook round-trip. Any add-on with a
		// positive quantity means custom; pure base means pro.
		newPlan := "pro"
		for _, qty := range desired {
			if qty > 0 {
				newPlan = "custom"
				break
			}
		}
		if err := setOrgPlan(db, orgID, newPlan); err != nil {
			slog.Error("failed to set org plan after update", "plan", newPlan, "error", err)
		}
		if newPlan == "custom" {
			syncCustomLimits(db, orgID, subID)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// handleCreateBillingPortal opens the Stripe Customer Portal (manage/cancel subscription).
func handleCreateBillingPortal(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := requireStripeConfigured(w); err != nil {
			return
		}

		orgID := middleware.GetOrganizationID(r.Context())
		orgSlug := mux.Vars(r)["org_slug"]

		var customerID string
		err := db.QueryRow(`
			SELECT stripe_customer_id FROM subscriptions
			WHERE organization_id = $1 AND stripe_customer_id IS NOT NULL AND stripe_customer_id <> ''
		`, orgID).Scan(&customerID)
		if err == sql.ErrNoRows || customerID == "" {
			respondError(w, http.StatusBadRequest, ErrStripeSubscriptionMissing)
			return
		}
		if err != nil {
			slog.Error("stripe billing portal lookup failed", "error", err)
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		base := frontendBaseURL()
		returnURL := fmt.Sprintf("%s/organizations/%s/settings", base, orgSlug)

		sess, err := session.New(&stripe.BillingPortalSessionParams{
			Customer:  stripe.String(customerID),
			ReturnURL: stripe.String(returnURL),
		})
		if err != nil {
			slog.Error("stripe billing portal failed", "error", err)
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": sess.URL})
	}
}

// handleStripeWebhook processes Stripe subscription lifecycle events.
func handleStripeWebhook(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const maxBodyBytes = int64(65536)
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

		payload, err := io.ReadAll(r.Body)
		if err != nil {
			respondError(w, http.StatusServiceUnavailable, err)
			return
		}

		secret := os.Getenv("STRIPE_WEBHOOK_SECRET")
		if secret == "" {
			respondError(w, http.StatusInternalServerError, ErrStripeWebhookSecretMissing)
			return
		}

		event, err := webhook.ConstructEvent(payload, r.Header.Get("Stripe-Signature"), secret)
		if err != nil {
			slog.Warn("stripe webhook signature invalid", "error", err)
			respondError(w, http.StatusBadRequest, ErrWebhookSignatureInvalid)
			return
		}

		switch event.Type {
		case "checkout.session.completed":
			handleCheckoutCompleted(db, event.Data.Raw)
		case "customer.subscription.updated":
			handleSubscriptionUpdated(db, event.Data.Raw)
		case "customer.subscription.deleted":
			handleSubscriptionDeleted(db, event.Data.Raw)
		default:
			slog.Debug("stripe webhook event ignored", "type", event.Type)
		}

		w.WriteHeader(http.StatusOK)
	}
}

func handleCheckoutCompleted(db *sql.DB, raw []byte) {
	// event.Data.Raw is already the session itself, not a {"object": ...} wrapper.
	var sess struct {
		ID                string            `json:"id"`
		ClientReferenceID string            `json:"client_reference_id"`
		Customer          string            `json:"customer"`
		Subscription      string            `json:"subscription"`
		Metadata          map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &sess); err != nil {
		slog.Error("failed to parse checkout.session.completed", "error", err)
		return
	}

	orgID, _ := strconv.ParseInt(sess.ClientReferenceID, 10, 64)
	orgSlug := ""
	if sess.Metadata != nil {
		if orgID == 0 {
			orgID, _ = strconv.ParseInt(sess.Metadata["organization_id"], 10, 64)
		}
		orgSlug = sess.Metadata["organization_slug"]
	}
	if orgID <= 0 {
		slog.Error("checkout.session.completed has no organization_id")
		return
	}

	plan := "pro"
	if sess.Metadata != nil && sess.Metadata["plan"] == "custom" {
		plan = "custom"
	}

	if err := upsertSubscription(db, orgID, orgSlug, sess.Customer, sess.Subscription, plan, "active"); err != nil {
		slog.Error("failed to upsert subscription after checkout", "error", err)
		return
	}
	if err := setOrgPlan(db, orgID, plan); err != nil {
		slog.Error("failed to set org plan", "plan", plan, "error", err)
		return
	}
	if plan == "custom" {
		syncCustomLimits(db, orgID, sess.Subscription)
	}
	slog.Info("org upgraded", "org_id", orgID, "plan", plan, "customer", sess.Customer)
}

func handleSubscriptionUpdated(db *sql.DB, raw []byte) {
	// event.Data.Raw is already the subscription itself, not a {"object": ...} wrapper.
	var sub struct {
		ID                string `json:"id"`
		Status            string `json:"status"`
		Customer          string `json:"customer"`
		CancelAtPeriodEnd bool   `json:"cancel_at_period_end"`
		Items             struct {
			Data []struct {
				Quantity int64 `json:"quantity"`
				Price    struct {
					ID string `json:"id"`
				} `json:"price"`
			} `json:"data"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &sub); err != nil {
		slog.Error("failed to parse subscription.updated", "error", err)
		return
	}

	orgID, orgSlug, err := orgFromStripeSubscription(db, sub.ID)
	if err != nil {
		slog.Error("subscription.updated lookup failed", "error", err)
		return
	}

	plan := "free"
	status := sub.Status
	if sub.Status == "active" && !sub.CancelAtPeriodEnd {
		// Any add-on line item with a positive quantity means the org went beyond
		// the Pro base tier — that's a custom plan.
		plan = "pro"
		managed := managedAddonPriceIDs()
		for _, it := range sub.Items.Data {
			if it.Quantity > 0 && managed[it.Price.ID] {
				plan = "custom"
				break
			}
		}
	}

	if err := upsertSubscription(db, orgID, orgSlug, sub.Customer, sub.ID, plan, status); err != nil {
		slog.Error("failed to upsert subscription on update", "error", err)
		return
	}
	if err := setOrgPlan(db, orgID, plan); err != nil {
		slog.Error("failed to set org plan on update", "error", err)
		return
	}
	if plan == "custom" {
		syncCustomLimits(db, orgID, sub.ID)
	}
	slog.Info("org plan updated", "org_id", orgID, "plan", plan, "status", status)
}

func handleSubscriptionDeleted(db *sql.DB, raw []byte) {
	// event.Data.Raw is already the subscription itself, not a {"object": ...} wrapper.
	var sub struct {
		ID       string `json:"id"`
		Customer string `json:"customer"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(raw, &sub); err != nil {
		slog.Error("failed to parse subscription.deleted", "error", err)
		return
	}

	orgID, orgSlug, err := orgFromStripeSubscription(db, sub.ID)
	if err != nil {
		slog.Error("subscription.deleted lookup failed", "error", err)
		return
	}

	if err := upsertSubscription(db, orgID, orgSlug, sub.Customer, sub.ID, "free", "canceled"); err != nil {
		slog.Error("failed to upsert subscription on delete", "error", err)
		return
	}
	if err := setOrgPlan(db, orgID, "free"); err != nil {
		slog.Error("failed to set org plan to free on delete", "error", err)
		return
	}
	slog.Info("org downgraded to free", "org_id", orgID)
}

func orgFromStripeSubscription(db *sql.DB, stripeSubID string) (orgID int64, orgSlug string, err error) {
	err = db.QueryRow(`
		SELECT organization_id, organization_slug FROM subscriptions WHERE stripe_subscription_id = $1
	`, stripeSubID).Scan(&orgID, &orgSlug)
	return
}

func upsertSubscription(db *sql.DB, orgID int64, orgSlug, customerID, subscriptionID, plan, status string) error {
	_, err := db.Exec(`
		INSERT INTO subscriptions (organization_id, organization_slug, stripe_customer_id, stripe_subscription_id, plan, status, updated_at)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5, $6, NOW())
		ON CONFLICT (organization_id) DO UPDATE SET
			organization_slug = EXCLUDED.organization_slug,
			stripe_customer_id = COALESCE(EXCLUDED.stripe_customer_id, subscriptions.stripe_customer_id),
			stripe_subscription_id = COALESCE(EXCLUDED.stripe_subscription_id, subscriptions.stripe_subscription_id),
			plan = EXCLUDED.plan,
			status = EXCLUDED.status,
			updated_at = NOW()
	`, orgID, orgSlug, customerID, subscriptionID, plan, status)
	return err
}

func setOrgPlan(db *sql.DB, orgID int64, plan string) error {
	switch plan {
	case "pro", "custom":
	default:
		plan = "free"
	}
	// Any billing outcome closes the trial: a leftover deadline would keep the org
	// on trial-priced access (and the UI in trial mode) after paying or canceling.
	_, err := db.Exec(`UPDATE organizations SET plan = $1, trial_ends_at = NULL, updated_at = NOW() WHERE id = $2`, plan, orgID)
	return err
}

func requireStripeConfigured(w http.ResponseWriter) error {
	stripe.Key = os.Getenv("STRIPE_SECRET_KEY")
	if stripe.Key == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "Stripe payments are not configured on this instance. Set STRIPE_SECRET_KEY, STRIPE_PRICE_PRO, and STRIPE_WEBHOOK_SECRET to enable upgrades."})
		return ErrStripeNotConfigured
	}
	return nil
}

func stripeProPriceID() string {
	if id := strings.TrimSpace(os.Getenv("STRIPE_PRICE_PRO")); id != "" {
		return id
	}
	return strings.TrimSpace(os.Getenv("STRIPE_PRO_PRICE_ID"))
}

func frontendBaseURL() string {
	raw := os.Getenv("FRONTEND_URL")
	if raw == "" {
		return "http://localhost:3000"
	}
	for _, origin := range strings.Split(raw, ",") {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			return strings.TrimRight(origin, "/")
		}
	}
	return "http://localhost:3000"
}
