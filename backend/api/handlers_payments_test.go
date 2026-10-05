package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gorilla/mux"
	"github.com/stripe/stripe-go/v82"
)

// stubStripe points the Stripe SDK at a local server for the duration of a
// test, so the billing handlers can be driven without a live account. It
// returns the form values of every request the handler made.
func stubStripe(t *testing.T, handler http.HandlerFunc) *[]url.Values {
	t.Helper()
	var seen []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		seen = append(seen, r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	t.Setenv("STRIPE_SECRET_KEY", "sk_test_stub")
	previous := stripe.GetBackend(stripe.APIBackend)
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL:               stripe.String(server.URL),
		MaxNetworkRetries: stripe.Int64(0),
	}))
	t.Cleanup(func() { stripe.SetBackend(stripe.APIBackend, previous) })
	return &seen
}

// stripeSessionRequest builds an org-scoped request carrying the slug the
// success/cancel URLs are built from.
func stripeSessionRequest(body string) *http.Request {
	r := orgRequest("POST", "/x", body, map[string]string{"org_slug": "acme"})
	return mux.SetURLVars(r, map[string]string{"org_slug": "acme"})
}

// An instance with no Stripe key is not broken, it simply does not sell: the
// answer says so instead of failing with a bare 500.
func TestBillingEndpointsAreDisabledWithoutStripe(t *testing.T) {
	t.Setenv("STRIPE_SECRET_KEY", "")
	db, _, _ := sqlmock.New()
	defer db.Close()

	handlers := map[string]http.HandlerFunc{
		"checkout":        handleCreateCheckoutSession(),
		"custom checkout": handleCreateCustomCheckoutSession(),
		"subscription":    handleGetSubscription(db),
		"update sub":      handleUpdateSubscription(db),
		"billing portal":  handleCreateBillingPortal(db),
	}
	for name, handler := range handlers {
		rec := httptest.NewRecorder()
		handler(rec, stripeSessionRequest(`{"plan":"pro"}`))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status %d, want 503", name, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "not configured") {
			t.Fatalf("%s: body %q", name, rec.Body.String())
		}
	}
}

// Only Pro is purchasable through this endpoint; every other plan is either
// free or negotiated, and a body that is not JSON never reaches Stripe.
func TestCheckoutRejectsAnythingButPro(t *testing.T) {
	stubStripe(t, func(w http.ResponseWriter, r *http.Request) {})
	t.Setenv("STRIPE_PRICE_PRO", "price_pro")

	rec := httptest.NewRecorder()
	handleCreateCheckoutSession()(rec, stripeSessionRequest(`{`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleCreateCheckoutSession()(rec, stripeSessionRequest(`{"plan":"enterprise"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}

	// A session with no org has nothing to bill.
	rec = httptest.NewRecorder()
	handleCreateCheckoutSession()(rec, httptest.NewRequest("POST", "/x", strings.NewReader(`{"plan":"pro"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

// Without a configured price there is nothing to put in the cart; failing loudly
// beats sending Stripe an empty line item.
func TestCheckoutRequiresAConfiguredPrice(t *testing.T) {
	stubStripe(t, func(w http.ResponseWriter, r *http.Request) {})
	t.Setenv("STRIPE_PRICE_PRO", "")
	t.Setenv("STRIPE_PRO_PRICE_ID", "")

	rec := httptest.NewRecorder()
	handleCreateCheckoutSession()(rec, stripeSessionRequest(`{"plan":"pro"}`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

// The checkout session must carry the org id back to us, or the webhook cannot
// tell which organization just paid.
func TestCheckoutCarriesTheOrganizationBackToTheWebhook(t *testing.T) {
	seen := stubStripe(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"cs_1","url":"https://checkout.stripe.com/cs_1"}`))
	})
	t.Setenv("STRIPE_PRICE_PRO", "price_pro")
	t.Setenv("FRONTEND_URL", "https://app.example.com")

	rec := httptest.NewRecorder()
	handleCreateCheckoutSession()(rec, stripeSessionRequest(`{"plan":"pro"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "checkout.stripe.com") {
		t.Fatalf("the caller needs the redirect URL: %s", rec.Body.String())
	}
	if len(*seen) == 0 {
		t.Fatal("no request reached Stripe")
	}
	form := (*seen)[0]
	if form.Get("client_reference_id") != "1" || form.Get("metadata[organization_id]") != "1" {
		t.Fatalf("the org is not carried on the session: %v", form)
	}
	if !strings.HasPrefix(form.Get("success_url"), "https://app.example.com/organizations/acme/settings") {
		t.Fatalf("success_url %q", form.Get("success_url"))
	}
}

// A Stripe outage during checkout is our 500, and the caller gets no URL.
func TestCheckoutReportsAStripeFailure(t *testing.T) {
	stubStripe(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	})
	t.Setenv("STRIPE_PRICE_PRO", "price_pro")

	rec := httptest.NewRecorder()
	handleCreateCheckoutSession()(rec, stripeSessionRequest(`{"plan":"pro"}`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

// A custom plan is the Pro base plus only the add-ons that exceed it: asking for
// the baseline must not add a zero-quantity line item.
func TestCustomCheckoutOnlyBillsWhatExceedsTheBaseTier(t *testing.T) {
	seen := stubStripe(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"cs_2","url":"https://checkout.stripe.com/cs_2"}`))
	})
	t.Setenv("STRIPE_PRICE_PRO", "price_pro")
	t.Setenv("STRIPE_PRICE_EXTRA_HOST", "price_host")
	t.Setenv("STRIPE_PRICE_EXTRA_SERVICE", "price_service")
	t.Setenv("STRIPE_PRICE_EXTRA_SDK", "price_sdk")
	t.Setenv("STRIPE_PRICE_RETENTION_90D", "price_ret90")

	body := `{"hosts":25,"services":50,"sdk_services":10,"retention_days":90}`
	rec := httptest.NewRecorder()
	handleCreateCustomCheckoutSession()(rec, stripeSessionRequest(body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	form := (*seen)[0]
	prices := []string{form.Get("line_items[0][price]"), form.Get("line_items[1][price]"), form.Get("line_items[2][price]")}
	if prices[0] != "price_pro" {
		t.Fatalf("the base tier must come first: %v", prices)
	}
	if prices[1] != "price_host" || form.Get("line_items[1][quantity]") != "15" {
		t.Fatalf("15 hosts past the base tier were not billed: %v", form)
	}
	if prices[2] != "price_ret90" {
		t.Fatalf("retention is a flat add-on and must be the only other line: %v", form)
	}
	if form.Get("metadata[plan]") != "custom" {
		t.Fatalf("metadata %v", form)
	}
}

func TestCustomCheckoutValidatesItsInputs(t *testing.T) {
	stubStripe(t, func(w http.ResponseWriter, r *http.Request) {})

	t.Setenv("STRIPE_PRICE_PRO", "price_pro")
	rec := httptest.NewRecorder()
	handleCreateCustomCheckoutSession()(rec, stripeSessionRequest(`{`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleCreateCustomCheckoutSession()(rec, httptest.NewRequest("POST", "/x", strings.NewReader(`{}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no org: status %d", rec.Code)
	}

	t.Setenv("STRIPE_PRICE_PRO", "")
	rec = httptest.NewRecorder()
	handleCreateCustomCheckoutSession()(rec, stripeSessionRequest(`{"hosts":25}`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("no base price: status %d", rec.Code)
	}
}

// The pricing calculator pre-fills its sliders from the live subscription: the
// baseline plus whatever was purchased on top.
func TestGetSubscriptionDerivesQuantitiesFromTheLineItems(t *testing.T) {
	stubStripe(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"sub_1","items":{"data":[
			{"id":"si_1","quantity":15,"price":{"id":"price_host"}},
			{"id":"si_2","quantity":20,"price":{"id":"price_service"}},
			{"id":"si_3","quantity":5,"price":{"id":"price_sdk"}},
			{"id":"si_4","quantity":1,"price":{"id":"price_ret365"}},
			{"id":"si_5","quantity":1,"price":{"id":"price_pro"}}
		]}}`))
	})
	t.Setenv("STRIPE_PRICE_EXTRA_HOST", "price_host")
	t.Setenv("STRIPE_PRICE_EXTRA_SERVICE", "price_service")
	t.Setenv("STRIPE_PRICE_EXTRA_SDK", "price_sdk")
	t.Setenv("STRIPE_PRICE_RETENTION_365D", "price_ret365")

	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("SELECT stripe_subscription_id FROM subscriptions").
		WillReturnRows(sqlmock.NewRows([]string{"stripe_subscription_id"}).AddRow("sub_1"))

	rec := httptest.NewRecorder()
	handleGetSubscription(db)(rec, stripeSessionRequest(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]int
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	want := map[string]int{"hosts": 25, "services": 70, "sdk_services": 15, "retention_days": 365}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %d, want %d (%v)", k, got[k], v, got)
		}
	}
}

// An org that never subscribed has no quantities to show, and that is a client
// state the settings page renders, not a server failure.
func TestGetSubscriptionReportsAMissingSubscription(t *testing.T) {
	stubStripe(t, func(w http.ResponseWriter, r *http.Request) {})
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT stripe_subscription_id FROM subscriptions").WillReturnError(errNoRows())

	rec := httptest.NewRecorder()
	handleGetSubscription(db)(rec, stripeSessionRequest(""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}

	// A session with no org cannot be resolved to a subscription at all.
	rec = httptest.NewRecorder()
	handleGetSubscription(db)(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

// Raising the sliders on an existing subscription changes its add-on
// quantities, moving a Pro subscriber to custom; the tiers not chosen are
// removed rather than left billing.
func TestUpdateSubscriptionAdjustsTheManagedAddons(t *testing.T) {
	seen := stubStripe(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"sub_1","items":{"data":[
			{"id":"si_host","quantity":5,"price":{"id":"price_host"}},
			{"id":"si_ret60","quantity":1,"price":{"id":"price_ret60"}}
		]}}`))
	})
	t.Setenv("STRIPE_PRICE_EXTRA_HOST", "price_host")
	t.Setenv("STRIPE_PRICE_EXTRA_SERVICE", "price_service")
	t.Setenv("STRIPE_PRICE_EXTRA_SDK", "")
	t.Setenv("STRIPE_PRICE_RETENTION_60D", "price_ret60")
	t.Setenv("STRIPE_PRICE_RETENTION_90D", "price_ret90")

	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT stripe_subscription_id FROM subscriptions").
		WillReturnRows(sqlmock.NewRows([]string{"stripe_subscription_id"}).AddRow("sub_1"))
	mock.ExpectExec("UPDATE organizations SET plan").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE organizations").WillReturnResult(sqlmock.NewResult(0, 1))

	body := `{"hosts":30,"services":50,"sdk_services":10,"retention_days":90}`
	rec := httptest.NewRecorder()
	handleUpdateSubscription(db)(rec, stripeSessionRequest(body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "updated") {
		t.Fatalf("body %q", rec.Body.String())
	}

	// The update call is the one carrying items; the reads carry none.
	var update url.Values
	for _, form := range *seen {
		if form.Get("proration_behavior") != "" {
			update = form
		}
	}
	if update == nil {
		t.Fatalf("no subscription update was sent: %v", *seen)
	}
	flat := update.Encode()
	if !strings.Contains(flat, "si_host") || !strings.Contains(flat, "20") {
		t.Fatalf("the host quantity was not raised: %s", flat)
	}
	if !strings.Contains(flat, "si_ret60") || !strings.Contains(flat, "deleted") {
		t.Fatalf("the abandoned retention tier is still billing: %s", flat)
	}
}

// Asking for exactly what is already there must not send Stripe an empty
// update, which it would reject.
func TestUpdateSubscriptionReportsNothingToChange(t *testing.T) {
	stubStripe(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"sub_1","items":{"data":[]}}`))
	})
	t.Setenv("STRIPE_PRICE_EXTRA_HOST", "")
	t.Setenv("STRIPE_PRICE_EXTRA_SERVICE", "")
	t.Setenv("STRIPE_PRICE_EXTRA_SDK", "")
	t.Setenv("STRIPE_PRICE_RETENTION_60D", "")
	t.Setenv("STRIPE_PRICE_RETENTION_90D", "")
	t.Setenv("STRIPE_PRICE_RETENTION_365D", "")

	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectQuery("SELECT stripe_subscription_id FROM subscriptions").
		WillReturnRows(sqlmock.NewRows([]string{"stripe_subscription_id"}).AddRow("sub_1"))

	rec := httptest.NewRecorder()
	handleUpdateSubscription(db)(rec, stripeSessionRequest(`{"hosts":10,"services":50,"sdk_services":10,"retention_days":30}`))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "unchanged") {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
}

// There is nothing to modify before a first subscription exists; the settings
// page sends the customer to checkout instead.
func TestUpdateSubscriptionRequiresAnExistingSubscription(t *testing.T) {
	stubStripe(t, func(w http.ResponseWriter, r *http.Request) {})
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT stripe_subscription_id FROM subscriptions").WillReturnError(errNoRows())

	rec := httptest.NewRecorder()
	handleUpdateSubscription(db)(rec, stripeSessionRequest(`{"hosts":25}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleUpdateSubscription(db)(rec, stripeSessionRequest(`{`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleUpdateSubscription(db)(rec, httptest.NewRequest("POST", "/x", strings.NewReader(`{}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no org: status %d", rec.Code)
	}
}

// The portal is where a customer cancels; it needs the Stripe customer we
// recorded at checkout.
func TestBillingPortalNeedsAKnownCustomer(t *testing.T) {
	stubStripe(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"bps_1","url":"https://billing.stripe.com/bps_1"}`))
	})
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	mock.ExpectQuery("SELECT stripe_customer_id FROM subscriptions").WillReturnError(errNoRows())
	rec := httptest.NewRecorder()
	handleCreateBillingPortal(db)(rec, stripeSessionRequest(""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}

	mock.ExpectQuery("SELECT stripe_customer_id FROM subscriptions").
		WillReturnRows(sqlmock.NewRows([]string{"stripe_customer_id"}).AddRow("cus_1"))
	rec = httptest.NewRecorder()
	handleCreateBillingPortal(db)(rec, stripeSessionRequest(""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "billing.stripe.com") {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
}

// signedStripeEvent builds the signature header Stripe sends, so the webhook can
// be exercised through its real verification path.
func signedStripeEvent(payload, secret string) string {
	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.%s", ts, payload)))
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

// The webhook is a public endpoint that grants paid plans: an unsigned or
// wrongly signed payload must never be acted on.
func TestWebhookRefusesAnUnsignedPayload(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	t.Setenv("STRIPE_WEBHOOK_SECRET", "")
	rec := httptest.NewRecorder()
	handleStripeWebhook(db)(rec, httptest.NewRequest("POST", "/x", strings.NewReader(`{}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unconfigured: status %d, want 500", rec.Code)
	}

	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_test")
	req := httptest.NewRequest("POST", "/x", strings.NewReader(`{"type":"checkout.session.completed"}`))
	req.Header.Set("Stripe-Signature", "t=1,v1=deadbeef")
	rec = httptest.NewRecorder()
	handleStripeWebhook(db)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad signature: status %d, want 400", rec.Code)
	}
}

// stripeEvent wraps an object in the envelope Stripe actually posts, signs it,
// and returns the request. The object is nested under data.object and carries
// its own "object" type string — the shape the parser has to handle.
func stripeEvent(t *testing.T, eventType, object string) *http.Request {
	t.Helper()
	payload := fmt.Sprintf(`{"id":"evt_1","object":"event","api_version":%q,"type":%q,"data":{"object":%s}}`,
		stripe.APIVersion, eventType, object)
	req := httptest.NewRequest("POST", "/api/v1/webhooks/stripe", strings.NewReader(payload))
	req.Header.Set("Stripe-Signature", signedStripeEvent(payload, "whsec_test"))
	return req
}

// A completed checkout is what actually grants the plan: the subscription row
// and the org's plan both have to move, and the trial deadline is cleared. The
// session is read straight out of data.object, which carries its own "object"
// type string — reading it as a nested wrapper is what silently granted nothing.
func TestWebhookGrantsTheProPlanOnCheckout(t *testing.T) {
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_test")
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectExec("INSERT INTO subscriptions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE organizations SET plan").
		WithArgs("pro", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	handleStripeWebhook(db)(rec, stripeEvent(t, "checkout.session.completed",
		`{"id":"cs_1","object":"checkout.session","client_reference_id":"7","customer":"cus_1",`+
			`"subscription":"sub_1","metadata":{"organization_slug":"acme","plan":"pro"}}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the plan was not granted: %v", err)
	}
}

// The org may only be carried in the metadata (a session created without a
// client reference), and that path has to work too.
func TestWebhookFallsBackToTheMetadataOrg(t *testing.T) {
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_test")
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectExec("INSERT INTO subscriptions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE organizations SET plan").
		WithArgs("custom", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	handleStripeWebhook(db)(rec, stripeEvent(t, "checkout.session.completed",
		`{"id":"cs_1","object":"checkout.session","customer":"cus_1","subscription":"",`+
			`"metadata":{"organization_id":"7","organization_slug":"acme","plan":"custom"}}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the custom plan was not granted: %v", err)
	}
}

// A checkout with no organization anywhere on it cannot be attributed, and
// acting on it would upgrade the wrong org.
func TestWebhookIgnoresAnUnattributableSession(t *testing.T) {
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_test")
	db, mock, _ := sqlmock.New()
	defer db.Close()

	rec := httptest.NewRecorder()
	handleStripeWebhook(db)(rec, stripeEvent(t, "checkout.session.completed",
		`{"id":"cs_1","object":"checkout.session","customer":"cus_1"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("something was written for an unattributable checkout: %v", err)
	}
}

// A malformed object is dropped rather than acted on with zero values.
func TestCheckoutCompletedIgnoresAnUnparseableObject(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	handleCheckoutCompleted(db, []byte(`not json`))
	handleSubscriptionUpdated(db, []byte(`not json`))
	handleSubscriptionDeleted(db, []byte(`not json`))

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("an unparseable event touched the database: %v", err)
	}
}

// A subscription carrying a paid add-on is a custom plan, not Pro: billing and
// the enforced limits have to agree.
func TestWebhookPromotesToCustomWhenAnAddonIsBilled(t *testing.T) {
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_test")
	t.Setenv("STRIPE_PRICE_EXTRA_HOST", "price_host")
	t.Setenv("STRIPE_PRICE_EXTRA_SERVICE", "")
	t.Setenv("STRIPE_PRICE_EXTRA_SDK", "")
	t.Setenv("STRIPE_PRICE_RETENTION_60D", "")
	t.Setenv("STRIPE_PRICE_RETENTION_90D", "")
	t.Setenv("STRIPE_PRICE_RETENTION_365D", "")
	stubStripe(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"sub_1","items":{"data":[{"id":"si_1","quantity":15,"price":{"id":"price_host"}}]}}`))
	})

	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT organization_id, organization_slug FROM subscriptions").
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "organization_slug"}).AddRow(int64(7), "acme"))
	mock.ExpectExec("INSERT INTO subscriptions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE organizations SET plan").
		WithArgs("custom", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SET custom_max_hosts").
		WithArgs(25, 50, 10, 30, int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	handleStripeWebhook(db)(rec, stripeEvent(t, "customer.subscription.updated",
		`{"id":"sub_1","object":"subscription","status":"active","customer":"cus_1",`+
			`"cancel_at_period_end":false,"items":{"object":"list","data":[{"id":"si_1","object":"subscription_item",`+
			`"quantity":15,"price":{"id":"price_host","object":"price"}}]}}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the purchased caps were not synced: %v", err)
	}
}

// A subscription set to cancel at period end is no longer a paid plan for the
// purposes of enforcement.
func TestWebhookDowngradesACancellingSubscription(t *testing.T) {
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_test")
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT organization_id, organization_slug FROM subscriptions").
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "organization_slug"}).AddRow(int64(7), "acme"))
	mock.ExpectExec("INSERT INTO subscriptions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE organizations SET plan").
		WithArgs("free", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	handleStripeWebhook(db)(rec, stripeEvent(t, "customer.subscription.updated",
		`{"id":"sub_1","object":"subscription","status":"active","customer":"cus_1",`+
			`"cancel_at_period_end":true,"items":{"object":"list","data":[]}}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the org kept a paid plan: %v", err)
	}
}

// An event naming a subscription we never recorded cannot be acted on.
func TestWebhookIgnoresAnUnknownSubscription(t *testing.T) {
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_test")
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT organization_id, organization_slug FROM subscriptions").WillReturnError(errNoRows())
	mock.ExpectQuery("SELECT organization_id, organization_slug FROM subscriptions").WillReturnError(errNoRows())

	for _, eventType := range []string{"customer.subscription.updated", "customer.subscription.deleted"} {
		rec := httptest.NewRecorder()
		handleStripeWebhook(db)(rec, stripeEvent(t, eventType,
			`{"id":"sub_ghost","object":"subscription","status":"active"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", eventType, rec.Code)
		}
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("an unknown subscription was acted on: %v", err)
	}
}

// A deleted subscription drops the org back to free; leaving it on pro would
// hand out paid capacity for free.
func TestWebhookDowngradesOnSubscriptionDeleted(t *testing.T) {
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_test")
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT organization_id, organization_slug FROM subscriptions").
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "organization_slug"}).AddRow(int64(7), "acme"))
	mock.ExpectExec("INSERT INTO subscriptions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE organizations SET plan").
		WithArgs("free", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	handleStripeWebhook(db)(rec, stripeEvent(t, "customer.subscription.deleted",
		`{"id":"sub_1","object":"subscription","customer":"cus_1","status":"canceled"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the org was not downgraded: %v", err)
	}
}

// A subscription row that cannot be written must stop the flow: granting the
// plan without recording the subscription would leave billing unreconcilable.
func TestPlanIsNotGrantedWhenTheSubscriptionRowFails(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectExec("INSERT INTO subscriptions").WillReturnError(errors.New("db down"))

	handleCheckoutCompleted(db, []byte(`{"id":"cs_1","object":"checkout.session","client_reference_id":"7","customer":"cus_1"}`))

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("%v", err)
	}
}

// syncCustomLimits derives the enforced caps from what Stripe says is billed,
// so the UI and the quota checks agree with the invoice.
func TestSyncCustomLimitsDerivesTheCapsFromTheSubscription(t *testing.T) {
	t.Setenv("STRIPE_PRICE_EXTRA_HOST", "price_host")
	t.Setenv("STRIPE_PRICE_EXTRA_SERVICE", "price_service")
	t.Setenv("STRIPE_PRICE_EXTRA_SDK", "price_sdk")
	t.Setenv("STRIPE_PRICE_RETENTION_60D", "price_ret60")
	t.Setenv("STRIPE_PRICE_RETENTION_90D", "price_ret90")
	t.Setenv("STRIPE_PRICE_RETENTION_365D", "price_ret365")
	stubStripe(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"sub_1","items":{"data":[
			{"id":"si_1","quantity":15,"price":{"id":"price_host"}},
			{"id":"si_2","quantity":20,"price":{"id":"price_service"}},
			{"id":"si_3","quantity":5,"price":{"id":"price_sdk"}},
			{"id":"si_4","quantity":1,"price":{"id":"price_ret90"}},
			{"id":"si_5","quantity":1,"price":{"id":"price_pro"}},
			{"id":"si_6","quantity":1}
		]}}`))
	})

	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectExec("SET custom_max_hosts").
		WithArgs(25, 70, 15, 90, int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))

	syncCustomLimits(db, 7, "sub_1")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the caps do not match what is billed: %v", err)
	}
}

// A Stripe read failure must leave the stored caps alone rather than reset them
// to the baseline and lock the customer out of what they pay for.
func TestSyncCustomLimitsLeavesTheCapsAloneWhenStripeFails(t *testing.T) {
	stubStripe(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	})

	db, mock, _ := sqlmock.New()
	defer db.Close()

	syncCustomLimits(db, 7, "sub_1")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the caps were rewritten from an unread subscription: %v", err)
	}
}

// Stripe sends far more event types than we act on; an unhandled one is
// acknowledged so Stripe stops retrying it.
func TestWebhookAcknowledgesEventsItIgnores(t *testing.T) {
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_test")
	db, mock, _ := sqlmock.New()
	defer db.Close()

	payload := `{"api_version":"` + stripe.APIVersion + `","type":"invoice.paid","data":{"object":{}}}`
	req := httptest.NewRequest("POST", "/x", strings.NewReader(payload))
	req.Header.Set("Stripe-Signature", signedStripeEvent(payload, "whsec_test"))
	rec := httptest.NewRecorder()
	handleStripeWebhook(db)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("an ignored event touched the database: %v", err)
	}
}

// Any billing outcome closes the trial: a leftover deadline would keep the org
// in trial mode after it paid or cancelled.
func TestSetOrgPlanClearsTheTrialAndRejectsUnknownPlans(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	mock.ExpectExec("SET plan = .*trial_ends_at = NULL").WithArgs("pro", int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := setOrgPlan(db, 7, "pro"); err != nil {
		t.Fatalf("set plan: %v", err)
	}

	// An unknown plan name falls back to free rather than being written as-is.
	mock.ExpectExec("SET plan").WithArgs("free", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := setOrgPlan(db, 7, "platinum"); err != nil {
		t.Fatalf("set plan: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("%v", err)
	}
}

// syncCustomLimits is what makes a custom plan enforceable; without a
// subscription id there is nothing to read and it must not write defaults.
func TestSyncCustomLimitsNeedsASubscription(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	syncCustomLimits(db, 7, "")
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("an empty subscription id caused a write: %v", err)
	}
}

func TestRetentionAndManagedAddonPrices(t *testing.T) {
	t.Setenv("STRIPE_PRICE_RETENTION_60D", "price_ret60")
	t.Setenv("STRIPE_PRICE_RETENTION_90D", "price_ret90")
	t.Setenv("STRIPE_PRICE_RETENTION_365D", "price_ret365")
	t.Setenv("STRIPE_PRICE_EXTRA_HOST", "price_host")
	t.Setenv("STRIPE_PRICE_EXTRA_SERVICE", "")
	t.Setenv("STRIPE_PRICE_EXTRA_SDK", "")

	if got := retentionPriceID(60); got != "price_ret60" {
		t.Fatalf("got %q", got)
	}
	if got := retentionPriceID(90); got != "price_ret90" {
		t.Fatalf("got %q", got)
	}
	if got := retentionPriceID(365); got != "price_ret365" {
		t.Fatalf("got %q", got)
	}
	// 30 days is the baseline everyone gets: it is not a billable add-on.
	if got := retentionPriceID(30); got != "" {
		t.Fatalf("got %q", got)
	}

	managed := managedAddonPriceIDs()
	if !managed["price_host"] || !managed["price_ret365"] {
		t.Fatalf("configured add-ons must be managed: %v", managed)
	}
	if len(managed) != 4 {
		t.Fatalf("an unconfigured price must not be managed: %v", managed)
	}
}

// The Pro price falls back to the legacy variable name, so an instance
// configured before the rename keeps selling.
func TestStripeProPriceIDFallsBackToTheLegacyVariable(t *testing.T) {
	t.Setenv("STRIPE_PRICE_PRO", "")
	t.Setenv("STRIPE_PRO_PRICE_ID", "price_legacy")
	if got := stripeProPriceID(); got != "price_legacy" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("STRIPE_PRICE_PRO", "price_new")
	if got := stripeProPriceID(); got != "price_new" {
		t.Fatalf("got %q", got)
	}
}

// The redirect URLs go to the first configured origin: FRONTEND_URL doubles as
// the CORS allowlist and may carry several.
func TestFrontendBaseURLTakesTheFirstOrigin(t *testing.T) {
	t.Setenv("FRONTEND_URL", "")
	if got := frontendBaseURL(); got != "http://localhost:3000" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("FRONTEND_URL", " https://app.example.com/ , https://other.example.com ")
	if got := frontendBaseURL(); got != "https://app.example.com" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("FRONTEND_URL", " , ")
	if got := frontendBaseURL(); got != "http://localhost:3000" {
		t.Fatalf("a list of blanks must fall back: %q", got)
	}
}

func TestMaxInt(t *testing.T) {
	if maxInt(3, 7) != 7 || maxInt(7, 3) != 7 || maxInt(-2, 0) != 0 {
		t.Fatal("maxInt does not return the larger value")
	}
}
