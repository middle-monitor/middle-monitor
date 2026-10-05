package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestListDeliveriesRejectsABadChannelID(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	rec := httptest.NewRecorder()
	handleListDeliveries(db)(rec, orgRequest("GET", "/x", "", map[string]string{"id": "abc"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

func TestListDeliveriesSurfacesALookupFailure(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(".*").WillReturnError(errors.New("down"))

	rec := httptest.NewRecorder()
	handleListDeliveries(db)(rec, orgRequest("GET", "/x", "", map[string]string{"id": "4"}))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}

func TestReplayDeliveryRejectsBadIDs(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	rec := httptest.NewRecorder()
	handleReplayDelivery(db)(rec, orgRequest("POST", "/x", "", map[string]string{"id": "abc", "deliveryID": "1"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("channel id: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handleReplayDelivery(db)(rec, orgRequest("POST", "/x", "", map[string]string{"id": "4", "deliveryID": "abc"}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delivery id: status %d", rec.Code)
	}
}

// A replay the receiver rejects is the receiver's problem: the caller needs to
// tell that apart from "this delivery does not exist".
func TestReplayDeliveryDistinguishesMissingFromFailed(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(".*").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	rec := httptest.NewRecorder()
	handleReplayDelivery(db)(rec, orgRequest("POST", "/x", "", map[string]string{"id": "4", "deliveryID": "9"}))
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 404 or 422", rec.Code)
	}
}
