package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// Without a valid token anyone could suppress the whole prospect list, so a
// wrong, missing or unsigned token must never reach the database.
func TestUnsubscribeRejectsAnUnprovenRequest(t *testing.T) {
	t.Setenv("UNSUB_SECRET", "s3cr3t")
	cases := map[string]string{
		"not json":      `{`,
		"missing token": `{"email":"contact@acme.fr"}`,
		"missing email": `{"token":"d7290b723c1f31e5"}`,
		"forged token":  `{"email":"contact@acme.fr","token":"0000000000000000"}`,
		"other address": `{"email":"someone@else.fr","token":"d7290b723c1f31e5"}`,
	}
	for name, body := range cases {
		rec := httptest.NewRecorder()
		handleUnsubscribe(nil)(rec, httptest.NewRequest("POST", "/api/v1/unsubscribe", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

// The sender signs the address as it stands in its list. A prospect stored with
// a capital must still be able to opt out, and the row written is the normalised
// address so the suppression list stays free of duplicates.
func TestUnsubscribeAcceptsAMixedCaseAddress(t *testing.T) {
	t.Setenv("UNSUB_SECRET", "s3cr3t")
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.ExpectExec("INSERT INTO email_suppressions").
		WithArgs("contact@acme.fr").
		WillReturnResult(sqlmock.NewResult(1, 1))

	token := unsubscribeToken("Contact@Acme.fr", "s3cr3t")
	rec := httptest.NewRecorder()
	handleUnsubscribe(db)(rec, httptest.NewRequest("POST", "/api/v1/unsubscribe?e=Contact@Acme.fr&t="+token, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

// An unconfigured secret makes every token verify against the empty string, so
// the endpoint has to refuse rather than accept whatever it is handed.
func TestUnsubscribeRefusesWhenTheSecretIsMissing(t *testing.T) {
	t.Setenv("UNSUB_SECRET", "")
	rec := httptest.NewRecorder()
	body := `{"email":"contact@acme.fr","token":"d7290b723c1f31e5"}`
	handleUnsubscribe(nil)(rec, httptest.NewRequest("POST", "/api/v1/unsubscribe", strings.NewReader(body)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
	}
}
