package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/services"
)

// A personal token authenticates as its owner with MFAEnrollmentRequired unset,
// so minting one is a way out of the enrollment gate: the user gets back, with a
// key, exactly the org data the gate is holding them back from. Minting must
// therefore sit behind the same gate as the org routes.

// gatedCreate mirrors the wiring in SetupAPIRouter for POST /auth/api-tokens.
func gatedCreate(reached *bool) http.Handler {
	return middleware.RequireMFAEnrollment(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusCreated)
	}))
}

func requestAs(claims *services.JWTClaims) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/api-tokens", strings.NewReader(`{"name":"probe"}`))
	ctx := context.WithValue(req.Context(), middleware.ClaimsContextKey, claims)
	return httptest.NewRecorder(), req.WithContext(ctx)
}

func TestCreatePersonalToken_RefusedWhileEnrollmentIsOwed(t *testing.T) {
	reached := false
	rec, req := requestAs(&services.JWTClaims{UserID: 7, OrganizationID: 3, MFAEnrollmentRequired: true})

	gatedCreate(&reached).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusForbidden)
	}
	if reached {
		t.Error("the handler ran: a gated user was able to mint a token")
	}
}

func TestCreatePersonalToken_AllowedOnceEnrolled(t *testing.T) {
	reached := false
	rec, req := requestAs(&services.JWTClaims{UserID: 7, OrganizationID: 3, MFAEnrollmentRequired: false})

	gatedCreate(&reached).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated || !reached {
		t.Fatalf("an enrolled user must still mint tokens: got %d, reached=%v", rec.Code, reached)
	}
}
