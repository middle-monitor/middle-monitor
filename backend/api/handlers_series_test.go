package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// Without OpenSearch the series endpoints degrade to empty answers rather than
// failing: the rest of the dashboard still works.
func TestSeriesEndpointsDegradeWithoutOpenSearch(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	cases := []struct {
		name    string
		handler http.HandlerFunc
		target  string
		key     string
	}{
		{"names", handleListMetricNames(db), "/x", "names"},
		{"label keys", handleListMetricLabelKeys(db), "/x", "keys"},
		{"label values", handleListMetricLabelValues(db), "/x?key=host", "values"},
		{"series", handleQueryMetricSeries(db), "/x?metric=cpu", "series"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		c.handler(rec, orgRequest("GET", c.target, "", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", c.name, rec.Code)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: decode: %v", c.name, err)
		}
		if string(body[c.key]) != "[]" {
			t.Fatalf("%s: %s = %s, want an empty list", c.name, c.key, body[c.key])
		}
	}
}

// The label-value endpoint is meaningless without a key, and a bad time range
// or step is a client mistake, not a backend failure.
func TestSeriesEndpointsRejectMalformedQueries(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	cases := []struct {
		name    string
		handler http.HandlerFunc
		target  string
	}{
		{"missing label key", handleListMetricLabelValues(db), "/x"},
		{"bad range on series", handleQueryMetricSeries(db), "/x?start=yesterday"},
		{"bad filter", handleQueryMetricSeries(db), "/x?filter=nonsense"},
		{"bad step", handleQueryMetricSeries(db), "/x?metric=cpu&step=0"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		c.handler(rec, orgRequest("GET", c.target, "", nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400 (%s)", c.name, rec.Code, rec.Body.String())
		}
	}
}

// A host filter matching nothing yields an empty answer without ever reaching
// the search backend.
func TestSeriesEndpointsReturnNothingForAnEmptyHostScope(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 4; i++ {
		mock.ExpectQuery("SELECT name FROM hosts").WillReturnRows(sqlmock.NewRows([]string{"name"}))
	}

	handlers := []http.HandlerFunc{
		handleListMetricNames(db), handleListMetricLabelKeys(db),
		handleListMetricLabelValues(db), handleQueryMetricSeries(db),
	}
	for i, h := range handlers {
		rec := httptest.NewRecorder()
		h(rec, orgRequest("GET", "/x?host_id=4&key=host&metric=cpu", "", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("handler %d: status %d", i, rec.Code)
		}
	}
}

// The expression endpoint validates before the OpenSearch check: a deployment
// without search must still refuse a malformed query rather than answer [].
func TestSeriesExpressionRejectsMalformedRequests(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()
	previous := opensearch
	opensearch = nil
	t.Cleanup(func() { opensearch = previous })

	cases := []struct {
		name, target, body string
	}{
		{"not json", "/x", "{"},
		{"no query", "/x", `{"queries":[]}`},
		{"syntax", "/x", `{"queries":[{"ref":"A","metric":"m","aggregation":"avg"}],"expression":"$A +"}`},
		{"unknown ref", "/x", `{"queries":[{"ref":"A","metric":"m","aggregation":"avg"}],"expression":"$B * 2"}`},
		{"bad aggregation", "/x", `{"queries":[{"ref":"A","metric":"m","aggregation":"median"}]}`},
		{"bad step", "/x?step=0", `{"queries":[{"ref":"A","metric":"m","aggregation":"avg"}]}`},
		{"bad range", "/x?start=yesterday", `{"queries":[{"ref":"A","metric":"m","aggregation":"avg"}]}`},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		handleQueryMetricExpression(db)(rec, orgRequest("POST", c.target, c.body, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (%s)", c.name, rec.Code, rec.Body.String())
		}
	}
}

func TestSeriesExpressionDegradesWithoutOpenSearch(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()
	previous := opensearch
	opensearch = nil
	t.Cleanup(func() { opensearch = previous })

	rec := httptest.NewRecorder()
	body := `{"queries":[{"ref":"A","metric":"m","aggregation":"rate"}],"expression":"$A * 60"}`
	handleQueryMetricExpression(db)(rec, orgRequest("POST", "/x", body, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"series":[]`) {
		t.Fatalf("status %d body %s, want 200 with an empty list", rec.Code, rec.Body.String())
	}
}

// The French UI shows the reason inside a French sentence; an API client without
// Accept-Language keeps the English text it may match on.
func TestSeriesExpressionErrorsFollowTheRequestLanguage(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()
	previous := opensearch
	opensearch = nil
	t.Cleanup(func() { opensearch = previous })

	cases := []struct {
		lang, expression, want string
	}{
		{"fr", "$A +", "expression invalide (position 5)"},
		{"fr-FR", "$Z * 2", "l'expression référence une requête inconnue : $Z"},
		{"fr", "foo($A)", "fonction inconnue : foo"},
		{"", "$A +", "at position 5: invalid expression"},
		{"en", "$Z * 2", "$Z: expression references an unknown query"},
	}
	for _, c := range cases {
		body := `{"queries":[{"ref":"A","metric":"m"}],"expression":"` + c.expression + `"}`
		req := orgRequest("POST", "/x", body, nil)
		if c.lang != "" {
			req.Header.Set("Accept-Language", c.lang)
		}
		rec := httptest.NewRecorder()
		handleQueryMetricExpression(db)(rec, req)
		var got map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if rec.Code != http.StatusBadRequest || got["error"] != c.want {
			t.Errorf("%s %q: status %d error %q, want 400 %q", c.lang, c.expression, rec.Code, got["error"], c.want)
		}
	}
}
