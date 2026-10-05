package middleware

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"

	"middle-monitor/backend/services"
)

type contextKey string

const (
	UserContextKey   contextKey = "user"
	ClaimsContextKey contextKey = "claims"
)

// AuthMiddleware creates a middleware that validates JWT tokens
func AuthMiddleware(authService *services.AuthService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get token from Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, `{"error": "Authorization header required"}`, http.StatusUnauthorized)
				return
			}

			// Check Bearer prefix
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				http.Error(w, `{"error": "Invalid authorization header format"}`, http.StatusUnauthorized)
				return
			}

			tokenString := parts[1]

			// Validate token: first as a JWT, then as an "mm_" API key.
			claims, err := authService.ValidateToken(tokenString)
			if err != nil {
				if apiClaims, ok := apiKeyClaims(authService, tokenString); ok {
					ctx := context.WithValue(r.Context(), ClaimsContextKey, apiClaims)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				http.Error(w, `{"error": "Invalid or expired token"}`, http.StatusUnauthorized)
				return
			}

			// A "mfa" challenge token only proves the password step; it must never act
			// as a bearer/access token.
			if claims.Purpose == "mfa" {
				http.Error(w, `{"error": "Invalid or expired token"}`, http.StatusUnauthorized)
				return
			}

			// Add claims to context
			ctx := context.WithValue(r.Context(), ClaimsContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// apiKeyClaims validates a raw API key and, on success, synthesizes JWT-like
// claims. A personal token (user_id set) authenticates AS its owner, carrying the
// user's role; an org token gets write access. This also stamps the key's
// last_used_at. Returns false if the token is not a valid API key.
func apiKeyClaims(authService *services.AuthService, token string) (*services.JWTClaims, bool) {
	ident, err := services.NewAPIKeyService(authService.DB()).ValidateKey(token)
	if err != nil {
		return nil, false
	}
	claims := &services.JWTClaims{
		OrganizationID:   ident.OrgID,
		OrganizationSlug: ident.OrgSlug,
		// API keys belong to an established org; they must never be gated by the
		// human email-verification or 2FA-enrollment flows (it would break SDK/agent
		// ingestion). MFAEnrollmentRequired stays false (zero value).
		EmailVerified: true,
	}
	if ident.UserID != nil {
		// Personal token: act as the owning user, with their role and identity.
		claims.UserID = *ident.UserID
		claims.Role = ident.Role
		claims.Email = ident.Email
	} else {
		// Org token: ingest data (SDK/agent), so it needs write access. Admin-only
		// routes (user management, billing) are never reachable with an API key.
		claims.Role = services.RoleReadWrite
	}
	return claims, true
}

// OptionalAuthMiddleware creates a middleware that validates JWT tokens but doesn't require them
func OptionalAuthMiddleware(authService *services.AuthService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get token from Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				// No auth, continue without claims
				next.ServeHTTP(w, r)
				return
			}

			// Check Bearer prefix
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				// Invalid format, continue without claims
				next.ServeHTTP(w, r)
				return
			}

			tokenString := parts[1]

			// Validate token
			claims, err := authService.ValidateToken(tokenString)
			if err != nil {
				// Invalid token, continue without claims
				next.ServeHTTP(w, r)
				return
			}

			// Add claims to context
			ctx := context.WithValue(r.Context(), ClaimsContextKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WriteAccess gates mutating requests to roles that may write (read_write or
// admin). Read requests pass through for everyone, so read_only users keep full
// read access but cannot mutate anything. Default-deny: any unrecognized role is
// treated as read-only for writes.
func WriteAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isReadRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		claims := GetClaimsFromContext(r.Context())
		if claims == nil {
			http.Error(w, `{"error": "Unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if claims.Role != services.RoleReadWrite && claims.Role != services.RoleAdmin {
			http.Error(w, `{"error": "Write access required"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isReadRequest classifies a request as a read (permitted for every role,
// including read_only). Safe HTTP methods are reads. A few endpoints use POST but
// are read-only analysis actions — they run an on-demand AI explanation and never
// mutate org resources — so they are treated as reads too.
func isReadRequest(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	// The series expression query is a POST only because its queries do not fit a URL.
	return r.Method == http.MethodPost &&
		(strings.HasSuffix(r.URL.Path, "/explain") || strings.HasSuffix(r.URL.Path, "/metrics/series/expression"))
}

// PlatformAdminEmails parses PLATFORM_ADMIN_EMAILS (comma-separated) into the
// set of emails allowed onto the cross-org platform admin routes.
func PlatformAdminEmails() map[string]struct{} {
	emails := map[string]struct{}{}
	for _, e := range strings.Split(os.Getenv("PLATFORM_ADMIN_EMAILS"), ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" {
			emails[e] = struct{}{}
		}
	}
	return emails
}

// IsPlatformAdmin reports whether email is listed in PLATFORM_ADMIN_EMAILS.
func IsPlatformAdmin(email string) bool {
	_, ok := PlatformAdminEmails()[strings.ToLower(email)]
	return ok
}

// RequirePlatformAdmin gates the cross-organization admin routes to the emails
// listed in PLATFORM_ADMIN_EMAILS. Unlike AdminOnly (an org's own admin role),
// this has nothing to do with org membership.
func RequirePlatformAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaimsFromContext(r.Context())
		if claims == nil {
			http.Error(w, `{"error": "Unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if !IsPlatformAdmin(claims.Email) {
			http.Error(w, `{"error": "platform admin access required"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AdminOnly creates a middleware that requires admin role
func AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaimsFromContext(r.Context())
		if claims == nil {
			http.Error(w, `{"error": "Unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if claims.Role != services.RoleAdmin {
			http.Error(w, `{"error": "Admin access required"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireVerifiedEmail blocks access for authenticated users whose email is not
// yet verified. API-key callers carry EmailVerified=true and are never blocked.
// Returns 403 so the frontend can distinguish "not verified" from "not logged in".
func RequireVerifiedEmail(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaimsFromContext(r.Context())
		if claims == nil {
			http.Error(w, `{"error": "Unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if !claims.EmailVerified {
			http.Error(w, `{"error": "email not verified"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireMFAEnrollment blocks access when the org enforces 2FA and the user has not
// yet enrolled an authenticator. API-key callers carry MFAEnrollmentRequired=false
// and are never blocked. Returns 403 so the frontend can show the enrollment gate.
func RequireMFAEnrollment(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaimsFromContext(r.Context())
		if claims == nil {
			http.Error(w, `{"error": "Unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if claims.MFAEnrollmentRequired {
			http.Error(w, `{"error": "mfa enrollment required"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GetClaimsFromContext retrieves JWT claims from context
func GetClaimsFromContext(ctx context.Context) *services.JWTClaims {
	claims, ok := ctx.Value(ClaimsContextKey).(*services.JWTClaims)
	if !ok {
		return nil
	}
	return claims
}

// OrgAccessMiddleware validates {org_slug} in the URL matches the user's organization.
func OrgAccessMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaimsFromContext(r.Context())
		if claims == nil {
			http.Error(w, `{"error": "Unauthorized"}`, http.StatusUnauthorized)
			return
		}

		vars := mux.Vars(r)
		orgSlug := vars["org_slug"]
		if orgSlug == "" {
			http.Error(w, `{"error": "Organization slug required"}`, http.StatusBadRequest)
			return
		}

		// Verify user belongs to this org by comparing slug from JWT
		if claims.OrganizationSlug != orgSlug {
			http.Error(w, `{"error": "Access denied to this organization"}`, http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// GetOrganizationID retrieves the organization ID from context
func GetOrganizationID(ctx context.Context) int64 {
	claims := GetClaimsFromContext(ctx)
	if claims == nil {
		return 0
	}
	return claims.OrganizationID
}

// GetUserID retrieves the user ID from context
func GetUserID(ctx context.Context) int64 {
	claims := GetClaimsFromContext(ctx)
	if claims == nil {
		return 0
	}
	return claims.UserID
}

// GetUserEmail retrieves the user email from context
func GetUserEmail(ctx context.Context) string {
	claims := GetClaimsFromContext(ctx)
	if claims == nil {
		return ""
	}
	return claims.Email
}
