package services

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Enough for OpenSearch's error envelope; a larger body is not an explanation.
const maxErrorBodyBytes = 8 << 10

// newOpenSearchStatusError keeps the type and reason of a rejection. A bare
// status 400 hid a mapping conflict that dropped every http check result for months.
func newOpenSearchStatusError(op string, resp *http.Response) *OpenSearchStatusError {
	statusErr := &OpenSearchStatusError{Op: op, StatusCode: resp.StatusCode}
	var body struct {
		Error struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxErrorBodyBytes)).Decode(&body); err != nil {
		return statusErr
	}
	// The preview quotes the rejected value, which may be a customer's log or payload.
	reason, _, _ := strings.Cut(body.Error.Reason, ". Preview of field's value")
	switch {
	case body.Error.Type != "" && reason != "":
		statusErr.Reason = fmt.Sprintf("%s: %s", body.Error.Type, reason)
	case body.Error.Type != "":
		statusErr.Reason = body.Error.Type
	default:
		statusErr.Reason = reason
	}
	return statusErr
}
