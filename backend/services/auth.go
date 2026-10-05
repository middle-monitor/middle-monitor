package services

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image/png"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"middle-monitor/backend/models"
)

// User roles within an organization, ordered by privilege.
//   - RoleReadOnly: may only read (safe HTTP methods); all writes are rejected.
//   - RoleReadWrite: full read/write on org resources (create, update, delete).
//   - RoleAdmin: everything RoleReadWrite can do, plus user management and billing.
const (
	RoleReadOnly  = "read_only"
	RoleReadWrite = "read_write"
	RoleAdmin     = "admin"
)

// IsValidRole reports whether r is one of the three recognized org roles.
func IsValidRole(r string) bool {
	return r == RoleReadOnly || r == RoleReadWrite || r == RoleAdmin
}

type AuthService struct {
	db        *sql.DB
	jwtSecret []byte
}

type JWTClaims struct {
	UserID           int64  `json:"user_id"`
	OrganizationID   int64  `json:"organization_id"`
	OrganizationSlug string `json:"organization_slug"`
	Email            string `json:"email"`
	Role             string `json:"role"`
	EmailVerified    bool   `json:"email_verified"`
	// MFAEnrollmentRequired is true when the org enforces 2FA and this user has not
	// yet enrolled an authenticator. The dashboard stays locked (RequireMFAEnrollment)
	// until enrollment completes; recomputed on every token mint so an admin toggling
	// the org setting takes effect on the next refresh.
	MFAEnrollmentRequired bool `json:"mfa_enrollment_required"`
	// Purpose distinguishes token kinds. Empty for access/refresh tokens; "mfa" for
	// the short-lived challenge issued between password and code at login. A "mfa"
	// token is rejected by AuthMiddleware so it can never act as an access token.
	Purpose string `json:"purpose,omitempty"`
	jwt.RegisteredClaims
}

// mfaChallengePurpose marks the short-lived token issued after the password step
// when a TOTP code is still required to finish logging in.
const mfaChallengePurpose = "mfa"

func NewAuthService(db *sql.DB) *AuthService {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		// Refuse to boot without an explicit secret. A random per-process secret
		// invalidates every issued token at restart and silently masks misconfig
		// in production.
		slog.Error("JWT_SECRET is required, generate one with `openssl rand -base64 32`")
		os.Exit(1)
	}
	if len(secret) < 32 {
		slog.Error("JWT_SECRET is too short, use at least 32 chars", "chars", len(secret))
		os.Exit(1)
	}
	return &AuthService{
		db:        db,
		jwtSecret: []byte(secret),
	}
}

// DB exposes the underlying connection for handlers that need a direct query
// (e.g. updating org-level settings) without a dedicated service method.
func (s *AuthService) DB() *sql.DB {
	return s.db
}

// Register creates a new organization and admin user
func (s *AuthService) Register(req models.RegisterRequest) (*models.UserWithOrg, *models.AuthTokens, error) {
	// Validate input
	if err := validateEmail(req.Email); err != nil {
		return nil, nil, err
	}
	if err := validatePassword(req.Password); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, nil, ErrNameRequired
	}
	if strings.TrimSpace(req.OrganizationName) == "" {
		return nil, nil, ErrOrgNameRequired
	}
	if len(strings.TrimSpace(req.OrganizationName)) > 255 {
		return nil, nil, ErrOrgNameTooLong
	}

	// Check if user already exists
	var existingID int64
	err := s.db.QueryRow("SELECT id FROM users WHERE email = $1", strings.ToLower(req.Email)).Scan(&existingID)
	if err == nil {
		return nil, nil, ErrUserExists
	} else if err != sql.ErrNoRows {
		return nil, nil, fmt.Errorf("%w: %w", ErrExistingUserCheck, err)
	}

	// Resolve org slug: when the caller provides one it is the chosen identifier
	// and must be valid + unique (hard error if taken). When omitted, fall back to
	// deriving it from the name with a random suffix on collision (legacy).
	userChoseSlug := strings.TrimSpace(req.OrganizationSlug) != ""
	var slug string
	if userChoseSlug {
		if err := validateSlug(req.OrganizationSlug); err != nil {
			return nil, nil, err
		}
		slug = req.OrganizationSlug
	} else {
		slug = generateSlug(req.OrganizationName)
	}

	// Check if org slug already exists
	err = s.db.QueryRow("SELECT id FROM organizations WHERE slug = $1", slug).Scan(&existingID)
	if err == nil {
		if userChoseSlug {
			return nil, nil, ErrOrgExists
		}
		// Add random suffix to make it unique
		slug = fmt.Sprintf("%s-%s", slug, generateRandomString(6))
	} else if err != sql.ErrNoRows {
		return nil, nil, fmt.Errorf("%w: %w", ErrExistingOrgCheck, err)
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrPasswordHash, err)
	}

	// Start transaction
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrTransactionStart, err)
	}
	defer tx.Rollback()

	// Create organization: free plan, opened with a card-less Pro trial so the
	// product can be evaluated beyond the free caps.
	var org models.Organization
	var trialEndsAt sql.NullTime
	err = tx.QueryRow(`
		INSERT INTO organizations (name, slug, plan, trial_ends_at, created_at, updated_at)
		VALUES ($1, $2, 'free', NOW() + make_interval(days => $3), NOW(), NOW())
		RETURNING id, name, slug, plan, trial_ends_at, created_at, updated_at
	`, req.OrganizationName, slug, TrialDays).Scan(&org.ID, &org.Name, &org.Slug, &org.Plan, &trialEndsAt, &org.CreatedAt, &org.UpdatedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrOrganizationCreate, err)
	}
	ApplyTrial(&org, trialEndsAt)

	// Every organization gets a default host group; new hosts land here unless
	// explicitly reassigned, and correlation scopes to a host + its group.
	if _, err = tx.Exec(`INSERT INTO host_groups (organization_id, name, is_default) VALUES ($1, 'Default', true)`, org.ID); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrDefaultHostGroupCreate, err)
	}

	// Create admin user. Self-serve registrations start unverified: the account
	// must confirm its email before the dashboard API is unlocked.
	var user models.User
	err = tx.QueryRow(`
		INSERT INTO users (organization_id, email, password_hash, name, role, email_verified, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'admin', false, NOW(), NOW())
		RETURNING id, organization_id, email, name, role, email_verified, created_at, updated_at
	`, org.ID, strings.ToLower(req.Email), string(hashedPassword), req.Name).Scan(
		&user.ID, &user.OrganizationID, &user.Email, &user.Name, &user.Role, &user.EmailVerified, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrUserCreate, err)
	}

	// Record the founder's membership (admin) — the source of truth for org access.
	_, err = tx.Exec(`
		INSERT INTO memberships (user_id, organization_id, role, created_at)
		VALUES ($1, $2, 'admin', NOW())
	`, user.ID, org.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrMembershipCreate, err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrTransactionCommit, err)
	}

	// Generate tokens. A freshly created org never enforces 2FA yet.
	tokens, err := s.generateTokens(&user, org.Slug, false)
	if err != nil {
		return nil, nil, err
	}

	return &models.UserWithOrg{User: user, Organization: org}, tokens, nil
}

// Login authenticates a user with email + password. When the org enforces 2FA and
// the user has enrolled an authenticator, no session tokens are issued yet: a
// short-lived MFA challenge token is returned instead (mfaChallenge non-empty,
// user/tokens nil) and the caller must finish via CompleteMFALogin. Otherwise the
// usual user + tokens are returned (mfaChallenge empty).
func (s *AuthService) Login(req models.LoginRequest) (*models.UserWithOrg, *models.AuthTokens, string, error) {
	// Find the identity by email (org-agnostic — a user may belong to several orgs).
	var user models.User
	err := s.db.QueryRow(`
		SELECT id, organization_id, email, password_hash, name, COALESCE(email_verified, false), COALESCE(totp_enabled, false), created_at, updated_at
		FROM users
		WHERE email = $1
	`, strings.ToLower(req.Email)).Scan(
		&user.ID, &user.OrganizationID, &user.Email, &user.PasswordHash, &user.Name, &user.EmailVerified, &user.TOTPEnabled, &user.CreatedAt, &user.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil, "", ErrInvalidCredentials
	} else if err != nil {
		return nil, nil, "", fmt.Errorf("%w: %w", ErrUserFind, err)
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return nil, nil, "", ErrInvalidCredentials
	}

	// Second factor: demanded whenever the user has enrolled an authenticator. It is
	// an identity-level check, independent of the active org. After completion the
	// user lands in their default org (CompleteMFALogin), then can switch.
	if user.TOTPEnabled {
		challenge, err := s.generateMFAChallenge(user.ID)
		if err != nil {
			return nil, nil, "", err
		}
		return nil, nil, challenge, nil
	}

	// Resolve the active (default) org from the user's memberships.
	active, err := s.defaultOrg(user.ID, user.OrganizationID)
	if err != nil {
		return nil, nil, "", err
	}
	user.Role = active.Role
	user.OrganizationID = active.ID

	// Update last login
	if _, err := s.db.Exec("UPDATE users SET last_login_at = NOW() WHERE id = $1", user.ID); err != nil {
		slog.Warn("update last_login_at failed", "error", err)
	}

	tokens, err := s.generateTokens(&user, active.Slug, active.MFARequired)
	if err != nil {
		return nil, nil, "", err
	}

	return &models.UserWithOrg{User: user, Organization: active.Organization}, tokens, "", nil
}

// defaultOrg picks the org a user lands in by default: their home org
// (users.organization_id) when they are still a member of it, otherwise the
// earliest-joined membership. Returns ErrInvalidCredentials when the identity has
// no membership at all (no org access).
func (s *AuthService) defaultOrg(userID, homeOrgID int64) (*models.UserOrganization, error) {
	orgs, err := s.GetUserOrganizations(userID)
	if err != nil {
		return nil, err
	}
	if len(orgs) == 0 {
		return nil, ErrInvalidCredentials
	}
	for i := range orgs {
		if orgs[i].ID == homeOrgID {
			return &orgs[i], nil
		}
	}
	return &orgs[0], nil
}

// CompleteMFALogin finishes a login that required a second factor. It validates the
// short-lived challenge token returned by Login, then accepts either a valid TOTP
// code or one unused recovery code, and on success issues the real session tokens.
func (s *AuthService) CompleteMFALogin(challengeToken, code string) (*models.UserWithOrg, *models.AuthTokens, error) {
	claims, err := s.ValidateToken(challengeToken)
	if err != nil || claims.Purpose != mfaChallengePurpose {
		return nil, nil, ErrInvalidToken
	}

	userWithOrg, secret, err := s.getUserForMFA(claims.UserID)
	if err != nil {
		return nil, nil, err
	}

	if !s.verifySecondFactor(claims.UserID, secret, code) {
		return nil, nil, ErrInvalidMFACode
	}

	if _, err := s.db.Exec("UPDATE users SET last_login_at = NOW() WHERE id = $1", claims.UserID); err != nil {
		slog.Warn("update last_login_at failed", "error", err)
	}

	tokens, err := s.generateTokens(&userWithOrg.User, userWithOrg.Organization.Slug, userWithOrg.Organization.MFARequired)
	if err != nil {
		return nil, nil, err
	}
	return userWithOrg, tokens, nil
}

// RefreshTokens validates a refresh token and issues a fresh access/refresh pair
// for the same user — no password required. This is the proper refresh path:
// it never re-runs the password check (which is why feeding an empty password to
// Login could never work).
func (s *AuthService) RefreshTokens(refreshToken string) (*models.UserWithOrg, *models.AuthTokens, error) {
	claims, err := s.ValidateToken(refreshToken)
	if err != nil {
		return nil, nil, ErrInvalidToken
	}

	// Refresh keeps the active org carried by the token. If the membership was
	// revoked since the token was minted, fall back to another org the user belongs
	// to (or fail if none remain).
	userWithOrg, err := s.GetUserInOrg(claims.UserID, claims.OrganizationID)
	if err == ErrUserNotFound {
		active, derr := s.defaultOrg(claims.UserID, 0)
		if derr != nil {
			return nil, nil, derr
		}
		userWithOrg, err = s.GetUserInOrg(claims.UserID, active.ID)
	}
	if err != nil {
		return nil, nil, err
	}

	tokens, err := s.generateTokens(&userWithOrg.User, userWithOrg.Organization.Slug, userWithOrg.Organization.MFARequired)
	if err != nil {
		return nil, nil, err
	}

	return userWithOrg, tokens, nil
}

// ValidateToken validates a JWT token and returns the claims
func (s *AuthService) ValidateToken(tokenString string) (*JWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, &SigningMethodError{Alg: token.Header["alg"]}
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		// Log the real cause: often "signature invalid" (JWT_SECRET changed/not set) or "token is expired"
		slog.Debug("jwt validation failed", "error", err)
		return nil, ErrInvalidToken
	}

	if claims, ok := token.Claims.(*JWTClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, ErrInvalidToken
}

// GetUserByID retrieves a user by ID
func (s *AuthService) GetUserByID(userID int64) (*models.UserWithOrg, error) {
	var user models.User
	var org models.Organization
	var trialEndsAt sql.NullTime
	err := s.db.QueryRow(`
		SELECT u.id, u.organization_id, u.email, u.name, u.role, COALESCE(u.email_verified, false), COALESCE(u.totp_enabled, false), u.created_at, u.updated_at, u.last_login_at,
		       o.id, o.name, o.slug, COALESCE(o.plan, 'free'), o.trial_ends_at, COALESCE(o.mfa_required, false), o.created_at, o.updated_at
		FROM users u
		JOIN organizations o ON o.id = u.organization_id
		WHERE u.id = $1
	`, userID).Scan(
		&user.ID, &user.OrganizationID, &user.Email, &user.Name, &user.Role, &user.EmailVerified, &user.TOTPEnabled, &user.CreatedAt, &user.UpdatedAt, &user.LastLoginAt,
		&org.ID, &org.Name, &org.Slug, &org.Plan, &trialEndsAt, &org.MFARequired, &org.CreatedAt, &org.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	} else if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUserFind, err)
	}
	ApplyTrial(&org, trialEndsAt)
	return &models.UserWithOrg{User: user, Organization: org}, nil
}

// IssueEmailVerification generates a fresh opaque verification token for an
// unverified user, stores it (overwriting any previous one), and returns the raw
// token so the caller can email the verification link. Returns ErrAlreadyVerified
// if the account is already verified (nothing to do).
func (s *AuthService) IssueEmailVerification(userID int64) (string, error) {
	token := generateRandomString(48)
	res, err := s.db.Exec(`
		UPDATE users SET verification_token = $1, verification_sent_at = NOW()
		WHERE id = $2 AND COALESCE(email_verified, false) = false
	`, token, userID)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrVerificationTokenIssue, err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		// Either the user doesn't exist or is already verified.
		return "", ErrAlreadyVerified
	}
	return token, nil
}

// VerifyEmail marks the user owning the given token as verified, as long as the
// token is still valid (issued within the last 24h). The token is single-use:
// it is cleared on success. Returns the verified user's ID so the caller can
// follow up (e.g. send the welcome email), or ErrInvalidToken when no matching,
// unexpired token exists.
func (s *AuthService) VerifyEmail(token string) (int64, error) {
	if strings.TrimSpace(token) == "" {
		return 0, ErrInvalidToken
	}
	var userID int64
	err := s.db.QueryRow(`
		UPDATE users
		SET email_verified = true, verification_token = NULL, updated_at = NOW()
		WHERE verification_token = $1
		  AND verification_sent_at > NOW() - INTERVAL '24 hours'
		RETURNING id
	`, token).Scan(&userID)
	if err == sql.ErrNoRows {
		return 0, ErrInvalidToken
	} else if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrEmailVerify, err)
	}
	return userID, nil
}

// IssuePasswordReset generates a fresh opaque reset token for the account owning
// the given email and returns it together with the user's name so the caller can
// send a reset link. To avoid leaking which emails are registered, a missing or
// pending (not yet activated) account is NOT an error: token is returned empty
// and the caller simply skips sending the email.
func (s *AuthService) IssuePasswordReset(email string) (token, name string, err error) {
	token = generateRandomString(48)
	err = s.db.QueryRow(`
		UPDATE users SET reset_token = $1, reset_sent_at = NOW()
		WHERE email = $2 AND invite_token IS NULL
		RETURNING name
	`, token, strings.ToLower(strings.TrimSpace(email))).Scan(&name)
	if err == sql.ErrNoRows {
		// No such (activated) account: behave as success but with no token to send.
		return "", "", nil
	} else if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrPasswordResetTokenIssue, err)
	}
	return token, name, nil
}

// ResetPassword sets a new password for the user owning the given reset token, as
// long as the token is still valid (issued within the last hour). The token is
// single-use: it is cleared on success. Returns ErrWeakPassword when the new
// password is too weak, or ErrInvalidToken when no matching, unexpired token
// exists.
func (s *AuthService) ResetPassword(token, newPassword string) error {
	if strings.TrimSpace(token) == "" {
		return ErrInvalidToken
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPasswordHash, err)
	}

	var userID int64
	err = s.db.QueryRow(`
		UPDATE users
		SET password_hash = $1, reset_token = NULL, updated_at = NOW()
		WHERE reset_token = $2
		  AND reset_sent_at > NOW() - INTERVAL '1 hour'
		RETURNING id
	`, string(hashedPassword), token).Scan(&userID)
	if err == sql.ErrNoRows {
		return ErrInvalidToken
	} else if err != nil {
		return fmt.Errorf("%w: %w", ErrPasswordReset, err)
	}
	return nil
}

// GetOrganizationUsers returns all members of an organization. The role is the
// per-org membership role (not the identity's home role), so the same person can
// appear with different roles in different orgs.
func (s *AuthService) GetOrganizationUsers(orgID int64) ([]models.User, error) {
	rows, err := s.db.Query(`
		SELECT u.id, u.organization_id, u.email, u.name, m.role, u.created_at, u.updated_at, u.last_login_at,
		       (u.invite_token IS NOT NULL) AS pending
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.organization_id = $1
		ORDER BY m.created_at ASC
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUsersFetch, err)
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		var user models.User
		if err := rows.Scan(&user.ID, &user.OrganizationID, &user.Email, &user.Name, &user.Role, &user.CreatedAt, &user.UpdatedAt, &user.LastLoginAt, &user.Pending); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrUserScan, err)
		}
		users = append(users, user)
	}
	return users, nil
}

// InviteUser invites someone to an organization (admin only). Behaviour depends
// on whether the email already has an identity in Middle Monitor:
//
//   - Existing identity: a membership is added for this org (the same person can
//     belong to several orgs). They already have a password, so no activation is
//     needed — the returned token is empty, signalling the caller to send a
//     "you've been added" notification. ErrUserExists is returned only if they are
//     already a member of THIS org.
//   - New email: a pending identity is created (unverified, throwaway password)
//     together with its membership and an opaque invite_token. The returned token
//     is non-empty; the caller emails an activation link and the invitee sets
//     their password via AcceptInvitation.
func (s *AuthService) InviteUser(orgID int64, req models.InviteUserRequest) (*models.User, string, error) {
	if err := validateEmail(req.Email); err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, "", ErrNameRequired
	}
	if !IsValidRole(req.Role) {
		req.Role = RoleReadWrite
	}
	email := strings.ToLower(req.Email)

	// Is there already an identity for this email (in any org)?
	var existing models.User
	err := s.db.QueryRow(`SELECT id, email, name FROM users WHERE email = $1`, email).
		Scan(&existing.ID, &existing.Email, &existing.Name)
	if err != nil && err != sql.ErrNoRows {
		return nil, "", fmt.Errorf("%w: %w", ErrExistingUserCheck, err)
	}

	if err == nil {
		// Identity exists → just grant access to this org via a membership.
		var dummy int64
		mErr := s.db.QueryRow(`SELECT id FROM memberships WHERE user_id = $1 AND organization_id = $2`, existing.ID, orgID).Scan(&dummy)
		if mErr == nil {
			return nil, "", ErrUserExists // already a member of this org
		} else if mErr != sql.ErrNoRows {
			return nil, "", fmt.Errorf("%w: %w", ErrMembershipCheck, mErr)
		}
		if err := s.AddMembership(existing.ID, orgID, req.Role); err != nil {
			return nil, "", err
		}
		existing.OrganizationID = orgID
		existing.Role = req.Role
		existing.Pending = false
		return &existing, "", nil
	}

	// New identity → create a pending user + its membership atomically.
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(generateRandomString(32)), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrPasswordHash, err)
	}
	inviteToken := generateRandomString(48)

	tx, err := s.db.Begin()
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrTransactionStart, err)
	}
	defer tx.Rollback()

	var user models.User
	err = tx.QueryRow(`
		INSERT INTO users (organization_id, email, password_hash, name, role, email_verified, invite_token, invited_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, false, $6, NOW(), NOW(), NOW())
		RETURNING id, organization_id, email, name, role, email_verified, created_at, updated_at
	`, orgID, email, string(hashedPassword), req.Name, req.Role, inviteToken).Scan(
		&user.ID, &user.OrganizationID, &user.Email, &user.Name, &user.Role, &user.EmailVerified, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrUserCreate, err)
	}

	_, err = tx.Exec(`INSERT INTO memberships (user_id, organization_id, role, created_at) VALUES ($1, $2, $3, NOW())`, user.ID, orgID, req.Role)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrMembershipCreate, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrTransactionCommit, err)
	}

	user.Pending = true
	return &user, inviteToken, nil
}

// AcceptInvitation completes an invited account: it validates the invite token
// (issued within the last 7 days), sets the invitee's chosen password, marks the
// email verified and clears the token (single-use). Returns the activated user
// with their organization so the caller can issue a session. Returns
// ErrInvalidToken when no matching, unexpired invitation exists.
func (s *AuthService) AcceptInvitation(token, password string) (*models.UserWithOrg, error) {
	if strings.TrimSpace(token) == "" {
		return nil, ErrInvalidToken
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPasswordHash, err)
	}

	var userID int64
	err = s.db.QueryRow(`
		UPDATE users
		SET password_hash = $1, email_verified = true, invite_token = NULL, updated_at = NOW()
		WHERE invite_token = $2
		  AND invited_at > NOW() - INTERVAL '7 days'
		RETURNING id
	`, string(hashedPassword), token).Scan(&userID)
	if err == sql.ErrNoRows {
		return nil, ErrInvalidToken
	} else if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvitationAccept, err)
	}

	return s.GetUserByID(userID)
}

// AcceptInvitationAndLogin accepts an invitation (see AcceptInvitation) and, on
// success, issues a fresh access/refresh token pair so the freshly activated
// user is logged straight in.
func (s *AuthService) AcceptInvitationAndLogin(token, password string) (*models.UserWithOrg, *models.AuthTokens, error) {
	userWithOrg, err := s.AcceptInvitation(token, password)
	if err != nil {
		return nil, nil, err
	}
	tokens, err := s.generateTokens(&userWithOrg.User, userWithOrg.Organization.Slug, userWithOrg.Organization.MFARequired)
	if err != nil {
		return nil, nil, err
	}
	return userWithOrg, tokens, nil
}

// UpdateOrganization updates an organization's details
func (s *AuthService) UpdateOrganization(orgID int64, name string) (*models.Organization, error) {
	var org models.Organization
	err := s.db.QueryRow(`
		UPDATE organizations SET name = $2, updated_at = NOW()
		WHERE id = $1
		RETURNING id, name, slug, created_at, updated_at
	`, orgID, name).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedAt, &org.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrOrgNotFound
	} else if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOrganizationUpdate, err)
	}
	return &org, nil
}

// DeleteUser removes a user from an organization (admin only, cannot remove self).
// It revokes the org membership rather than deleting the identity, since the same
// person may still belong to other orgs. The identity is deleted only once it has
// no memberships left, to avoid leaving an orphaned account.
func (s *AuthService) DeleteUser(orgID, userID, requestingUserID int64) error {
	if userID == requestingUserID {
		return ErrSelfAccountDelete
	}

	result, err := s.db.Exec("DELETE FROM memberships WHERE user_id = $1 AND organization_id = $2", userID, orgID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMembershipRemove, err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrUserNotFound
	}

	// Garbage-collect the identity if it no longer belongs to any organization.
	var remaining int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM memberships WHERE user_id = $1", userID).Scan(&remaining); err != nil {
		return fmt.Errorf("%w: %w", ErrMembershipsCount, err)
	}
	if remaining == 0 {
		if _, err := s.db.Exec("DELETE FROM users WHERE id = $1", userID); err != nil {
			return fmt.Errorf("%w: %w", ErrOrphanedUserDelete, err)
		}
		return nil
	}

	// If we just removed the identity's home org, repoint it to a remaining one so
	// users.organization_id never dangles to an org they no longer belong to.
	if _, err := s.db.Exec(`
		UPDATE users SET organization_id = (
			SELECT organization_id FROM memberships WHERE user_id = $1 ORDER BY created_at ASC LIMIT 1
		)
		WHERE id = $1 AND organization_id = $2
	`, userID, orgID); err != nil {
		return fmt.Errorf("%w: %w", ErrHomeOrgRepoint, err)
	}
	return nil
}

// ValidRole reports whether role is one of the three supported roles.
func ValidRole(role string) bool {
	return role == "read_only" || role == "read_write" || role == "admin"
}

// UpdateUserRole changes a member's role within an organization. The per-org role
// lives in memberships; users.role is the denormalized copy of the member's active
// org, so it is only updated when this org is their active one. Refuses to demote
// the org's last admin (which would lock everyone out of admin actions).
func (s *AuthService) UpdateUserRole(orgID, userID int64, role string) error {
	if !ValidRole(role) {
		return ErrInvalidRole
	}

	// Confirm the target is a member of this org and read their current role.
	var current string
	err := s.db.QueryRow(`SELECT role FROM memberships WHERE user_id = $1 AND organization_id = $2`, userID, orgID).Scan(&current)
	if err == sql.ErrNoRows {
		return ErrUserNotFound
	} else if err != nil {
		return fmt.Errorf("%w: %w", ErrMembershipLoad, err)
	}
	if current == role {
		return nil
	}

	// Don't allow removing the last admin of the org.
	if current == "admin" && role != "admin" {
		var admins int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM memberships WHERE organization_id = $1 AND role = 'admin'`, orgID).Scan(&admins); err != nil {
			return fmt.Errorf("%w: %w", ErrAdminsCount, err)
		}
		if admins <= 1 {
			return ErrLastAdmin
		}
	}

	if _, err := s.db.Exec(`UPDATE memberships SET role = $1 WHERE user_id = $2 AND organization_id = $3`, role, userID, orgID); err != nil {
		return fmt.Errorf("%w: %w", ErrMembershipRoleUpdate, err)
	}
	// Keep the denormalized copy in sync only when this is the member's active org.
	if _, err := s.db.Exec(`UPDATE users SET role = $1, updated_at = NOW() WHERE id = $2 AND organization_id = $3`, role, userID, orgID); err != nil {
		return fmt.Errorf("%w: %w", ErrUserRoleSync, err)
	}
	return nil
}

// generateTokens creates JWT access and refresh tokens. orgMFARequired reflects the
// org's 2FA enforcement so the minted claims carry an up-to-date enrollment gate.
func (s *AuthService) generateTokens(user *models.User, orgSlug string, orgMFARequired bool) (*models.AuthTokens, error) {
	enrollmentRequired := orgMFARequired && !user.TOTPEnabled

	// Access token - 24 hours
	accessExpiry := time.Now().Add(24 * time.Hour)
	accessClaims := JWTClaims{
		UserID:                user.ID,
		OrganizationID:        user.OrganizationID,
		OrganizationSlug:      orgSlug,
		Email:                 user.Email,
		Role:                  user.Role,
		EmailVerified:         user.EmailVerified,
		MFAEnrollmentRequired: enrollmentRequired,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(accessExpiry),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   fmt.Sprintf("%d", user.ID),
		},
	}
	accessToken := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessTokenString, err := accessToken.SignedString(s.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAccessTokenSign, err)
	}

	// Refresh token - 7 days
	refreshExpiry := time.Now().Add(7 * 24 * time.Hour)
	refreshClaims := JWTClaims{
		UserID:                user.ID,
		OrganizationID:        user.OrganizationID,
		OrganizationSlug:      orgSlug,
		Email:                 user.Email,
		Role:                  user.Role,
		EmailVerified:         user.EmailVerified,
		MFAEnrollmentRequired: enrollmentRequired,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(refreshExpiry),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   fmt.Sprintf("%d", user.ID),
		},
	}
	refreshToken := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshTokenString, err := refreshToken.SignedString(s.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRefreshTokenSign, err)
	}

	return &models.AuthTokens{
		AccessToken:  accessTokenString,
		RefreshToken: refreshTokenString,
		ExpiresIn:    int64(24 * 60 * 60), // 24 hours in seconds
	}, nil
}

// generateMFAChallenge signs a short-lived (5 min) token that proves the password
// step succeeded. It carries Purpose "mfa" so AuthMiddleware refuses it as a bearer
// token; only CompleteMFALogin accepts it.
func (s *AuthService) generateMFAChallenge(userID int64) (string, error) {
	claims := JWTClaims{
		UserID:  userID,
		Purpose: mfaChallengePurpose,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   fmt.Sprintf("%d", userID),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrMFAChallengeSign, err)
	}
	return signed, nil
}

// --- Two-factor (TOTP) ---

const totpIssuer = "Middle Monitor"

// SetupTOTP starts (or restarts) enrollment for a user: it generates a fresh TOTP
// secret, stores it as pending (totp_enabled stays false), and returns the base32
// secret, the otpauth:// URL and a base64-encoded PNG QR code so the frontend can
// render it without a QR library. Enrollment is confirmed by EnableTOTP.
func (s *AuthService) SetupTOTP(userID int64, accountEmail string) (secret, otpauthURL, qrPNGBase64 string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: accountEmail,
	})
	if err != nil {
		return "", "", "", fmt.Errorf("%w: %w", ErrTOTPSecretGenerate, err)
	}

	// Store the pending secret; leave totp_enabled untouched (false until confirmed).
	if _, err := s.db.Exec(`UPDATE users SET totp_secret = $1, updated_at = NOW() WHERE id = $2`, key.Secret(), userID); err != nil {
		return "", "", "", fmt.Errorf("%w: %w", ErrTOTPSecretStore, err)
	}

	img, err := key.Image(220, 220)
	if err != nil {
		return "", "", "", fmt.Errorf("%w: %w", ErrQRCodeRender, err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", "", "", fmt.Errorf("%w: %w", ErrQRCodeEncode, err)
	}
	return key.Secret(), key.URL(), base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// EnableTOTP confirms enrollment: it validates the code against the pending secret,
// flips totp_enabled to true, and (re)generates a fresh set of single-use recovery
// codes, returning them in plaintext exactly once for the user to store.
func (s *AuthService) EnableTOTP(userID int64, code string) ([]string, error) {
	var secret sql.NullString
	if err := s.db.QueryRow(`SELECT totp_secret FROM users WHERE id = $1`, userID).Scan(&secret); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("%w: %w", ErrTOTPSecretLoad, err)
	}
	if !secret.Valid || secret.String == "" {
		return nil, ErrTOTPNotPending
	}
	// Authenticator apps display codes grouped as "123 456"; tolerate any whitespace.
	if !totp.Validate(strings.Join(strings.Fields(code), ""), secret.String) {
		return nil, ErrInvalidMFACode
	}

	codes, err := s.regenerateRecoveryCodes(userID)
	if err != nil {
		return nil, err
	}

	if _, err := s.db.Exec(`UPDATE users SET totp_enabled = true, updated_at = NOW() WHERE id = $1`, userID); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTOTPEnable, err)
	}
	return codes, nil
}

// DisableTOTP turns off a user's own two-factor authentication, clearing the secret
// and recovery codes. It is refused when the user's org enforces 2FA (in that case
// disabling would just bounce them back to the enrollment gate). Returns
// ErrMFARequiredByOrg in that case.
func (s *AuthService) DisableTOTP(userID int64) error {
	var orgRequires bool
	err := s.db.QueryRow(`
		SELECT COALESCE(o.mfa_required, false)
		FROM users u JOIN organizations o ON o.id = u.organization_id
		WHERE u.id = $1`, userID).Scan(&orgRequires)
	if err == sql.ErrNoRows {
		return ErrUserNotFound
	} else if err != nil {
		return fmt.Errorf("%w: %w", ErrOrgMFAPolicyCheck, err)
	}
	if orgRequires {
		return ErrMFARequiredByOrg
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTransactionStart, err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM user_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("%w: %w", ErrRecoveryCodesClear, err)
	}
	if _, err := tx.Exec(`UPDATE users SET totp_enabled = false, totp_secret = NULL, updated_at = NOW() WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("%w: %w", ErrTOTPDisable, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: %w", ErrTOTPDisableCommit, err)
	}
	return nil
}

// getUserForMFA loads the user + org and the stored TOTP secret in one place for the
// login completion path.
func (s *AuthService) getUserForMFA(userID int64) (*models.UserWithOrg, string, error) {
	userWithOrg, err := s.GetUserByID(userID)
	if err != nil {
		return nil, "", err
	}
	var secret sql.NullString
	if err := s.db.QueryRow(`SELECT totp_secret FROM users WHERE id = $1`, userID).Scan(&secret); err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrTOTPSecretLoad, err)
	}
	return userWithOrg, secret.String, nil
}

// verifySecondFactor accepts either a valid TOTP code for the secret or one unused
// recovery code (which it then consumes). Returns true on success.
func (s *AuthService) verifySecondFactor(userID int64, secret, code string) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	// Try as a TOTP code first (tolerating "123 456" grouping); fall back to a
	// recovery code, which is matched against the originally trimmed value.
	if secret != "" && totp.Validate(strings.Join(strings.Fields(code), ""), secret) {
		return true
	}
	return s.consumeRecoveryCode(userID, code)
}

// regenerateRecoveryCodes deletes any existing codes for the user and inserts a
// fresh batch (bcrypt-hashed), returning the plaintext codes.
func (s *AuthService) regenerateRecoveryCodes(userID int64) ([]string, error) {
	const count = 10
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTransactionStart, err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM user_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRecoveryCodesClear, err)
	}

	codes := make([]string, 0, count)
	for i := 0; i < count; i++ {
		code := generateRecoveryCode()
		hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrRecoveryCodeHash, err)
		}
		if _, err := tx.Exec(`INSERT INTO user_recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, string(hash)); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrRecoveryCodeStore, err)
		}
		codes = append(codes, code)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRecoveryCodesCommit, err)
	}
	return codes, nil
}

// consumeRecoveryCode checks the submitted code against the user's unused recovery
// codes; on a match it marks that code used (single-use) and returns true.
func (s *AuthService) consumeRecoveryCode(userID int64, code string) bool {
	rows, err := s.db.Query(`SELECT id, code_hash FROM user_recovery_codes WHERE user_id = $1 AND used_at IS NULL`, userID)
	if err != nil {
		return false
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var hash string
		if err := rows.Scan(&id, &hash); err != nil {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(code)) == nil {
			if _, err := s.db.Exec(`UPDATE user_recovery_codes SET used_at = NOW() WHERE id = $1`, id); err != nil {
				return false
			}
			return true
		}
	}
	return false
}

// generateRecoveryCode returns a human-friendly single-use code like "a1b2-c3d4".
func generateRecoveryCode() string {
	raw := generateRandomString(8)
	return raw[:4] + "-" + raw[4:]
}

// Helper functions

func validateEmail(email string) error {
	emailRegex := regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	if !emailRegex.MatchString(email) {
		return ErrInvalidEmail
	}
	return nil
}

// validateSlug enforces the URL identifier format: lowercase alphanumeric
// segments separated by single hyphens, 1-63 chars (no leading/trailing hyphen).
func validateSlug(slug string) error {
	if len(slug) < 1 || len(slug) > 63 {
		return ErrInvalidSlug
	}
	slugRegex := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	if !slugRegex.MatchString(slug) {
		return ErrInvalidSlug
	}
	return nil
}

func validatePassword(password string) error {
	if len(password) < 8 {
		return ErrWeakPassword
	}
	var hasUpper, hasLower, hasNumber bool
	for _, c := range password {
		switch {
		case unicode.IsUpper(c):
			hasUpper = true
		case unicode.IsLower(c):
			hasLower = true
		case unicode.IsNumber(c):
			hasNumber = true
		}
	}
	if !hasUpper || !hasLower || !hasNumber {
		return ErrWeakPassword
	}
	return nil
}

func generateSlug(name string) string {
	// Convert to lowercase
	slug := strings.ToLower(name)
	// Replace spaces with hyphens
	slug = strings.ReplaceAll(slug, " ", "-")
	// Remove non-alphanumeric characters (except hyphens)
	reg := regexp.MustCompile(`[^a-z0-9-]`)
	slug = reg.ReplaceAllString(slug, "")
	// Remove multiple consecutive hyphens
	reg = regexp.MustCompile(`-+`)
	slug = reg.ReplaceAllString(slug, "-")
	// Trim hyphens from start and end
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "org"
	}
	return slug
}

func generateRandomString(length int) string {
	b := make([]byte, length/2+1)
	rand.Read(b)
	return hex.EncodeToString(b)[:length]
}

// GenerateRandomString is the exported version
func GenerateRandomString(length int) string {
	return generateRandomString(length)
}
