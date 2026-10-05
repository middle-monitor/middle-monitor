package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"middle-monitor/backend/services"
)

// French renderings of the series query mistakes a user can type. Services stay
// English (see services/errors.go); only the response is localized.
var seriesErrorsFR = []struct {
	err error
	msg string
}{
	{services.ErrExpressionQueries, "entre 1 et 5 requêtes sont nécessaires"},
	{services.ErrExpressionRef, "les références de requête doivent être des lettres majuscules distinctes"},
	{services.ErrExpressionTooLong, "expression trop longue"},
	{services.ErrExpressionSyntax, "expression invalide"},
	{services.ErrExpressionUnknownRef, "l'expression référence une requête inconnue"},
	{services.ErrExpressionFunction, "fonction inconnue"},
	{services.ErrExpressionScalar, "l'expression doit référencer au moins une requête"},
	{services.ErrExpressionNoMatch, "aucune série ne partage les mêmes labels des deux côtés"},
	{services.ErrSeriesTooMany, "trop de séries pour rate, affinez la requête avec des filtres"},
	{services.ErrSeriesMetricRequired, "le nom de la métrique est obligatoire"},
	{services.ErrSeriesAggregation, "agrégation non prise en charge"},
	{services.ErrSeriesRange, "la fin doit être après le début"},
	{services.ErrSeriesStep, "le pas doit être un nombre positif de secondes"},
}

// LocalizedError carries a translated message while still matching its cause.
type LocalizedError struct {
	Message string
	Err     error
}

func (e *LocalizedError) Error() string { return e.Message }

func (e *LocalizedError) Unwrap() error { return e.Err }

// localizeSeriesError translates for a French UI only: API clients that send no
// Accept-Language keep the English message they may already match on.
func localizeSeriesError(r *http.Request, err error) error {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Accept-Language"))), "fr") {
		return err
	}
	for _, entry := range seriesErrorsFR {
		if !errors.Is(err, entry.err) {
			continue
		}
		msg := entry.msg
		var located *services.ExpressionError
		if errors.As(err, &located) {
			if located.Token != "" {
				msg = fmt.Sprintf("%s : %s", msg, located.Token)
			} else {
				msg = fmt.Sprintf("%s (position %d)", msg, located.Position)
			}
		}
		return &LocalizedError{Message: msg, Err: err}
	}
	return err
}
