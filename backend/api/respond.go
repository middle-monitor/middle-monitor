package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	middlemonitor "github.com/middle-monitor/sdk-go"
)

// selfMonitorSDK gates every SDK call: the Go SDK auto-initializes from env on
// first use, so an unguarded call on a dev boot would point the exporter at
// the default production endpoint. Same opt-in as cmd/api's InitSimple.
var selfMonitorSDK = os.Getenv("MIDDLE_MONITOR_TOKEN") != ""

// respondError writes a JSON error body ({"error": "..."}) with the given status.
//
// For 5xx the underlying error is logged server-side and the client receives a
// generic message, so internal details (SQL errors, file paths, driver text)
// never leak. For 4xx the error text is returned to the caller, since those are
// actionable validation messages the client is meant to see.
//
// The JSON shape matches what the frontend reads (err.response.data.error) and
// what the SDKs parse via getMessageFromExceptionBody.
func respondError(w http.ResponseWriter, status int, err error) {
	msg := http.StatusText(status)
	if status < 500 && err != nil {
		msg = err.Error()
	} else if err != nil {
		slog.Error("request failed", "status", status, "status_text", http.StatusText(status), "error", err)
		// Self-monitoring: report our own 5xx to Middle-Monitor through the
		// public Go SDK, exactly like any instrumented client application. The
		// log record carries the same cause to the Logs view; ERROR is the only
		// level the SDK samples by default.
		if selfMonitorSDK {
			middlemonitor.ReportError(err)
			middlemonitor.Log(context.Background(), middlemonitor.LogLevelERROR, err.Error(), map[string]string{
				"http.status_code": strconv.Itoa(status),
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// respondJSON writes a successful JSON body. Errors go through respondError,
// which sets the status and hides 5xx internals.
func respondJSON(w http.ResponseWriter, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// respondConflict writes a 409 naming the row that already holds the natural
// key, so a converging tool can turn a failed create into a get instead of
// giving up or duplicating.
func respondConflict(w http.ResponseWriter, err error, existingID int64) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	body := map[string]interface{}{"error": err.Error(), "code": "already_exists"}
	if existingID != 0 {
		body["id"] = existingID
	}
	_ = json.NewEncoder(w).Encode(body)
}

// respondPlanLimit writes a 400 for a plan-quota error, adding a machine-readable
// "code": "plan_limit" so the frontend can show an upgrade CTA instead of a plain
// error. The error text still states the exact limit that was hit.
func respondPlanLimit(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error(), "code": "plan_limit"})
}
