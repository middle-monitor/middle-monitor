package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"middle-monitor/backend/middleware"
	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

// handleRegister handles user registration
func handleRegister(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req models.RegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		user, tokens, err := authService.Register(req)
		if err != nil {
			switch err {
			case services.ErrUserExists:
				respondError(w, http.StatusConflict, ErrEmailTaken)
			case services.ErrInvalidEmail:
				respondError(w, http.StatusBadRequest, ErrEmailInvalid)
			case services.ErrWeakPassword:
				respondError(w, http.StatusBadRequest, ErrPasswordWeak)
			default:
				respondError(w, http.StatusInternalServerError, err)
			}
			return
		}

		// New accounts must confirm their email before the dashboard unlocks.
		// Issue a verification token and email it. The welcome email is deferred
		// until verification succeeds (see handleVerifyEmail). Non-blocking,
		// failure is non-fatal — the user can resend from the app.
		if token, tokErr := authService.IssueEmailVerification(user.User.ID); tokErr == nil {
			go func() {
				if err := services.SendVerificationEmail(user.User.Email, user.User.Name, token); err != nil {
					slog.Error("verification email failed", "email", user.User.Email, "error", err)
				}
			}()
		} else {
			slog.Error("failed to issue verification token", "email", user.User.Email, "error", tokErr)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"user":   user,
			"tokens": tokens,
		})
	}
}

// handleLogin handles user authentication
func handleLogin(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req models.LoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			slog.Warn("login body decode failed", "error", err)
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		slog.Debug("login attempt", "email", req.Email)

		user, tokens, mfaChallenge, err := authService.Login(req)
		if err != nil {
			slog.Warn("login failed", "email", req.Email, "error", err)
			if err == services.ErrInvalidCredentials {
				respondError(w, http.StatusUnauthorized, ErrCredentialsInvalid)
			} else {
				respondError(w, http.StatusInternalServerError, err)
			}
			return
		}

		// Org enforces 2FA and the user is enrolled: stop here and hand back a
		// challenge token. The client must POST it with a code to /auth/login/mfa.
		if mfaChallenge != "" {
			slog.Info("login requires 2fa", "email", req.Email)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"mfa_required": true,
				"mfa_token":    mfaChallenge,
			})
			return
		}

		slog.Info("login successful", "email", req.Email, "user_id", user.User.ID)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"user":   user,
			"tokens": tokens,
		})
	}
}

// handleLoginMFA completes a 2FA login: it takes the challenge token issued by
// handleLogin plus a TOTP code (or recovery code) and returns session tokens.
func handleLoginMFA(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MFAToken string `json:"mfa_token"`
			Code     string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if req.MFAToken == "" || req.Code == "" {
			respondError(w, http.StatusBadRequest, ErrTokenOrCodeMissing)
			return
		}

		user, tokens, err := authService.CompleteMFALogin(req.MFAToken, req.Code)
		if err != nil {
			switch err {
			case services.ErrInvalidToken:
				respondError(w, http.StatusUnauthorized, ErrSessionExpired)
			case services.ErrInvalidMFACode:
				respondError(w, http.StatusUnauthorized, ErrAuthCodeInvalid)
			default:
				respondError(w, http.StatusInternalServerError, err)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"user":   user,
			"tokens": tokens,
		})
	}
}

// handleSetup2FA starts authenticator enrollment for the current user: it returns
// the secret, otpauth URL and a QR PNG. The secret is pending until handleEnable2FA
// confirms a code.
func handleSetup2FA(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaimsFromContext(r.Context())
		if claims == nil {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		secret, otpauthURL, qrPNG, err := authService.SetupTOTP(claims.UserID, claims.Email)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"secret":      secret,
			"otpauth_url": otpauthURL,
			"qr_png":      qrPNG,
		})
	}
}

// handleEnable2FA confirms enrollment by validating a code against the pending
// secret, then returns the one-time recovery codes (shown once).
func handleEnable2FA(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaimsFromContext(r.Context())
		if claims == nil {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		var req struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		codes, err := authService.EnableTOTP(claims.UserID, req.Code)
		if err != nil {
			switch err {
			case services.ErrInvalidMFACode:
				respondError(w, http.StatusBadRequest, ErrAuthCodeInvalid)
			case services.ErrTOTPNotPending:
				respondError(w, http.StatusBadRequest, ErrTwoFactorSetupMissing)
			default:
				respondError(w, http.StatusInternalServerError, err)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"recovery_codes": codes,
		})
	}
}

// handleDisable2FA turns off the current user's own two-factor authentication.
// Refused (403) when the org enforces 2FA.
func handleDisable2FA(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaimsFromContext(r.Context())
		if claims == nil {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		if err := authService.DisableTOTP(claims.UserID); err != nil {
			if err == services.ErrMFARequiredByOrg {
				respondError(w, http.StatusForbidden, err)
			} else {
				respondError(w, http.StatusInternalServerError, err)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "two-factor authentication disabled"})
	}
}

// handleGetPersonalTokens lists the current user's personal API tokens.
func handleGetPersonalTokens(authService *services.AuthService) http.HandlerFunc {
	keyService := services.NewAPIKeyService(authService.DB())
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaimsFromContext(r.Context())
		if claims == nil {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		keys, err := keyService.GetPersonalKeys(claims.UserID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(keys)
	}
}

// handleCreatePersonalToken creates a personal API token that authenticates as the
// current user (carrying their role).
func handleCreatePersonalToken(authService *services.AuthService) http.HandlerFunc {
	keyService := services.NewAPIKeyService(authService.DB())
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaimsFromContext(r.Context())
		if claims == nil {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		var body struct {
			Name      string     `json:"name"`
			ExpiresAt *time.Time `json:"expires_at,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			respondError(w, http.StatusBadRequest, ErrNameRequired)
			return
		}
		// Expiration is mandatory (no perpetual tokens), but there is no upper bound.
		if body.ExpiresAt == nil {
			respondError(w, http.StatusBadRequest, ErrExpirationRequired)
			return
		}
		if body.ExpiresAt.Before(time.Now()) {
			respondError(w, http.StatusBadRequest, ErrExpirationInPast)
			return
		}

		key, err := keyService.CreatePersonalKey(claims.OrganizationID, claims.UserID, body.Name, body.ExpiresAt)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(key)
	}
}

// handleDeletePersonalToken revokes one of the current user's personal API tokens.
func handleDeletePersonalToken(authService *services.AuthService) http.HandlerFunc {
	keyService := services.NewAPIKeyService(authService.DB())
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaimsFromContext(r.Context())
		if claims == nil {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		tokenID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrTokenIDInvalid)
			return
		}

		if err := keyService.DeletePersonalKey(tokenID, claims.UserID); err != nil {
			respondError(w, http.StatusNotFound, ErrTokenNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "token revoked"})
	}
}

// handleGetMe returns the current authenticated user with organization
func handleGetMe(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaimsFromContext(r.Context())
		if claims == nil {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		// Resolve the identity scoped to the active org carried by the JWT, falling
		// back to the home org (handles tokens minted before memberships existed).
		userWithOrg, err := authService.GetUserInOrg(claims.UserID, claims.OrganizationID)
		if err == services.ErrUserNotFound {
			userWithOrg, err = authService.GetUserByID(claims.UserID)
		}
		if err != nil {
			if err == services.ErrUserNotFound {
				respondError(w, http.StatusNotFound, ErrUserNotFound)
			} else {
				respondError(w, http.StatusInternalServerError, err)
			}
			return
		}

		// All orgs the user belongs to — drives the in-app org switcher.
		orgs, oErr := authService.GetUserOrganizations(claims.UserID)
		if oErr != nil {
			respondError(w, http.StatusInternalServerError, oErr)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"user":              userWithOrg.User,
			"organization":      userWithOrg.Organization,
			"organizations":     orgs,
			"is_platform_admin": middleware.IsPlatformAdmin(claims.Email),
		})
	}
}

// handleRefreshToken refreshes the access token using refresh token
func handleRefreshToken(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if req.RefreshToken == "" {
			respondError(w, http.StatusBadRequest, ErrRefreshTokenMissing)
			return
		}

		newUser, tokens, err := authService.RefreshTokens(req.RefreshToken)
		if err != nil {
			respondError(w, http.StatusUnauthorized, ErrRefreshTokenInvalid)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"user":   newUser,
			"tokens": tokens,
		})
	}
}

// handleSwitchOrg issues a fresh session for another organization the
// authenticated user belongs to. Returns 403 if they are not a member.
func handleSwitchOrg(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaimsFromContext(r.Context())
		if claims == nil {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}
		var req struct {
			OrganizationID int64 `json:"organization_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		userWithOrg, tokens, err := authService.SwitchOrganization(claims.UserID, req.OrganizationID)
		if err != nil {
			if err == services.ErrUserNotFound {
				respondError(w, http.StatusForbidden, ErrNotAMember)
			} else {
				respondError(w, http.StatusInternalServerError, err)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"user":         userWithOrg.User,
			"organization": userWithOrg.Organization,
			"tokens":       tokens,
		})
	}
}

// handleVerifyEmail confirms a user's email from the token embedded in the
// verification link. Public endpoint (the user may not be logged in on the
// device opening the link). On success the welcome email is sent.
func handleVerifyEmail(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		userID, err := authService.VerifyEmail(req.Token)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrVerificationLinkInvalid)
			return
		}

		// Best-effort welcome email now that the account is active.
		if user, err := authService.GetUserByID(userID); err == nil && user != nil {
			go func() {
				if err := services.SendWelcomeEmail(user.User.Email, user.User.Name, user.Organization.Name); err != nil {
					slog.Error("welcome email failed", "email", user.User.Email, "error", err)
				}
			}()
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "email verified"})
	}
}

// handleResendVerification re-issues and re-sends a verification email for the
// authenticated user. Used by the dashboard gate's "resend" action.
func handleResendVerification(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaimsFromContext(r.Context())
		if claims == nil {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		userWithOrg, err := authService.GetUserByID(claims.UserID)
		if err != nil {
			respondError(w, http.StatusNotFound, ErrUserNotFound)
			return
		}

		token, err := authService.IssueEmailVerification(claims.UserID)
		if err != nil {
			if err == services.ErrAlreadyVerified {
				// Idempotent: nothing to do, report success so the UI can unlock.
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"message": "already verified"})
				return
			}
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		go func() {
			if err := services.SendVerificationEmail(userWithOrg.User.Email, userWithOrg.User.Name, token); err != nil {
				slog.Error("verification resend failed", "email", userWithOrg.User.Email, "error", err)
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "verification email sent"})
	}
}

// handleForgotPassword issues a password-reset token and emails a reset link.
// It always responds 200 with the same generic message whether or not the email
// matches an account, so the endpoint can't be used to enumerate registered users.
func handleForgotPassword(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		token, name, err := authService.IssuePasswordReset(req.Email)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		// token is empty when no activated account matches: silently skip the email.
		if token != "" {
			go func() {
				if err := services.SendPasswordResetEmail(req.Email, name, token); err != nil {
					slog.Error("password reset email failed", "email", req.Email, "error", err)
				}
			}()
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "If an account exists for that email, a reset link has been sent."})
	}
}

// handleResetPassword sets a new password from a valid reset token.
func handleResetPassword(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Token    string `json:"token"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		if err := authService.ResetPassword(req.Token, req.Password); err != nil {
			switch err {
			case services.ErrInvalidToken:
				respondError(w, http.StatusBadRequest, ErrResetLinkInvalid)
			case services.ErrWeakPassword:
				respondError(w, http.StatusBadRequest, services.ErrWeakPassword)
			default:
				respondError(w, http.StatusInternalServerError, err)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "password reset"})
	}
}

// loadOrg loads the full organization including SMTP settings. The SMTP password
// is never returned; SMTPConfigured reports whether one is set.
func loadOrg(db *sql.DB, orgID int64) (models.Organization, error) {
	var org models.Organization
	var passEnc string
	var trialEndsAt sql.NullTime
	err := db.QueryRow(`
		SELECT id, name, slug, COALESCE(plan, 'free'), trial_ends_at, created_at, updated_at,
		       COALESCE(alert_warning_enabled, true), COALESCE(alert_critical_enabled, true),
		       COALESCE(mfa_required, false),
		       COALESCE(smtp_host, ''), COALESCE(smtp_port, ''), COALESCE(smtp_user, ''),
		       COALESCE(smtp_from, ''), COALESCE(smtp_pass_enc, '')
		FROM organizations WHERE id = $1
	`, orgID).Scan(&org.ID, &org.Name, &org.Slug, &org.Plan, &trialEndsAt, &org.CreatedAt, &org.UpdatedAt,
		&org.AlertWarningEnabled, &org.AlertCriticalEnabled, &org.MFARequired,
		&org.SMTPHost, &org.SMTPPort, &org.SMTPUser, &org.SMTPFrom, &passEnc)
	if err != nil {
		return org, err
	}
	services.ApplyTrial(&org, trialEndsAt)
	org.SMTPConfigured = passEnc != ""
	return org, nil
}

// handleGetOrganization returns the current organization
func handleGetOrganization(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		if orgID == 0 {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		org, err := loadOrg(db, orgID)
		if err != nil {
			respondError(w, http.StatusNotFound, ErrOrganizationNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(org)
	}
}

// handleUpdateOrganization updates the organization
func handleUpdateOrganization(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		if orgID == 0 {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		var req struct {
			Name                 string  `json:"name"`
			AlertWarningEnabled  *bool   `json:"alert_warning_enabled,omitempty"`
			AlertCriticalEnabled *bool   `json:"alert_critical_enabled,omitempty"`
			MFARequired          *bool   `json:"mfa_required,omitempty"`
			SMTPHost             *string `json:"smtp_host,omitempty"`
			SMTPPort             *string `json:"smtp_port,omitempty"`
			SMTPUser             *string `json:"smtp_user,omitempty"`
			SMTPFrom             *string `json:"smtp_from,omitempty"`
			// Write-only: send only to set a new password; "" clears it; omit to keep.
			SMTPPass *string `json:"smtp_pass,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		// Toggling org-wide 2FA enforcement is an admin action.
		if req.MFARequired != nil {
			claims := middleware.GetClaimsFromContext(r.Context())
			if claims == nil || claims.Role != "admin" {
				respondError(w, http.StatusForbidden, ErrAdminRequired)
				return
			}
			// Enforcing 2FA sends every member without an authenticator into the
			// enrollment gate, admins included. Requiring the admin to be enrolled
			// first keeps them from locking themselves out of their own org.
			if *req.MFARequired {
				var enrolled bool
				if err := authService.DB().QueryRow(
					`SELECT COALESCE(totp_enabled, false) FROM users WHERE id = $1`,
					claims.UserID).Scan(&enrolled); err != nil {
					respondError(w, http.StatusInternalServerError, err)
					return
				}
				if !enrolled {
					respondError(w, http.StatusConflict, services.ErrMFAEnrollFirst)
					return
				}
			}
			if _, err := authService.DB().Exec(
				`UPDATE organizations SET mfa_required = $1, updated_at = NOW() WHERE id = $2`,
				*req.MFARequired, orgID); err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
		}

		// Per-org SMTP for email alert channels (admin only). Each field updates
		// independently; the password is encrypted at rest and only touched when
		// smtp_pass is sent ("" clears it).
		if req.SMTPHost != nil || req.SMTPPort != nil || req.SMTPUser != nil || req.SMTPFrom != nil || req.SMTPPass != nil {
			claims := middleware.GetClaimsFromContext(r.Context())
			if claims == nil || claims.Role != "admin" {
				respondError(w, http.StatusForbidden, ErrAdminRequired)
				return
			}
			if req.SMTPHost != nil {
				if _, err := authService.DB().Exec(`UPDATE organizations SET smtp_host = $1, updated_at = NOW() WHERE id = $2`, strings.TrimSpace(*req.SMTPHost), orgID); err != nil {
					respondError(w, http.StatusInternalServerError, err)
					return
				}
			}
			if req.SMTPPort != nil {
				if _, err := authService.DB().Exec(`UPDATE organizations SET smtp_port = $1, updated_at = NOW() WHERE id = $2`, strings.TrimSpace(*req.SMTPPort), orgID); err != nil {
					respondError(w, http.StatusInternalServerError, err)
					return
				}
			}
			if req.SMTPUser != nil {
				if _, err := authService.DB().Exec(`UPDATE organizations SET smtp_user = $1, updated_at = NOW() WHERE id = $2`, *req.SMTPUser, orgID); err != nil {
					respondError(w, http.StatusInternalServerError, err)
					return
				}
			}
			if req.SMTPFrom != nil {
				if _, err := authService.DB().Exec(`UPDATE organizations SET smtp_from = $1, updated_at = NOW() WHERE id = $2`, strings.TrimSpace(*req.SMTPFrom), orgID); err != nil {
					respondError(w, http.StatusInternalServerError, err)
					return
				}
			}
			if req.SMTPPass != nil {
				encrypted, err := services.EncryptSMTPPassword(*req.SMTPPass)
				if err != nil {
					respondError(w, http.StatusInternalServerError, err)
					return
				}
				if _, err := authService.DB().Exec(`UPDATE organizations SET smtp_pass_enc = $1, updated_at = NOW() WHERE id = $2`, encrypted, orgID); err != nil {
					respondError(w, http.StatusInternalServerError, err)
					return
				}
			}
		}

		// Per-org alert severity opt-in is updated directly (independent of name).
		if req.AlertWarningEnabled != nil {
			if _, err := authService.DB().Exec(
				`UPDATE organizations SET alert_warning_enabled = $1, updated_at = NOW() WHERE id = $2`,
				*req.AlertWarningEnabled, orgID); err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
		}
		if req.AlertCriticalEnabled != nil {
			if _, err := authService.DB().Exec(
				`UPDATE organizations SET alert_critical_enabled = $1, updated_at = NOW() WHERE id = $2`,
				*req.AlertCriticalEnabled, orgID); err != nil {
				respondError(w, http.StatusInternalServerError, err)
				return
			}
		}

		if req.Name == "" {
			// Settings-only update (e.g. toggles, SMTP): return the current org.
			org, err := loadOrg(authService.DB(), orgID)
			if err != nil {
				respondError(w, http.StatusNotFound, ErrOrganizationNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(org)
			return
		}

		if _, err := authService.UpdateOrganization(orgID, req.Name); err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		org, err := loadOrg(authService.DB(), orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(org)
	}
}

// handleTestSMTP sends a test email through the org's SMTP config (admin only).
func handleTestSMTP(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		claims := middleware.GetClaimsFromContext(r.Context())
		if orgID == 0 || claims == nil {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}
		if claims.Role != "admin" {
			respondError(w, http.StatusForbidden, ErrAdminRequired)
			return
		}

		var req struct {
			Email string `json:"email"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		to := strings.TrimSpace(req.Email)
		if to == "" {
			to = claims.Email
		}
		if to == "" {
			respondError(w, http.StatusBadRequest, ErrRecipientMissing)
			return
		}

		if err := services.SendOrgTestEmail(orgID, to); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "sent", "to": to})
	}
}

// handleGetOrganizationUsers returns all users in the organization
func handleGetOrganizationUsers(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		if orgID == 0 {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		users, err := authService.GetOrganizationUsers(orgID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(users)
	}
}

// handleInviteUser invites a new user to the organization (admin only)
func handleInviteUser(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		if orgID == 0 {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		var req models.InviteUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		user, inviteToken, err := authService.InviteUser(orgID, req)
		if err != nil {
			switch err {
			case services.ErrUserExists:
				respondError(w, http.StatusConflict, ErrAlreadyMember)
			case services.ErrInvalidEmail:
				respondError(w, http.StatusBadRequest, ErrEmailInvalid)
			default:
				respondError(w, http.StatusInternalServerError, err)
			}
			return
		}

		// Resolve the target org's name by its id (the active org from the JWT), not
		// via the inviter's home org — an admin may be inviting into a non-home org.
		orgName := "your organization"
		if err := authService.DB().QueryRow("SELECT name FROM organizations WHERE id = $1", orgID).Scan(&orgName); err != nil {
			slog.Error("failed to resolve org name", "org_id", orgID, "error", err)
		}

		// Best-effort email. A new identity gets an activation link (set password);
		// an existing identity is simply notified that it now has access.
		email, name, token := user.Email, user.Name, inviteToken
		go func() {
			var mailErr error
			if token != "" {
				mailErr = services.SendInvitationEmail(email, name, orgName, token)
			} else {
				mailErr = services.SendAddedToOrgEmail(email, name, orgName)
			}
			if mailErr != nil {
				slog.Error("invitation email failed", "email", email, "error", mailErr)
			}
		}()

		message := "Invitation sent. The user will receive an email to set their password and join the organization."
		if inviteToken == "" {
			message = "User added to the organization. They have been notified by email."
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"user":    user,
			"message": message,
		})
	}
}

// handleAcceptInvitation activates an invited account from the token embedded in
// the invitation link. Public endpoint (the invitee is not logged in yet): it
// sets their chosen password, verifies the email and returns a fresh session so
// the frontend can log them straight in.
func handleAcceptInvitation(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Token    string `json:"token"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}

		userWithOrg, tokens, err := authService.AcceptInvitationAndLogin(req.Token, req.Password)
		if err != nil {
			switch err {
			case services.ErrInvalidToken:
				respondError(w, http.StatusBadRequest, ErrInvitationLinkInvalid)
			case services.ErrWeakPassword:
				respondError(w, http.StatusBadRequest, services.ErrWeakPassword)
			default:
				respondError(w, http.StatusInternalServerError, err)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"user":         userWithOrg.User,
			"organization": userWithOrg.Organization,
			"tokens":       tokens,
		})
	}
}

// handleDeleteUser removes a user from the organization (admin only)
func handleDeleteUser(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		userID := middleware.GetUserID(r.Context())
		if orgID == 0 || userID == 0 {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		vars := mux.Vars(r)
		targetUserID, err := strconv.ParseInt(vars["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrUserIDInvalid)
			return
		}

		if err := authService.DeleteUser(orgID, targetUserID, userID); err != nil {
			if err == services.ErrUserNotFound {
				respondError(w, http.StatusNotFound, ErrUserNotFound)
			} else if errors.Is(err, services.ErrSelfAccountDelete) {
				respondError(w, http.StatusBadRequest, ErrOwnAccountDelete)
			} else {
				respondError(w, http.StatusInternalServerError, err)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "User deleted successfully",
		})
	}
}

// handleUpdateUserRole changes a member's role within the org (admin only).
func handleUpdateUserRole(authService *services.AuthService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.GetOrganizationID(r.Context())
		userID := middleware.GetUserID(r.Context())
		if orgID == 0 || userID == 0 {
			respondError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}

		targetUserID, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
		if err != nil {
			respondError(w, http.StatusBadRequest, ErrUserIDInvalid)
			return
		}
		if targetUserID == userID {
			respondError(w, http.StatusBadRequest, ErrOwnRoleChange)
			return
		}

		var req struct {
			Role string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, ErrRequestBodyInvalid)
			return
		}
		if !services.ValidRole(req.Role) {
			respondError(w, http.StatusBadRequest, ErrRoleInvalid)
			return
		}

		if err := authService.UpdateUserRole(orgID, targetUserID, req.Role); err != nil {
			switch {
			case err == services.ErrUserNotFound:
				respondError(w, http.StatusNotFound, ErrUserNotFound)
			case errors.Is(err, services.ErrLastAdmin):
				respondError(w, http.StatusBadRequest, err)
			default:
				respondError(w, http.StatusInternalServerError, err)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "role updated", "role": req.Role})
	}
}

// OrganizationStats represents aggregated stats for an organization
type OrganizationStats struct {
	Services struct {
		Total   int `json:"total"`
		Healthy int `json:"healthy"`
		Warning int `json:"warning"`
		Failing int `json:"failing"`
		Unknown int `json:"unknown"`
	} `json:"services"`
	Hosts struct {
		Total   int `json:"total"`
		Healthy int `json:"healthy"`
		Failing int `json:"failing"`
	} `json:"hosts"`
	Errors struct {
		Total24h         int            `json:"total_24h"`
		ByService        map[string]int `json:"by_service"`
		ImpactedServices int            `json:"impacted_services"`
	} `json:"errors"`
	Status    string `json:"status"` // healthy, degraded, critical
	PlanUsage struct {
		HostsUsed          int `json:"hosts_used"`
		HostsLimit         int `json:"hosts_limit"`    // -1 = unlimited
		ServicesUsed       int `json:"services_used"`  // monitored check services
		ServicesLimit      int `json:"services_limit"` // -1 = unlimited
		ErrorServicesUsed  int `json:"error_services_used"`
		ErrorServicesLimit int `json:"error_services_limit"` // -1 = unlimited
		// Metric ingestion: the busiest minute of the last hour against the budget,
		// and the points over it in the last day, rejected only when enforced.
		PointsPerMinuteLimit int  `json:"points_per_minute_limit"` // -1 = unlimited
		PointsPerMinutePeak  int  `json:"points_per_minute_peak"`
		PointsOverLimit24h   int  `json:"points_over_limit_24h"`
		PointsLimitEnforced  bool `json:"points_limit_enforced"`
	} `json:"plan_usage"`
}

// handleGetOrganizationStats returns aggregated stats for the organization
func handleGetOrganizationStats(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// OrgAccessMiddleware already validated org_id matches user's org
		orgID := middleware.GetOrganizationID(r.Context())

		var stats OrganizationStats
		stats.Errors.ByService = make(map[string]int)

		// Get all services count (excluding error_service_*; those are in the Errors tab)
		err := db.QueryRow(`
			SELECT COUNT(*) FROM services WHERE organization_id = $1 AND type NOT LIKE 'error_service_%'
		`, orgID).Scan(&stats.Services.Total)
		if err != nil {
			slog.Error("failed to count services", "error", err)
		}

		// Service status breakdown: reuse the smoothed facet counts (a service only
		// turns failing/warning after max_attempts consecutive breaches) so the
		// overview matches the services page instead of the raw latest result.
		if svcStats, err := services.NewServiceService(db).ServiceStats(orgID); err != nil {
			slog.Error("failed to fetch service statuses", "error", err)
		} else {
			stats.Services.Healthy = svcStats.Healthy
			stats.Services.Warning = svcStats.Warning
			stats.Services.Failing = svcStats.Failing
			stats.Services.Unknown = svcStats.Unknown
		}

		// Get hosts count and status
		err = db.QueryRow(`
			SELECT COUNT(*) FROM hosts WHERE organization_id = $1
		`, orgID).Scan(&stats.Hosts.Total)
		if err != nil {
			slog.Error("failed to count hosts", "error", err)
		}

		err = db.QueryRow(`
			SELECT COUNT(*) FROM hosts WHERE organization_id = $1 AND status = 'success'
		`, orgID).Scan(&stats.Hosts.Healthy)
		if err != nil {
			slog.Error("failed to count healthy hosts", "error", err)
		}

		err = db.QueryRow(`
			SELECT COUNT(*) FROM hosts WHERE organization_id = $1 AND status = 'failure'
		`, orgID).Scan(&stats.Hosts.Failing)
		if err != nil {
			slog.Error("failed to count failing hosts", "error", err)
		}

		// Get errors in last 24h
		err = db.QueryRow(`
			SELECT COUNT(*) FROM application_errors
			WHERE organization_id = $1 AND timestamp > NOW() - INTERVAL '24 hours'
		`, orgID).Scan(&stats.Errors.Total24h)
		if err != nil {
			slog.Error("failed to count errors", "error", err)
		}

		// Get errors by service
		errorRows, err := db.Query(`
			SELECT service, COUNT(*) as count
			FROM application_errors
			WHERE organization_id = $1 AND timestamp > NOW() - INTERVAL '24 hours'
			GROUP BY service
			ORDER BY count DESC
			LIMIT 10
		`, orgID)
		if err != nil {
			slog.Error("failed to fetch errors by service", "error", err)
		} else {
			defer errorRows.Close()
			for errorRows.Next() {
				var serviceName string
				var count int
				if err := errorRows.Scan(&serviceName, &count); err != nil {
					continue
				}
				stats.Errors.ByService[serviceName] = count
			}
			stats.Errors.ImpactedServices = len(stats.Errors.ByService)
		}

		// Plan usage: current resource consumption vs. plan limits (custom plans read
		// their purchased caps from the org row).
		limits, err := services.NewPlanLimitsService(db).GetLimits(orgID)
		if err != nil {
			slog.Error("failed to fetch plan limits", "error", err)
			limits = services.LimitsForPlan("free")
		}
		stats.PlanUsage.HostsUsed = stats.Hosts.Total
		stats.PlanUsage.HostsLimit = limits.MaxHosts
		stats.PlanUsage.ServicesLimit = limits.MaxMonitoredServices
		stats.PlanUsage.ErrorServicesLimit = limits.MaxErrorServices
		// Monitored check services (excludes agent metrics and SDK services).
		if err := db.QueryRow(`SELECT COUNT(*) FROM services WHERE organization_id = $1 AND type NOT LIKE 'agent_%' AND type NOT LIKE 'error_service_%'`, orgID).Scan(&stats.PlanUsage.ServicesUsed); err != nil {
			slog.Error("failed to count monitored services", "error", err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM services WHERE organization_id = $1 AND type LIKE 'error_service_%'`, orgID).Scan(&stats.PlanUsage.ErrorServicesUsed); err != nil {
			slog.Error("failed to count error services", "error", err)
		}
		stats.PlanUsage.PointsPerMinuteLimit = services.PointsPerMinute(limits)
		stats.PlanUsage.PointsLimitEnforced = ingestBudgetEnforced()
		if err := db.QueryRow(`
			SELECT
				COALESCE(MAX(accepted_points + over_limit_points) FILTER (WHERE minute >= NOW() - INTERVAL '1 hour'), 0),
				COALESCE(SUM(over_limit_points), 0)
			FROM ingest_usage
			WHERE organization_id = $1 AND minute >= NOW() - INTERVAL '24 hours'
		`, orgID).Scan(&stats.PlanUsage.PointsPerMinutePeak, &stats.PlanUsage.PointsOverLimit24h); err != nil {
			slog.Error("failed to read ingest usage", "error", err)
		}

		// Determine overall status
		if stats.Services.Failing > 0 || stats.Hosts.Failing > 0 {
			if stats.Services.Failing > stats.Services.Total/2 || stats.Hosts.Failing > stats.Hosts.Total/2 {
				stats.Status = "critical"
			} else {
				stats.Status = "degraded"
			}
		} else if stats.Services.Warning > 0 {
			stats.Status = "degraded"
		} else {
			stats.Status = "healthy"
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}
}
