package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

type UnsubscribeRequest struct {
	Email string `json:"email"`
	Token string `json:"token"`
}

// unsubscribeToken must stay byte-identical to the one the outreach sender puts
// in the link, otherwise a valid opt-out is rejected.
func unsubscribeToken(email, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(email))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

func handleUnsubscribe(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)

		// Mail providers implementing RFC 8058 one-click POST straight to this URL
		// with the parameters in the query string and no JSON body, so query wins
		// and the body is only read when it is absent.
		var req UnsubscribeRequest
		req.Email = r.URL.Query().Get("e")
		req.Token = r.URL.Query().Get("t")
		if req.Email == "" || req.Token == "" {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
				return
			}
		}

		if strings.TrimSpace(req.Email) == "" || req.Token == "" {
			respondError(w, http.StatusBadRequest, ErrUnsubscribeFieldsMissing)
			return
		}

		// Without a secret the token proves nothing, and anyone could suppress the
		// whole prospect list. Refuse rather than accept blindly.
		secret := os.Getenv("UNSUB_SECRET")
		if secret == "" {
			slog.Error("unsubscribe secret not configured")
			respondError(w, http.StatusInternalServerError, ErrUnsubscribeNotConfigured)
			return
		}

		// The sender signs the address as it stands in its list, casing included,
		// so the signature is checked on the value as received.
		if !hmac.Equal([]byte(req.Token), []byte(unsubscribeToken(req.Email, secret))) {
			respondError(w, http.StatusBadRequest, ErrUnsubscribeTokenInvalid)
			return
		}

		email := strings.ToLower(strings.TrimSpace(req.Email))

		_, err := db.Exec(
			`INSERT INTO email_suppressions (email, source) VALUES ($1, 'unsubscribe_link')
			 ON CONFLICT (email) DO NOTHING`, email)
		if err != nil {
			slog.Error("unsubscribe insert failed", "error", err)
			respondError(w, http.StatusInternalServerError, ErrUnsubscribeStore)
			return
		}

		slog.Info("email suppressed", "email", email)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}
}
